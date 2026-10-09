// Package testauth supplies an opt-in child process for tests. It is imported
// only by tests, and is never a credential provider shipped in the desktop.
package testauth

import (
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Run returns immediately in an ordinary test process. The caller test must
// select its own exact name when launching a child test binary.
func Run() {
	mode := os.Getenv("ASTER_CREDENTIAL_FIXTURE")
	if mode == "" {
		return
	}
	if path := os.Getenv("ASTER_FIXTURE_STARTED"); path != "" {
		if os.WriteFile(path, []byte("started"), 0600) != nil {
			os.Exit(91)
		}
	}
	switch mode {
	case "sleep":
		time.Sleep(60 * time.Second)
	case "stdout":
		_, _ = os.Stdout.Write([]byte(strings.Repeat("s", (256<<10)+1)))
		time.Sleep(time.Second)
	case "stderr":
		_, _ = os.Stderr.Write([]byte(strings.Repeat("s", (16<<10)+1)))
		time.Sleep(time.Second)
	case "failure":
		_, _ = os.Stderr.WriteString("DO-NOT-EXPOSE-CREDENTIAL-OR-ARGUMENT")
		os.Exit(17)
	case "descendant":
		time.Sleep(1800 * time.Millisecond)
		if os.WriteFile(os.Getenv("ASTER_FIXTURE_MARKER"), []byte("escaped"), 0600) != nil {
			os.Exit(91)
		}
	case "tree", "tree-wait":
		exe, err := os.Executable()
		if err != nil {
			os.Exit(91)
		}
		child := exec.Command(exe, "-test.run=^TestCredentialFixture$")
		child.Env = append(os.Environ(), "ASTER_CREDENTIAL_FIXTURE=descendant", "ASTER_FIXTURE_STARTED=")
		if child.Start() != nil {
			os.Exit(91)
		}
		if path := os.Getenv("ASTER_FIXTURE_SPAWNED"); path != "" {
			if os.WriteFile(path, []byte("spawned"), 0600) != nil {
				os.Exit(91)
			}
		}
		if mode == "tree-wait" {
			time.Sleep(60 * time.Second)
		}
		_, _ = os.Stdout.WriteString(os.Getenv("ASTER_FIXTURE_PAYLOAD"))
	case "inspect":
		if os.Getenv("ASTER_PARENT_SECRET") != "" || os.Getenv("NODE_OPTIONS") != "" {
			os.Exit(92)
		}
		var info struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Spec       struct {
				Interactive bool `json:"interactive"`
				Cluster     struct {
					Server string `json:"server"`
				} `json:"cluster"`
			} `json:"spec"`
		}
		if json.Unmarshal([]byte(os.Getenv("KUBERNETES_EXEC_INFO")), &info) != nil || info.Kind != "ExecCredential" || info.Spec.Interactive || info.Spec.Cluster.Server != "https://example.invalid:6443" {
			os.Exit(93)
		}
		if os.Getenv("ASTER_EXPLICIT_SETTING") != "chosen" || os.Args[len(os.Args)-1] != "; touch NOT-A-SHELL" {
			os.Exit(94)
		}
		_, _ = os.Stdout.WriteString(os.Getenv("ASTER_FIXTURE_PAYLOAD"))
	case "credential":
		if os.Getenv("ASTER_FIXTURE_DURATION") != "" {
			n, e := strconv.Atoi(os.Getenv("ASTER_FIXTURE_DURATION"))
			if e != nil {
				os.Exit(94)
			}
			version := os.Getenv("ASTER_FIXTURE_VERSION")
			if version == "" {
				version = "client.authentication.k8s.io/v1"
			}
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"apiVersion": version, "kind": "ExecCredential", "status": map[string]any{"token": os.Getenv("ASTER_FIXTURE_TOKEN"), "expirationTimestamp": time.Now().Add(time.Duration(n) * time.Second).UTC().Format(time.RFC3339)}})
		} else {
			_, _ = os.Stdout.WriteString(os.Getenv("ASTER_FIXTURE_PAYLOAD"))
		}
	default:
		os.Exit(95)
	}
	os.Exit(0)
}
