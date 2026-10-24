package kube

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
)

// HelmInstallPreparation contains only sampled identities and a private binding;
// it is not operation.Prepared, a dry-run, write consent or a transaction lock.
type HelmInstallPreparation struct {
	owner                                  *backendIdentity
	candidate                              *helmrelease.InstallCandidate
	id, session, namespaceUID, namespaceRV string
	started, finished, expires             time.Time
}

func (p *HelmInstallPreparation) ID() string {
	if p == nil {
		return ""
	}
	return p.id
}
func (p *HelmInstallPreparation) ExpiresAt() time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.expires
}
func (p *HelmInstallPreparation) NamespaceIdentity() (string, string) {
	if p == nil {
		return "", ""
	}
	return p.namespaceUID, p.namespaceRV
}
func (p *HelmInstallPreparation) Matches(b *Backend, c *helmrelease.InstallCandidate, session string, now time.Time) bool {
	return p != nil && b != nil && b.identity != nil && p.owner == b.identity && p.candidate == c &&
		p.session == session && c.Valid(now) && !now.Before(p.finished) && now.Before(p.expires)
}
func (p HelmInstallPreparation) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, "Inert Helm install preparation (no write authority)")
}
func (p HelmInstallPreparation) MarshalJSON() ([]byte, error) {
	return []byte(`{"kind":"inert-helm-install-preparation"}`), nil
}

// PrepareHelmInstall performs GETs only, using the current bounded transport.
// Complete metadata histories bracket exact-name reads; both histories must
// exclude ALL revisions of this release, including failed/uninstalled ones.
// An additional exact reserved-Secret GET detects unlabeled collisions.
// These observations cannot exclude a race after the last GET. Future create
// requests must fail on AlreadyExists, never update/adopt, and must be journaled.
func (b *Backend) PrepareHelmInstall(parent context.Context, c *helmrelease.InstallCandidate, session string, acknowledged bool) (*HelmInstallPreparation, error) {
	if !acknowledged {
		return nil, helmrelease.ErrInstallConsent
	}
	if parent == nil || b == nil || b.identity == nil || !helmrelease.ValidPreviewSession(session) {
		return nil, helmrelease.ErrPreviewSession
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	started := time.Now()
	if !c.Valid(started) {
		return nil, helmrelease.ErrInstallStale
	}
	expires := started.Add(helmrelease.PreparationLifetime)
	if c.ExpiresAt().Before(expires) {
		expires = c.ExpiresAt()
	}
	ctx, cancel := context.WithDeadline(parent, expires)
	defer cancel()
	ctx, readCancel := context.WithTimeout(ctx, helmrelease.ReadTimeout)
	defer readCancel()
	remaining := maxLiveReadBytes
	data, _, err := b.helmLiveGET(ctx, []string{"/api", "v1"}, "application/json", maxLiveDiscoveryBytes, &remaining, false)
	if err != nil {
		return nil, err
	}
	discovery, err := decodeLiveDiscovery(data, "v1")
	clear(data)
	if err != nil {
		return nil, err
	}
	for kind, want := range map[string]liveRoute{
		"ConfigMap": {resource: "configmaps", namespaced: true},
		"Secret":    {resource: "secrets", namespaced: true},
		"Namespace": {resource: "namespaces", namespaced: false},
	} {
		route, err := resolveLiveRoute(discovery, kind)
		if err != nil || route != want {
			return nil, helmrelease.ErrLiveRouting
		}
		// History requires list as well as get. GVK-only resolution also
		// cannot accept a second kind claiming the same collection path.
		matches := 0
		for _, advertised := range discovery.APIResources {
			if advertised.Name != want.resource {
				continue
			}
			matches++
			if advertised.Kind != kind || kind == "Secret" && !hasVerb(advertised.Verbs, "list") {
				return nil, helmrelease.ErrLiveRouting
			}
		}
		if matches != 1 {
			return nil, helmrelease.ErrLiveRouting
		}
	}
	nsBefore, err := b.installNamespace(ctx, c.Namespace(), &remaining)
	if err != nil {
		return nil, err
	}
	before, err := b.helmReleaseMetadata(ctx, c.Namespace(), helmrelease.DecodeInstallMetadataPage)
	if err != nil {
		return nil, err
	}
	if err := installHistoryClear(before, c); err != nil {
		return nil, err
	}
	if err := b.installAbsent(ctx, c, "Secret", "secrets", c.StorageName(), &remaining); err != nil {
		return nil, err
	}
	for _, target := range c.Targets() {
		if err := b.installAbsent(ctx, c, "ConfigMap", "configmaps", target.Name, &remaining); err != nil {
			return nil, err
		}
	}
	after, err := b.helmReleaseMetadata(ctx, c.Namespace(), helmrelease.DecodeInstallMetadataPage)
	if err != nil {
		return nil, err
	}
	if err := installHistoryClear(after, c); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before.Revisions, after.Revisions) {
		return nil, helmrelease.ErrInstallStale
	}
	if err := b.installAbsent(ctx, c, "Secret", "secrets", c.StorageName(), &remaining); err != nil {
		return nil, err
	}
	nsAfter, err := b.installNamespace(ctx, c.Namespace(), &remaining)
	if err != nil {
		return nil, err
	}
	if nsBefore.UID != nsAfter.UID || nsBefore.ResourceVersion != nsAfter.ResourceVersion {
		return nil, helmrelease.ErrInstallStale
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, helmrelease.ErrLiveUnavailable
	}
	finished := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !c.Valid(finished) || !finished.Before(expires) {
		return nil, helmrelease.ErrInstallStale
	}
	return &HelmInstallPreparation{owner: b.identity, candidate: c, id: hex.EncodeToString(id[:]), session: session,
		namespaceUID: nsAfter.UID, namespaceRV: nsAfter.ResourceVersion, started: started, finished: finished, expires: expires}, nil
}
func installHistoryClear(s helmrelease.Snapshot, c *helmrelease.InstallCandidate) error {
	if s.Namespace != c.Namespace() || s.ResourceVersion == "" || s.ObservedAt.IsZero() {
		return helmrelease.ErrInvalid
	}
	for _, row := range s.Revisions {
		if row.Release == c.Release() || row.StorageName == c.StorageName() {
			return helmrelease.ErrInstallConflict
		}
	}
	return nil
}
func (b *Backend) installNamespace(ctx context.Context, ns string, remaining *int) (helmrelease.LiveItem, error) {
	item := helmrelease.UninstallItem{APIVersion: "v1", Kind: "Namespace", Name: ns}
	data, status, err := b.helmLiveGET(ctx, []string{"/api", "v1", "namespaces", ns}, liveMetadataAccept, maxLiveObjectBytes, remaining, false)
	if err != nil {
		return helmrelease.LiveItem{}, err
	}
	defer clear(data)
	row, err := projectLiveMetadata(data, status, item, "namespaces", "", "", "")
	if err != nil {
		return helmrelease.LiveItem{}, err
	}
	if row.State != "present" || row.Terminating {
		return helmrelease.LiveItem{}, helmrelease.ErrInstallConflict
	}
	return row, nil
}
func (b *Backend) installAbsent(ctx context.Context, c *helmrelease.InstallCandidate, kind, resource, name string, remaining *int) error {
	data, status, err := b.helmLiveGET(ctx, []string{"/api", "v1", "namespaces", c.Namespace(), resource, name},
		liveMetadataAccept, maxLiveObjectBytes, remaining, true)
	if err != nil {
		return err
	}
	defer clear(data)
	item := helmrelease.UninstallItem{APIVersion: "v1", Kind: kind, Namespace: c.Namespace(), Name: name}
	row, err := projectLiveMetadata(data, status, item, resource, c.Namespace(), c.Release(), c.Namespace())
	if err != nil {
		return err
	}
	if row.State != "absent" {
		return helmrelease.ErrInstallConflict
	}
	return ctx.Err()
}
