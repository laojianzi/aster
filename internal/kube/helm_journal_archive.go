package kube

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
)

// HelmJournalArchiveSession shares private state across value copies. The
// worker alone owns the original Store, creates/consumes the core consent and
// releases the OS lock. No UI callback is needed for cleanup, even if dropped.
// It contains no Backend/client and cannot issue a cluster request.
type HelmJournalArchiveSession struct{ state *helmJournalArchiveState }
type helmJournalArchiveState struct {
	cancel    context.CancelFunc
	ctx       context.Context
	command   chan string
	done      chan struct{}
	used      atomic.Bool
	id        string
	history   *HelmUninstallRecovery
	bytes     int64
	expires   time.Time
	inventory helmjournal.Admission
	result    helmjournal.ArchiveResult
	err       error
}

func StartHelmJournalArchive(ctx context.Context, path string, history *HelmUninstallRecovery) (*HelmJournalArchiveSession, error) {
	if ctx == nil || history == nil || history.archived || history.identity == nil {
		return nil, helmjournal.ErrArchiveConsent
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, err := helmjournal.OpenExisting(path)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*HelmJournalArchiveSession, error) { return nil, errors.Join(err, store.Close()) }
	review, err := store.PrepareLoadedArchive(ctx, history.identity)
	if err != nil {
		return fail(err)
	}
	if review.Record().ID != history.id || review.Record().Digest != history.digest {
		return fail(helmjournal.ErrArchiveConsent)
	}
	inventory, err := store.Inventory(ctx)
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithDeadline(ctx, review.ExpiresAt())
	s := &helmJournalArchiveState{cancel: cancel, ctx: ctx, command: make(chan string, 1), done: make(chan struct{}),
		history: history, id: history.id, bytes: review.Bytes(), expires: review.ExpiresAt(), inventory: inventory}
	go func() {
		out := helmjournal.ArchiveResult{RecordID: s.id, Bytes: s.bytes, Outcome: helmjournal.ArchiveNotApplied}
		var runErr error
		defer func() {
			s.result, s.err = out, errors.Join(runErr, store.Close())
			cancel()
			close(s.done) // publishes the immutable final result, after releasing the lock
		}()
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
		case id := <-s.command:
			consent, confirmErr := review.Confirm(id)
			if confirmErr != nil {
				runErr = confirmErr
				return
			}
			stop := context.AfterFunc(ctx, consent.Discard)
			defer stop()
			defer consent.Discard()
			out, runErr = store.Archive(ctx, consent)
		}
	}()
	return &HelmJournalArchiveSession{state: s}, nil
}

// Confirm queues at most one independent exact-ID acknowledgement. Even an
// invalid first attempt consumes the session and causes deterministic cleanup.
func (s *HelmJournalArchiveSession) Confirm(id string, acknowledged bool) error {
	if s == nil || s.state == nil {
		return helmjournal.ErrArchiveConsent
	}
	x := s.state
	if !x.used.CompareAndSwap(false, true) {
		return helmjournal.ErrArchiveConsent
	}
	if !acknowledged || id != x.id {
		x.cancel()
		return helmjournal.ErrArchiveConsent
	}
	if err := x.ctx.Err(); err != nil {
		x.cancel()
		return err
	}
	select {
	case x.command <- id:
		return nil
	case <-x.ctx.Done():
		return x.ctx.Err()
	}
}
func (s *HelmJournalArchiveSession) Cancel() {
	if s != nil && s.state != nil {
		s.state.cancel()
	}
}
func (s *HelmJournalArchiveSession) Done() <-chan struct{} {
	if s == nil || s.state == nil {
		c := make(chan struct{})
		close(c)
		return c
	}
	return s.state.done
}

// Result can be sampled without blocking and without racing a worker. A verified
// Archived effect remains visible alongside a late cancellation or close error.
func (s *HelmJournalArchiveSession) Result() (helmjournal.ArchiveResult, error, bool) {
	if s == nil || s.state == nil {
		return helmjournal.ArchiveResult{}, helmjournal.ErrArchiveConsent, true
	}
	select {
	case <-s.state.done:
		return s.state.result, s.state.err, true
	default:
		return helmjournal.ArchiveResult{}, nil, false
	}
}
func (s *HelmJournalArchiveSession) ID() string {
	if s == nil || s.state == nil {
		return ""
	}
	return s.state.id
}
func (s *HelmJournalArchiveSession) Bytes() int64 {
	if s == nil || s.state == nil {
		return 0
	}
	return s.state.bytes
}
func (s *HelmJournalArchiveSession) ExpiresAt() time.Time {
	if s == nil || s.state == nil {
		return time.Time{}
	}
	return s.state.expires
}
func (s *HelmJournalArchiveSession) Inventory() helmjournal.Admission {
	if s == nil || s.state == nil {
		return helmjournal.Admission{}
	}
	return s.state.inventory
}

func (j *HelmUninstallJournal) ArchivedIDs() ([]string, error) {
	if j == nil || j.store == nil {
		return nil, helmjournal.ErrClosed
	}
	return j.store.ListArchived()
}
func (j *HelmUninstallJournal) RecoverArchived(id string) (*HelmUninstallRecovery, error) {
	return j.recoverIdentified(id, true)
}
func (r *HelmUninstallRecovery) Archived() bool { return r != nil && r.archived }

// MatchesHistory rejects a different loaded projection, including the same ID.
func (s *HelmJournalArchiveSession) MatchesHistory(r *HelmUninstallRecovery) bool {
	return s != nil && s.state != nil && r != nil && s.state.history == r
}
