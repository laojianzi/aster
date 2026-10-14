package credentialexec

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRenderHelperFixture(t *testing.T) {
	mode := os.Getenv("ASTER_RENDER_HELPER_TEST")
	if mode == "" {
		return
	}
	switch mode {
	case "stdin":
		b, _ := io.ReadAll(os.Stdin)
		if len(b) != 256<<10 {
			os.Exit(90)
		}
		os.Stdout.Write([]byte("ok"))
	case "stdout":
		os.Stdout.Write([]byte(strings.Repeat("x", (2<<20)+1)))
	case "stderr":
		os.Stderr.Write([]byte(strings.Repeat("PRIVATE", (16<<10)/7+1)))
	case "sleep":
		time.Sleep(time.Minute)
	case "failure":
		os.Stderr.Write([]byte("PRIVATE_VALUE"))
		os.Exit(4)
	}
	os.Exit(0)
}
func TestRenderHelperPipeLimitsCancellationAndPrivacy(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"stdin", "stdout", "stderr", "sleep", "failure"} {
		t.Run(mode, func(t *testing.T) {
			env := []string{"ASTER_RENDER_HELPER_TEST=" + mode}
			for _, k := range []string{"SystemRoot", "WINDIR"} {
				if v := os.Getenv(k); v != "" {
					env = append(env, k+"="+v)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode == "sleep" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
			}
			start := time.Now()
			out, err := RunRenderHelper(ctx, executable, []string{"-test.run=^TestRenderHelperFixture$"}, env, t.TempDir(), []byte(strings.Repeat("x", 256<<10)))
			if mode == "stdin" {
				if err != nil || string(out) != "ok" {
					t.Fatal(err)
				}
				return
			}
			if err == nil || out != nil || strings.Contains(err.Error(), "PRIVATE") || time.Since(start) > 7*time.Second {
				t.Fatal("helper boundary failure", err)
			}
			if (mode == "stdout" || mode == "stderr") && !errors.Is(err, ErrOutputLimit) {
				t.Fatal("overflow not recognized", err)
			}
		})
	}
	if _, err := RunRenderHelper(nil, executable, nil, nil, t.TempDir(), nil); err == nil {
		t.Fatal("nil context accepted")
	}
}
