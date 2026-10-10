package credentialexec

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAppHelperFixture(t *testing.T) {
	mode := os.Getenv("ASTER_APP_HELPER_TEST")
	if mode == "" {
		return
	}
	switch mode {
	case "pipe":
		data, _ := io.ReadAll(os.Stdin)
		if string(data) != "sensitive-pipe-only" {
			os.Exit(90)
		}
		if strings.Contains(strings.Join(os.Args, " "), "sensitive-pipe-only") {
			os.Exit(91)
		}
		_, _ = os.Stdout.Write([]byte("pipe-ok"))
	case "overflow":
		_, _ = os.Stdout.Write([]byte(strings.Repeat("a", 5000)))
	case "sleep":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}
func TestApplicationHelperPipeBudgetsAndCancellation(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	env := []string{"ASTER_APP_HELPER_TEST=pipe"}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	out, e := RunHelper(context.Background(), exe, []string{"-test.run=^TestAppHelperFixture$"}, env, []byte("sensitive-pipe-only"), 5*time.Second)
	if e != nil || string(out) != "pipe-ok" {
		t.Fatal("helper pipe failed", e)
	}
	for _, mode := range []string{"sleep", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			vars := append([]string(nil), env...)
			vars[0] = "ASTER_APP_HELPER_TEST=" + mode
			_, e := RunHelper(context.Background(), exe, []string{"-test.run=^TestAppHelperFixture$"}, vars, nil, 200*time.Millisecond)
			if e == nil {
				t.Fatal("unbounded application helper")
			}
		})
	}
}
