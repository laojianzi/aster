package kube

import (
	"context"
	"errors"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
)

type installExecutionJournal interface {
	Intent(int, string) error
	Response(int, string, string, string) error
	Observe(int, string, string) error
}
type installExecutionRun struct {
	b                *Backend
	ctx              context.Context
	active, dispatch func() error
	state            *installDryRunState
	session          string
	journal          installExecutionJournal
	out              *HelmInstallExecution
	row              helmrelease.Revision
	objects          [][]byte
	remaining        int
}

func (r *installExecutionRun) run() error {
	r.objects = make([][]byte, r.state.plan.Len())
	defer func() {
		for _, v := range r.objects {
			clear(v)
		}
	}()
	if err := r.active(); err != nil {
		return err
	}
	fresh, err := r.b.PrepareHelmInstall(r.ctx, &r.state.candidate, r.session, true)
	if err != nil {
		return r.readError(err)
	}
	if fresh.namespaceUID != r.state.preparation.namespaceUID || fresh.namespaceRV != r.state.preparation.namespaceRV {
		return ErrHelmInstallExecutionChanged
	}
	if err = r.discovery(); err != nil {
		return err
	}
	for i := 0; i < r.state.plan.Len(); i++ {
		if err = r.guard(); err != nil {
			return err
		}
		resource, name := r.state.plan.Identity(i)
		kind := "ConfigMap"
		if resource == "secrets" {
			kind = "Secret"
		}
		if err = r.b.installAbsent(r.ctx, &r.state.candidate, kind, resource, name, &r.remaining); err != nil {
			return r.readError(err)
		}
		if err = r.guard(); err != nil {
			return err
		}
		body := r.state.plan.SensitiveBody(i)
		response, rejected, requestErr := r.mutate(i, body)
		clear(body)
		if requestErr != nil {
			clear(response)
			return r.failure(i, rejected, requestErr)
		}
		uid, rv, validationErr := validateInstallPersistentCreate(r.state.plan, i, response)
		for j := 0; j < i; j++ {
			if uid == r.out.effects[j].UID {
				validationErr = ErrHelmInstallExecutionResponse
			}
		}
		if validationErr != nil {
			clear(response)
			return r.failure(i, false, ErrHelmInstallExecutionUnknown)
		}
		e := &r.out.effects[i]
		e.UID, e.ObservedResourceVersion, e.Outcome = uid, rv, helmjournal.AcceptedPending
		r.objects[i] = response
		if i == 0 {
			r.row = helmrelease.Revision{Release: r.state.candidate.Release(), Number: 1, Status: "pending-install", StorageName: name, UID: uid, ResourceVersion: rv}
		}
		if err = r.journal.Response(i, helmjournal.AcceptedPending, uid, rv); err != nil {
			return errors.Join(ErrHelmExecutionJournal, err)
		}
		if err = r.observe(i); err != nil {
			return err
		}
		e.Outcome = helmjournal.Completed
		if err = r.journal.Observe(i, uid, rv); err != nil {
			return errors.Join(ErrHelmExecutionJournal, err)
		}
	}
	// Recheck all independently completed targets and the original namespace and
	// sole release revision before a conditional final patch. Reads do not lock
	// multiple objects; later external mutations remain possible.
	if err = r.guard(); err != nil {
		return err
	}
	for i := 1; i < len(r.objects); i++ {
		if err = r.observe(i); err != nil {
			return err
		}
	}
	if err = r.guard(); err != nil {
		return err
	}
	patch, err := helmrelease.BuildInstallDeployedPatch(r.ctx, &r.state.candidate, r.objects[0], r.row, time.Now())
	if err != nil {
		return r.readError(err)
	}
	defer clear(patch)
	last := len(r.out.effects) - 1
	response, rejected, err := r.mutate(last, patch)
	if err != nil {
		clear(response)
		return r.failure(last, rejected, err)
	}
	next, err := validateInstallDeployed(r.objects[0], patch, response, r.row)
	if err != nil {
		clear(response)
		return r.failure(last, false, ErrHelmInstallExecutionUnknown)
	}
	e := &r.out.effects[last]
	e.UID, e.ObservedResourceVersion, e.Outcome = next.UID, next.ResourceVersion, helmjournal.AcceptedPending
	if err = r.journal.Response(last, helmjournal.AcceptedPending, next.UID, next.ResourceVersion); err != nil {
		clear(response)
		return errors.Join(ErrHelmExecutionJournal, err)
	}
	clear(r.objects[0])
	r.objects[0] = response
	r.row = next
	if err = r.guard(); err != nil {
		return err
	}
	for i := 1; i < len(r.objects); i++ {
		if err = r.observe(i); err != nil {
			return err
		}
	}
	if err = r.guard(); err != nil {
		return err
	}
	e.Outcome = helmjournal.Completed
	if err = r.journal.Observe(last, next.UID, next.ResourceVersion); err != nil {
		return errors.Join(ErrHelmExecutionJournal, err)
	}
	return nil
}
func (r *installExecutionRun) failure(i int, rejected bool, err error) error {
	if r.out.effects[i].Outcome != helmjournal.Unknown {
		return err
	}
	if rejected {
		r.out.effects[i].Outcome = helmjournal.Rejected
	}
	// A failed response append leaves the durable intent Unknown. No retry.
	if e := r.journal.Response(i, r.out.effects[i].Outcome, "", ""); e != nil {
		return errors.Join(err, ErrHelmExecutionJournal, e)
	}
	return err
}
func (r *installExecutionRun) discovery() error {
	if err := r.active(); err != nil {
		return err
	}
	raw, _, err := r.b.helmLiveGET(r.ctx, []string{"/api", "v1"}, "application/json", maxLiveDiscoveryBytes, &r.remaining, false)
	if err != nil {
		return r.readError(err)
	}
	defer clear(raw)
	d, err := decodeLiveDiscovery(raw, "v1")
	if err != nil {
		return ErrHelmInstallExecutionResponse
	}
	for kind, resource := range map[string]string{"Secret": "secrets", "ConfigMap": "configmaps"} {
		route, e := resolveLiveRoute(d, kind)
		if e != nil || !route.namespaced || route.resource != resource {
			return helmrelease.ErrLiveRouting
		}
		n := 0
		for _, v := range d.APIResources {
			if v.Name == resource {
				n++
				if v.Kind != kind || !hasVerb(v.Verbs, "create") || resource == "secrets" && !hasVerb(v.Verbs, "patch") {
					return helmrelease.ErrLiveRouting
				}
			}
		}
		if n != 1 {
			return helmrelease.ErrLiveRouting
		}
	}
	return r.active()
}
func (r *installExecutionRun) namespace() error {
	if err := r.active(); err != nil {
		return err
	}
	row, err := r.b.installNamespace(r.ctx, r.state.candidate.Namespace(), &r.remaining)
	if err != nil {
		return r.readError(err)
	}
	if row.UID != r.state.preparation.namespaceUID || row.ResourceVersion != r.state.preparation.namespaceRV {
		return ErrHelmInstallExecutionChanged
	}
	return r.active()
}
func (r *installExecutionRun) history() error {
	if err := r.active(); err != nil {
		return err
	}
	s, err := r.b.helmReleaseMetadata(r.ctx, r.state.candidate.Namespace(), helmrelease.DecodeInstallMetadataPage)
	if err != nil {
		return r.readError(err)
	}
	n := 0
	for _, v := range s.Revisions {
		if v.Release == r.state.candidate.Release() || v.StorageName == r.state.candidate.StorageName() {
			n++
			if r.row.UID == "" || v != r.row {
				return ErrHelmInstallExecutionChanged
			}
		}
	}
	if (r.row.UID == "" && n != 0) || (r.row.UID != "" && n != 1) {
		return ErrHelmInstallExecutionChanged
	}
	return r.active()
}
func (r *installExecutionRun) guard() error {
	if err := r.namespace(); err != nil {
		return err
	}
	if err := r.history(); err != nil {
		return err
	}
	if r.row.UID != "" {
		if err := r.observe(0); err != nil {
			return err
		}
	}
	if err := r.history(); err != nil {
		return err
	}
	return r.namespace()
}
func (r *installExecutionRun) observe(i int) error {
	if i < 0 || i >= len(r.objects) || len(r.objects[i]) == 0 {
		return ErrHelmInstallExecutionResponse
	}
	resource, name := r.state.plan.Identity(i)
	data, err := r.readObject(resource, name)
	if err != nil {
		return err
	}
	defer clear(data)
	if !sameInstallObject(resource, data, r.objects[i]) {
		return ErrHelmInstallExecutionChanged
	}
	// Byte order and harmless JSON whitespace may differ, but typed identities,
	// complete data, labels, annotations and managed fields must remain exact.
	return r.active()
}
