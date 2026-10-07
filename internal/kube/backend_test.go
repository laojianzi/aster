package kube

import (
	"testing"

	"k8s.io/client-go/rest"
)

func TestNewRejectsNilConfig(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewCopiesConfig(t *testing.T) {
	cfg := &rest.Config{Host: "https://127.0.0.1:6443", UserAgent: "test"}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Host = "https://changed.invalid"
	if got := b.Config().Host; got != "https://127.0.0.1:6443" {
		t.Fatalf("config mutated through caller: %q", got)
	}
}
