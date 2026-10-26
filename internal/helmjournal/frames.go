package helmjournal

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"time"

	strictjson "sigs.k8s.io/json"
)

const journalMagic = "ASTER-HELM-JOURNAL-V1\n"
const maxFrames = 4 + 3*(32+2)

// Each frame has an explicit big-endian byte length, one canonical JSON value,
// an exact predecessor, and a checksum. Checksums detect corruption; they are
// not authentication against another process running as the same OS user.
type frame struct {
	Version                 int          `json:"version"`
	Sequence                int          `json:"sequence"`
	Previous                string       `json:"previous"`
	Event                   string       `json:"event"`
	ID                      string       `json:"id,omitempty"`
	Plan                    *Plan        `json:"plan,omitempty"`
	Index                   *int         `json:"index,omitempty"`
	ResourceVersion         string       `json:"resourceVersion,omitempty"`
	Outcome                 string       `json:"outcome,omitempty"`
	ObservedResourceVersion string       `json:"observedResourceVersion,omitempty"`
	Finished                *time.Time   `json:"finished,omitempty"`
	Install                 *InstallPlan `json:"install,omitempty"`
	UID                     string       `json:"uid,omitempty"`
	RequestDigest           string       `json:"requestDigest,omitempty"`
}

type envelope struct {
	Payload json.RawMessage `json:"payload"`
	SHA256  string          `json:"sha256"`
}

type journalState struct {
	snapshot Snapshot
	sequence int
	digest   string
	active   int
	intent   bool
}

func newState() journalState { return journalState{active: -1} }

func (s journalState) clone() journalState {
	s.snapshot = cloneSnapshot(s.snapshot)
	return s
}

func encodeFrame(f frame) ([]byte, string, error) {
	payload, err := json.Marshal(f)
	if err != nil {
		return nil, "", ErrInvalid
	}
	sum := sha256.Sum256(payload)
	data, err := json.Marshal(envelope{Payload: payload, SHA256: hex.EncodeToString(sum[:])})
	if err != nil || len(data) > MaxFrameBytes {
		return nil, "", ErrBudget
	}
	all := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(all, uint32(len(data)))
	copy(all[4:], data)
	sum = sha256.Sum256(data)
	return all, hex.EncodeToString(sum[:]), nil
}

func decodeFrame(data []byte) (frame, string, error) {
	var e envelope
	var f frame
	issues, err := strictjson.UnmarshalStrict(data, &e)
	if err != nil || len(issues) != 0 || len(e.Payload) == 0 || !validHex(e.SHA256, 64) {
		return f, "", ErrCorrupt
	}
	canonical, err := json.Marshal(e)
	if err != nil || !bytes.Equal(canonical, data) {
		return f, "", ErrCorrupt
	}
	sum := sha256.Sum256(e.Payload)
	if e.SHA256 != hex.EncodeToString(sum[:]) {
		return f, "", ErrCorrupt
	}
	issues, err = strictjson.UnmarshalStrict(e.Payload, &f)
	if err != nil || len(issues) != 0 {
		return f, "", ErrCorrupt
	}
	canonical, err = json.Marshal(f)
	if err != nil || !bytes.Equal(canonical, e.Payload) {
		return f, "", ErrCorrupt
	}
	sum = sha256.Sum256(data)
	return f, hex.EncodeToString(sum[:]), nil
}

func (s *journalState) apply(f frame, digest, expectedID string) error {
	if f.Version == 2 {
		return s.applyInstall(f, digest, expectedID)
	}
	if f.Install != nil || f.UID != "" || f.RequestDigest != "" || s.sequence != 0 && s.snapshot.Version != 1 {
		return ErrCorrupt
	}
	if f.Version != 1 || f.Sequence != s.sequence+1 || f.Sequence > maxFrames || f.Previous != s.digest || s.snapshot.Closed {
		return ErrCorrupt
	}
	if s.sequence == 0 {
		if f.Event != "start" || f.ID != expectedID || !validHex(f.ID, 32) || f.Plan == nil || !validPlan(*f.Plan) || f.Index != nil ||
			f.ResourceVersion != "" || f.Outcome != "" || f.ObservedResourceVersion != "" || f.Finished != nil {
			return ErrCorrupt
		}
		s.snapshot = initialSnapshot(f.ID, *f.Plan)
	} else {
		if f.ID != "" || f.Plan != nil {
			return ErrCorrupt
		}
		switch f.Event {
		case "intent":
			if f.Index == nil || f.Outcome != "" || f.ObservedResourceVersion != "" || f.Finished != nil {
				return ErrCorrupt
			}
			i := *f.Index
			if i < 0 || i >= len(s.snapshot.Effects) || s.intent || s.snapshot.Effects[i].Outcome != NotStarted || !validIdentity(f.ResourceVersion, 256) {
				return ErrCorrupt
			}
			for j := 0; j < i; j++ {
				if s.snapshot.Effects[j].Outcome != Completed {
					return ErrCorrupt
				}
			}
			for j := i + 1; j < len(s.snapshot.Effects); j++ {
				if s.snapshot.Effects[j].Outcome != NotStarted {
					return ErrCorrupt
				}
			}
			e := &s.snapshot.Effects[i]
			if e.Kind != "finish" && f.ResourceVersion != e.ResourceVersion {
				return ErrCorrupt
			}
			if e.Kind == "finish" && f.ResourceVersion != s.snapshot.Effects[0].ObservedResourceVersion {
				return ErrCorrupt
			}
			e.ResourceVersion, e.Outcome = f.ResourceVersion, Unknown
			s.active, s.intent = i, true
		case "result":
			if f.Index == nil || f.Finished != nil || f.ResourceVersion != "" || *f.Index != s.active || s.active < 0 {
				return ErrCorrupt
			}
			e := &s.snapshot.Effects[s.active]
			if s.intent {
				switch f.Outcome {
				case AcceptedPending:
					if f.ObservedResourceVersion != "" && !validIdentity(f.ObservedResourceVersion, 256) {
						return ErrCorrupt
					}
					if e.Kind != "delete" && (f.ObservedResourceVersion == "" || f.ObservedResourceVersion == e.ResourceVersion) {
						return ErrCorrupt
					}
				case Rejected, Unknown:
					if f.ObservedResourceVersion != "" {
						return ErrCorrupt
					}
				default:
					return ErrCorrupt
				}
			} else if e.Outcome != AcceptedPending || f.Outcome != Completed || f.ObservedResourceVersion != e.ObservedResourceVersion {
				return ErrCorrupt
			}
			e.Outcome, e.ObservedResourceVersion = f.Outcome, f.ObservedResourceVersion
			s.intent = false
		case "finish":
			if f.Index != nil || f.ResourceVersion != "" || f.Outcome != "" || f.ObservedResourceVersion != "" || f.Finished == nil ||
				!validTime(*f.Finished) || f.Finished.Before(s.snapshot.Plan.Started) {
				return ErrCorrupt
			}
			s.snapshot.Closed, s.snapshot.Finished = true, *f.Finished
		default:
			return ErrCorrupt
		}
	}
	s.sequence, s.digest, s.snapshot.Digest = f.Sequence, digest, digest
	return nil
}

// parse preserves only a fully checked prefix. It never repairs/truncates data.
// Missing or malformed start frames still produce an explicit corrupt record.
func parse(data []byte, id string) journalState {
	s := newState()
	s.snapshot = Snapshot{ID: id, Integrity: IntegrityValid}
	corrupt := func() journalState {
		s.snapshot.Corrupt, s.snapshot.Integrity = true, IntegrityCorrupt
		return s
	}
	if len(data) > MaxOperationBytes || !bytes.HasPrefix(data, []byte(journalMagic)) {
		return corrupt()
	}
	data = data[len(journalMagic):]
	for len(data) > 0 {
		if len(data) < 4 {
			return corrupt()
		}
		n := int(binary.BigEndian.Uint32(data))
		data = data[4:]
		if n <= 0 || n > MaxFrameBytes || n > len(data) {
			return corrupt()
		}
		f, digest, err := decodeFrame(data[:n])
		if err != nil {
			return corrupt()
		}
		next := s.clone()
		if next.apply(f, digest, id) != nil {
			return corrupt()
		}
		s = next
		data = data[n:]
	}
	if s.sequence == 0 {
		return corrupt()
	}
	return s
}
