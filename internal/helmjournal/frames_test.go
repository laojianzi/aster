package helmjournal

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const fixtureID = "0123456789abcdef0123456789abcdef"

func intPointer(i int) *int { return &i }

func fixtureFrames(t *testing.T, p Plan, following ...frame) []byte {
	t.Helper()
	frames := append([]frame{{Event: "start", ID: fixtureID, Plan: &p}}, following...)
	data := []byte(journalMagic)
	previous := ""
	for i, f := range frames {
		if f.Version == 0 {
			f.Version = 1
		}
		if f.Sequence == 0 {
			f.Sequence = i + 1
		}
		if f.Previous == "" {
			f.Previous = previous
		}
		encoded, digest, err := encodeFrame(f)
		mustJournal(t, err)
		data = append(data, encoded...)
		previous = digest
	}
	return data
}

func rawPayloadFrame(t *testing.T, payload []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(payload)
	data, err := json.Marshal(envelope{Payload: payload, SHA256: hex.EncodeToString(sum[:])})
	mustJournal(t, err)
	out := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(out, uint32(len(data)))
	copy(out[4:], data)
	return out
}

func TestHelmJournalStrictFramesAndTransitions(t *testing.T) {
	p := testPlan()
	now := time.Now().UTC()
	old := p.Started.Add(-time.Second)
	intent := frame{Event: "intent", Index: intPointer(0), ResourceVersion: "7"}
	ack := frame{Event: "result", Index: intPointer(0), Outcome: AcceptedPending, ObservedResourceVersion: "8"}
	complete := frame{Event: "result", Index: intPointer(0), Outcome: Completed, ObservedResourceVersion: "8"}
	cases := map[string][]frame{
		"version":                 {{Version: 2, Event: "intent", Index: intPointer(0), ResourceVersion: "7"}},
		"sequence":                {{Sequence: 40, Event: "intent", Index: intPointer(0), ResourceVersion: "7"}},
		"predecessor":             {{Previous: strings.Repeat("0", 64), Event: "intent", Index: intPointer(0), ResourceVersion: "7"}},
		"unknown-event":           {{Event: "resume"}},
		"extra-plan":              {{Event: "intent", Index: intPointer(0), ResourceVersion: "7", Plan: &p}},
		"negative-index":          {{Event: "intent", Index: intPointer(-1), ResourceVersion: "7"}},
		"large-index":             {{Event: "intent", Index: intPointer(34), ResourceVersion: "7"}},
		"skip-begin":              {{Event: "intent", Index: intPointer(1), ResourceVersion: "21"}},
		"changed-begin-rv":        {{Event: "intent", Index: intPointer(0), ResourceVersion: "8"}},
		"duplicate-intent":        {intent, intent},
		"out-of-order-intent":     {intent, {Event: "intent", Index: intPointer(1), ResourceVersion: "21"}},
		"result-without-intent":   {ack},
		"unrelated-result":        {intent, {Event: "result", Index: intPointer(1), Outcome: AcceptedPending, ObservedResourceVersion: "23"}},
		"direct-completion":       {intent, complete},
		"unknown-outcome":         {intent, {Event: "result", Index: intPointer(0), Outcome: "Resumed"}},
		"ack-empty-rv":            {intent, {Event: "result", Index: intPointer(0), Outcome: AcceptedPending}},
		"ack-same-rv":             {intent, {Event: "result", Index: intPointer(0), Outcome: AcceptedPending, ObservedResourceVersion: "7"}},
		"rejection-rv":            {intent, {Event: "result", Index: intPointer(0), Outcome: Rejected, ObservedResourceVersion: "8"}},
		"unknown-rv":              {intent, {Event: "result", Index: intPointer(0), Outcome: Unknown, ObservedResourceVersion: "8"}},
		"complete-changed-rv":     {intent, ack, {Event: "result", Index: intPointer(0), Outcome: Completed, ObservedResourceVersion: "9"}},
		"pending-next-intent":     {intent, ack, {Event: "intent", Index: intPointer(1), ResourceVersion: "21"}},
		"rejected-next-intent":    {intent, {Event: "result", Index: intPointer(0), Outcome: Rejected}, {Event: "intent", Index: intPointer(1), ResourceVersion: "21"}},
		"unknown-next-intent":     {intent, {Event: "result", Index: intPointer(0), Outcome: Unknown}, {Event: "intent", Index: intPointer(1), ResourceVersion: "21"}},
		"changed-target-rv":       {intent, ack, complete, {Event: "intent", Index: intPointer(1), ResourceVersion: "99"}},
		"premature-finish-intent": {intent, ack, complete, {Event: "intent", Index: intPointer(3), ResourceVersion: "8"}},
		"terminal-before-start":   {{Event: "finish", Finished: &old}},
		"terminal-with-index":     {{Event: "finish", Finished: &now, Index: intPointer(0)}},
		"frame-after-terminal":    {{Event: "finish", Finished: &now}, intent},
	}
	for name, frames := range cases {
		t.Run(name, func(t *testing.T) {
			data := fixtureFrames(t, p, frames...)
			snapshot := parse(data, fixtureID).snapshot
			if !snapshot.Corrupt || snapshot.Integrity != IntegrityCorrupt || snapshot.ID != fixtureID {
				t.Fatal("malformed journal was accepted")
			}
		})
	}
	t.Run("checked-prefix-and-torn-tail", func(t *testing.T) {
		good := fixtureFrames(t, p, intent, ack, complete)
		valid := parse(good, fixtureID).snapshot
		if valid.Corrupt || valid.Effects[0].Outcome != Completed {
			t.Fatal("valid prefix rejected")
		}
		for _, tail := range [][]byte{{1}, {0, 0, 0, 2, '{'}, {0, 0, 0, 0}, bytes.Repeat([]byte{0xff}, 4)} {
			bad := parse(append(bytes.Clone(good), tail...), fixtureID).snapshot
			if !bad.Corrupt || bad.Digest != valid.Digest || bad.Effects[0].Outcome != Completed || bad.Effects[1].Outcome != NotStarted {
				t.Fatal("corrupt tail erased or promoted checked history")
			}
		}
	})
	t.Run("strict-start-envelope", func(t *testing.T) {
		start := frame{Version: 1, Sequence: 1, Event: "start", ID: fixtureID, Plan: &p}
		payload, err := json.Marshal(start)
		mustJournal(t, err)
		badPayloads := [][]byte{
			append([]byte(`{"version":1,`), payload[1:]...),
			append([]byte(`{"privateRawPayload":"PRIVATE_CONTENT",`), payload[1:]...),
			[]byte("null"),
			bytes.Replace(payload, []byte(`"version":1`), []byte(`"version":1.0`), 1),
			bytes.Replace(payload, []byte(fixtureID), []byte(strings.Repeat("f", 32)), 1),
		}
		for _, raw := range badPayloads {
			if !parse(append([]byte(journalMagic), rawPayloadFrame(t, raw)...), fixtureID).snapshot.Corrupt {
				t.Fatal("malformed canonical payload accepted despite valid checksum")
			}
		}
		good := fixtureFrames(t, p)
		for _, bad := range [][]byte{nil, []byte(journalMagic), good[:len(good)-1], append(bytes.Clone(good), good...), bytes.Replace(good, []byte("sha256"), []byte("sha257"), 1)} {
			if !parse(bad, fixtureID).snapshot.Corrupt {
				t.Fatal("invalid framing accepted")
			}
		}
		bad := bytes.Clone(good)
		bad[len(bad)-3] ^= 1
		if !parse(bad, fixtureID).snapshot.Corrupt {
			t.Fatal("checksum corruption accepted")
		}
	})
	t.Run("plan-validation", func(t *testing.T) {
		changes := map[string]func(*Plan){
			"binding":         func(p *Plan) { p.Binding = "credential-not-a-binding" },
			"preparation":     func(p *Plan) { p.PreparationID = "expired-consent" },
			"namespace":       func(p *Plan) { p.Namespace = "../other" },
			"status":          func(p *Plan) { p.Revision.Status = "uninstalling" },
			"uid":             func(p *Plan) { p.Revision.UID = "private\ncontrol" },
			"rv":              func(p *Plan) { p.Revision.ResourceVersion = strings.Repeat("x", 257) },
			"zero-time":       func(p *Plan) { p.Started = time.Time{} },
			"expiry":          func(p *Plan) { p.Expires = p.Started },
			"extended-expiry": func(p *Plan) { p.Expires = p.Started.Add(31 * time.Second) },
			"no-targets":      func(p *Plan) { p.Targets = nil },
			"too-many-targets": func(p *Plan) {
				for len(p.Targets) < 33 {
					p.Targets = append(p.Targets, p.Targets[0])
				}
			},
			"duplicate-target":     func(p *Plan) { p.Targets[1] = p.Targets[0] },
			"duplicate-target-uid": func(p *Plan) { p.Targets[1].UID = p.Targets[0].UID },
			"target-ascii-space":   func(p *Plan) { p.Targets[0].UID = "unsafe identity" },
			"target-non-ascii":     func(p *Plan) { p.Targets[0].ResourceVersion = "caf\u00e9" },
			"target-kind":          func(p *Plan) { p.Targets[0].Kind = "Secret" },
			"target-resource":      func(p *Plan) { p.Targets[0].Resource = "secrets" },
			"target-namespace":     func(p *Plan) { p.Targets[0].Namespace = "other" },
			"target-name":          func(p *Plan) { p.Targets[0].Name = "../token" },
			"target-uid":           func(p *Plan) { p.Targets[0].UID = "\u202esecret" },
		}
		for name, change := range changes {
			t.Run(name, func(t *testing.T) {
				s, _ := testStore(t)
				bad := testPlan()
				change(&bad)
				if _, err := s.Begin(bad); !errors.Is(err, ErrInvalid) {
					t.Fatal("invalid plan allocated a writer")
				}
				ids, err := s.List()
				mustJournal(t, err)
				if len(ids) != 0 {
					t.Fatal("invalid plan left a journal file")
				}
				if !parse(fixtureFrames(t, bad), fixtureID).snapshot.Corrupt {
					t.Fatal("invalid stored plan parsed as valid")
				}
			})
		}
	})
	t.Run("finish-binds-real-storage-rv", func(t *testing.T) {
		base := []frame{intent, ack, complete}
		for i, rv := range []string{"21", "22"} {
			base = append(base, frame{Event: "intent", Index: intPointer(i + 1), ResourceVersion: rv}, frame{Event: "result", Index: intPointer(i + 1), Outcome: AcceptedPending}, frame{Event: "result", Index: intPointer(i + 1), Outcome: Completed})
		}
		for _, rv := range []string{"7", "hypothetical-rv", "9"} {
			frames := append(append([]frame(nil), base...), frame{Event: "intent", Index: intPointer(3), ResourceVersion: rv})
			if !parse(fixtureFrames(t, p, frames...), fixtureID).snapshot.Corrupt {
				t.Fatal("finish accepted an unobserved storage version")
			}
		}
	})
}

func FuzzHelmJournalFrames(f *testing.F) {
	f.Add([]byte(journalMagic))
	f.Add([]byte("PRIVATE_CORRUPT_DATA"))
	f.Add([]byte{0, 0, 0, 255})
	f.Fuzz(func(t *testing.T, data []byte) {
		s := parse(data, fixtureID).snapshot
		if s.Corrupt {
			if s.Integrity != IntegrityCorrupt {
				t.Fatal("corruption flag disagreement")
			}
			return
		}
		if s.Integrity != IntegrityValid || s.ID != fixtureID || !validPlan(s.Plan) || len(s.Effects) != len(s.Plan.Targets)+2 || !validHex(s.Digest, 64) {
			t.Fatal("parser published an invalid recovery projection")
		}
	})
}
