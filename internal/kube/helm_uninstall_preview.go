package kube

import (
	"context"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
)

// PreviewHelmUninstall is an explicitly consented read, never a live deletion
// forecast or write. Shared transport keeps redirects, retries and error bodies
// out of both content paths. Expiry uses real time, not the toolkit test clock.
func (b *Backend) PreviewHelmUninstall(ctx context.Context, ns string, row helmrelease.Revision, session string, acknowledged bool) (*helmrelease.UninstallPreview, error) {
	if !helmrelease.ValidPreviewSession(session) {
		return nil, helmrelease.ErrPreviewSession
	}
	var preview *helmrelease.UninstallPreview
	err := b.readHelmRevision(ctx, ns, row, acknowledged, func(ctx context.Context, data []byte) error {
		var err error
		preview, err = helmrelease.PreviewUninstallSecret(ctx, data, ns, row, session, time.Now())
		return err
	})
	if err != nil {
		return nil, err
	}
	return preview, nil
}
