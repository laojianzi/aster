package helmrelease

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"testing"
	"time"
)

func installExecutionLargeCandidate(t *testing.T) *InstallCandidate {
	t.Helper()
	// Deterministic synthetic high-entropy values stay inside all capture and
	// create limits, while the eventual patch must carry both release values.
	random := rand.New(rand.NewPCG(29, 64))
	noise := make([]byte, 640<<10)
	for i := range noise {
		noise[i] = byte(random.Uint32())
	}
	v := installTestRelease()
	v["config"] = map[string]any{"synthetic": base64.StdEncoding.EncodeToString(noise)}
	clear(noise)
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	c, err := DecodeInstallCapture(context.Background(), raw, "team", "example", installTestNow())
	if err != nil {
		t.Fatal("valid bounded capture rejected", err)
	}
	return c
}

func TestHelmInstallExecutionSizeBounds(t *testing.T) {
	for _, mode := range []string{"small", "oversized-patch", "invalid", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			c := installTestCandidate(t)
			if mode == "oversized-patch" {
				c = installExecutionLargeCandidate(t)
			}
			ctx := context.Background()
			p, err := BuildInstallCreatePlan(ctx, c, installTestNow())
			if err != nil {
				t.Fatal("bounded create plan rejected", err)
			}
			if mode == "invalid" {
				other := installTestCandidate(t)
				for _, v := range []struct {
					ctx context.Context
					c   *InstallCandidate
					p   *InstallCreatePlan
				}{{nil, c, p}, {ctx, nil, p}, {ctx, c, nil}, {ctx, &InstallCandidate{}, p},
					{ctx, c, &InstallCreatePlan{}}, {ctx, other, p}} {
					pending, patch, e := InstallExecutionSizeBounds(v.ctx, v.c, v.p)
					if !errors.Is(e, ErrInstallCodec) || pending != 0 || patch != 0 {
						t.Fatal("unbound plan produced a size fact", e)
					}
				}
				return
			}
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				pending, patch, e := InstallExecutionSizeBounds(ctx, c, p)
				if !errors.Is(e, context.Canceled) || pending != 0 || patch != 0 {
					t.Fatal("cancelled size computation continued", e)
				}
				return
			}
			pendingBytes, patchDataBytes, err := InstallExecutionSizeBounds(ctx, c, p)
			if err != nil || pendingBytes == 0 || patchDataBytes <= pendingBytes {
				t.Fatal("missing bounded size facts", err)
			}
			// Both sizes admit capture, create and complete preflight-shaped
			// acknowledgements. The old executor discovered the large patch only
			// after its persistent creates; admission can now reject it earlier.
			for i := 0; i < p.Len(); i++ {
				response, e := json.Marshal(installCreateResponse(t, p, i))
				if e != nil {
					t.Fatal(e)
				}
				e = ValidateInstallCreateResponse(ctx, p, i, response)
				clear(response)
				if e != nil {
					t.Fatal("bounded server preflight shape rejected", e)
				}
			}
			wire, row := installTestWire(t, c)
			defer clear(wire)
			patch, err := BuildInstallDeployedPatch(ctx, c, wire, row, installTestNow().Add(time.Second))
			defer clear(patch)
			if mode == "oversized-patch" {
				if pendingBytes >= MaxSecretBytes || patchDataBytes <= MaxSecretBytes || !errors.Is(err, ErrInstallCodec) || patch != nil {
					t.Fatal("large legal create did not prove an impossible final patch", err)
				}
				t.Logf("legal create=%d bytes; final patch data alone=%d bytes exceeds unchanged %d-byte limit", len(wire), patchDataBytes, MaxSecretBytes)
				return
			}
			if err != nil || patchDataBytes > MaxSecretBytes {
				t.Fatal("small final patch rejected", err)
			}
			var actual struct {
				Data map[string]string `json:"data"`
			}
			if json.Unmarshal(wire, &actual) != nil || len(actual.Data["release"]) != pendingBytes {
				t.Fatal("pending data bound differs from actual wire value")
			}
			var operations []struct {
				Path  string `json:"path"`
				Value any    `json:"value"`
			}
			if json.Unmarshal(patch, &operations) != nil {
				t.Fatal("invalid actual patch")
			}
			count, size := 0, 0
			for _, op := range operations {
				if op.Path == "/data/release" {
					value, ok := op.Value.(string)
					if !ok {
						t.Fatal("release data is not a string")
					}
					count++
					size += len(value)
				}
			}
			if count != 2 || size != patchDataBytes || size >= len(patch) {
				t.Fatal("lower bound does not match the actual conditional codec")
			}
		})
	}
}
