package helmjournal

import (
	"context"
	"time"
)

// InstallWriter shares the v1 durable writer and lease but not its operation
// methods. It records caller-verified outcomes; it cannot send a request.
// Its private owned wrapper prevents copies or caller assignments retargeting it.
type InstallWriter struct{ writer *Writer }

func (s *Store) BeginInstall(p InstallPlan) (*InstallWriter, error) {
	if s == nil || s.storeState == nil {
		return nil, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return nil, err
	}
	if s.active != nil {
		return nil, ErrBusy
	}
	if !validInstallPlan(p) {
		return nil, ErrInvalid
	}
	if _, err := s.admissionLocked(context.Background(), AdmissionScope{Binding: p.Binding, Namespace: p.Namespace, Release: p.Release}); err != nil {
		return nil, err
	}
	p = cloneInstallPlan(p)
	p.Started, p.Expires = p.Started.UTC(), p.Expires.UTC()
	w, err := s.beginFrame(frame{Version: 2, Sequence: 1, Event: "install-start", Install: &p})
	if err != nil {
		return nil, err
	}
	return &InstallWriter{writer: w}, nil
}

func (w *InstallWriter) raw() *Writer {
	if w == nil || w.writer == nil || w.writer.writerState == nil {
		return nil
	}
	return w.writer
}
func (w *InstallWriter) ID() string {
	if x := w.raw(); x != nil {
		return x.ID()
	}
	return ""
}

// Intent is synchronized before returning. Before dispatch it recovers Unknown,
// even if a process dies before sending. Create intents have no fabricated UID/RV;
// the final transition uses only the earlier recorded and observed Secret.
// The supplied digest must match frozen create bytes; for deploy it binds the
// exact freshly constructed conditional patch (which could not exist pre-create).
func (w *InstallWriter) Intent(index int, requestDigest string) error {
	x := w.raw()
	if x == nil {
		return ErrClosed
	}
	x.store.mu.Lock()
	defer x.store.mu.Unlock()
	if err := x.check(); err != nil {
		return err
	}
	if x.state.snapshot.Version != 2 {
		return ErrInvalid
	}
	f := frame{Event: "install-intent", Index: &index, RequestDigest: requestDigest}
	if index == len(x.state.snapshot.Effects)-1 {
		e := x.state.snapshot.Effects[0]
		f.UID, f.ResourceVersion = e.UID, e.ObservedResourceVersion
	}
	return x.append(f)
}

// Response records an accepted, rejected or uncertain response only. Accepted
// is not Completed: Observe must independently match the real UID and RV.
func (w *InstallWriter) Response(index int, outcome, uid, rv string) error {
	return w.record("install-response", index, outcome, uid, rv)
}
func (w *InstallWriter) Observe(index int, uid, rv string) error {
	return w.record("install-observed", index, Completed, uid, rv)
}
func (w *InstallWriter) record(event string, index int, outcome, uid, rv string) error {
	x := w.raw()
	if x == nil {
		return ErrClosed
	}
	x.store.mu.Lock()
	defer x.store.mu.Unlock()
	if err := x.check(); err != nil {
		return err
	}
	if x.state.snapshot.Version != 2 {
		return ErrInvalid
	}
	return x.append(frame{Event: event, Index: &index, Outcome: outcome, UID: uid, ObservedResourceVersion: rv})
}
func (w *InstallWriter) Finish(at time.Time) error {
	x := w.raw()
	if x == nil {
		return ErrClosed
	}
	return x.Finish(at)
}
func (w *InstallWriter) Abort() error {
	x := w.raw()
	if x == nil {
		return nil
	}
	return x.Abort()
}
