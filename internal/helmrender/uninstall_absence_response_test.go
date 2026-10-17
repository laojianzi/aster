package helmrender

import (
	"context"
	"fmt"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func TestHelmUninstallDeletionObservationRejectsAmbiguousReads(t *testing.T) {
	for _, mode := range []string{"typed-absence", "wrapped-absence", "raw-proxy-404", "synthetic-proxy-404", "other-name", "other-resource", "other-group", "nil-details", "wrong-code", "wrong-reason", "wrong-status", "cancel-after-read", "deadline-after-read", "expired-before-read"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "expired-before-read" {
				var stop context.CancelFunc
				ctx, stop = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer stop()
			}
			limit := time.Second
			if mode == "deadline-after-read" {
				limit = 10 * time.Millisecond
			}
			calls := 0
			err := observeHelmUninstallTargetAbsent(ctx, limit, "target", "fixture-uid", func(readCtx context.Context) (types.UID, error) {
				calls++
				if calls != 1 {
					t.Fatal("ambiguous response retried")
				}
				e := apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "target")
				switch mode {
				case "typed-absence":
				case "wrapped-absence":
					return "", fmt.Errorf("wrapped: %w", e)
				case "raw-proxy-404":
					return "", fmt.Errorf("404 proxy response")
				case "synthetic-proxy-404":
					return "", apierrors.NewGenericServerResponse(404, "get", schema.GroupResource{Resource: "configmaps"}, "target", "proxy response", 0, true)
				case "other-name":
					e.ErrStatus.Details.Name = "another"
				case "other-resource":
					e.ErrStatus.Details.Kind = "secrets"
				case "other-group":
					e.ErrStatus.Details.Group = "other"
				case "nil-details":
					e.ErrStatus.Details = nil
				case "wrong-code":
					e.ErrStatus.Code = 500
				case "wrong-reason":
					e.ErrStatus.Reason = metav1.StatusReasonForbidden
				case "wrong-status":
					e.ErrStatus.Status = metav1.StatusSuccess
				case "cancel-after-read":
					cancel()
				case "deadline-after-read":
					<-readCtx.Done()
				default:
					t.Fatal("expired observer performed a read")
				}
				return "", e
			})
			wantOK := mode == "typed-absence" || mode == "wrapped-absence"
			if (err == nil) != wantOK {
				t.Fatalf("success=%v expected=%v", err == nil, wantOK)
			}
			wantCalls := 1
			if mode == "expired-before-read" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("reads=%d expected=%d", calls, wantCalls)
			}
		})
	}
}
