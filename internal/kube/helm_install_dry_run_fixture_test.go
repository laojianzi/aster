package kube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/laojianzi/aster/internal/helmrelease"
)

// Cancel deterministically interrupts this reader, rather than depending on a
// socket race or scheduler timing. EOF proves the handler consumed all input.
type installCancelReader struct {
	ctx    context.Context
	reader *strings.Reader
	eof    bool
}

func (r *installCancelReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > 7 {
		p = p[:7]
	}
	n, err := r.reader.Read(p)
	if err == io.EOF {
		r.eof = true
	}
	return n, err
}

func TestHelmInstallDryRunFixtureConsumesBodyBeforeHook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &installCancelReader{ctx: ctx, reader: strings.NewReader(`{"metadata":{"name":"example","namespace":"team"}}`)}
	req := httptest.NewRequest(http.MethodPost, "/prefix/api/v1/namespaces/team/secrets?dryRun=All&fieldValidation=Strict&fieldManager="+helmrelease.InstallFieldManager, body)
	req.Header.Set("Authorization", "Bearer current")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	f := &installDryFixture{}
	hooks := 0
	f.onPost = func(n int) {
		hooks++
		if n != 1 || !body.eof || body.reader.Len() != 0 || f.bad.Load() {
			t.Error("cancellation hook ran before the complete request was checked")
		}
		cancel()
	}
	response := httptest.NewRecorder()
	f.handler(response, req)
	if hooks != 1 || f.posts.Load() != 1 || f.bad.Load() || response.Code != http.StatusCreated {
		t.Fatal("fixture failed complete validation before cancellation")
	}
}

func TestHelmInstallDryRunCancellationOneShot(t *testing.T) {
	for _, mode := range []string{"cancel", "revoke"} {
		t.Run(mode, func(t *testing.T) {
			b, c, p, f := installDrySetup(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			grant, err := b.AcknowledgeHelmInstallDryRun(ctx, p, c, "dry-session", true)
			if err != nil {
				t.Fatal(err)
			}
			f.onPost = func(n int) {
				if n == 1 {
					if mode == "cancel" {
						cancel()
					} else {
						grant.Discard()
					}
				}
			}
			report, err := b.PreflightHelmInstall(ctx, grant, c, "dry-session")
			if err == nil || report != nil && report.Complete() || f.posts.Load() != 1 || f.bad.Load() {
				t.Fatal("cancelled or revoked request advanced, retried or violated policy")
			}
			before := f.calls.Load()
			if _, err := b.PreflightHelmInstall(context.Background(), grant, c, "dry-session"); !errors.Is(err, ErrHelmDryRunConsent) || f.calls.Load() != before {
				t.Fatal("cancelled consent was reused")
			}
		})
	}
}
