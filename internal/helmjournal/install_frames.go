package helmjournal

// Version 2 shares the exact v1 length/checksum framing and filename namespace.
// Old readers encounter version=2 and reject it, never skip it. Each file has
// one payload version/action; a valid checksum does not permit mixing schemas.
func (s *journalState) applyInstall(f frame, digest, id string) error {
	if f.Version != 2 || f.Sequence != s.sequence+1 || f.Sequence > maxFrames || f.Previous != s.digest || s.snapshot.Closed || f.Plan != nil ||
		s.sequence != 0 && s.snapshot.Version != 2 {
		return ErrCorrupt
	}
	if s.sequence == 0 {
		if f.Event != "install-start" || f.ID != id || !validHex(id, 32) || f.Install == nil || !validInstallPlan(*f.Install) ||
			f.RequestDigest != "" || f.Index != nil || f.UID != "" || f.ResourceVersion != "" || f.Outcome != "" || f.ObservedResourceVersion != "" || f.Finished != nil {
			return ErrCorrupt
		}
		s.snapshot = initialInstallSnapshot(id, *f.Install)
	} else {
		if f.ID != "" || f.Install != nil {
			return ErrCorrupt
		}
		switch f.Event {
		case "install-intent":
			if f.Index == nil || !validHex(f.RequestDigest, 64) || f.Outcome != "" || f.ObservedResourceVersion != "" || f.Finished != nil || s.intent {
				return ErrCorrupt
			}
			i := *f.Index
			if i < 0 || i >= len(s.snapshot.Effects) || s.snapshot.Effects[i].Outcome != NotStarted {
				return ErrCorrupt
			}
			for j, e := range s.snapshot.Effects {
				if j < i && e.Outcome != Completed || j > i && e.Outcome != NotStarted {
					return ErrCorrupt
				}
			}
			e := &s.snapshot.Effects[i]
			if e.Kind == "deploy" {
				first := s.snapshot.Effects[0]
				if !validIdentity(f.UID, 128) || f.UID != first.UID || !validIdentity(f.ResourceVersion, 256) || f.ResourceVersion != first.ObservedResourceVersion {
					return ErrCorrupt
				}
				e.UID, e.ResourceVersion, e.ContentDigest = f.UID, f.ResourceVersion, f.RequestDigest
			} else if f.UID != "" || f.ResourceVersion != "" || f.RequestDigest != e.ContentDigest {
				return ErrCorrupt
			}
			e.Outcome = Unknown
			s.active, s.intent = i, true
		case "install-response", "install-observed":
			if f.RequestDigest != "" || f.Index == nil || f.Finished != nil || f.ResourceVersion != "" || s.active < 0 || *f.Index != s.active {
				return ErrCorrupt
			}
			e := &s.snapshot.Effects[s.active]
			if f.Event == "install-response" {
				if !s.intent {
					return ErrCorrupt
				}
				switch f.Outcome {
				case AcceptedPending:
					if !validIdentity(f.UID, 128) || !validIdentity(f.ObservedResourceVersion, 256) {
						return ErrCorrupt
					}
					if e.Kind == "deploy" {
						if f.UID != e.UID || f.ObservedResourceVersion == e.ResourceVersion {
							return ErrCorrupt
						}
					} else {
						for j := 0; j < s.active; j++ {
							if s.snapshot.Effects[j].UID == f.UID {
								return ErrCorrupt
							}
						}
					}
					e.UID, e.ObservedResourceVersion = f.UID, f.ObservedResourceVersion
				case Rejected, Unknown:
					if f.UID != "" || f.ObservedResourceVersion != "" {
						return ErrCorrupt
					}
				default:
					return ErrCorrupt
				}
				e.Outcome = f.Outcome
				s.intent = false
			} else {
				if s.intent || e.Outcome != AcceptedPending || f.Outcome != Completed || f.UID != e.UID || f.ObservedResourceVersion != e.ObservedResourceVersion {
					return ErrCorrupt
				}
				e.Outcome = Completed
			}
		case "install-finish":
			if f.RequestDigest != "" || f.Index != nil || f.UID != "" || f.ResourceVersion != "" || f.Outcome != "" || f.ObservedResourceVersion != "" ||
				f.Finished == nil || !validTime(*f.Finished) || f.Finished.Before(s.snapshot.Install.Started) {
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
