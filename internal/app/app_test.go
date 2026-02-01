package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain_AuthStatus_NoState(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	statePath := filepath.Join(dir, "state.json")

	cfg := "http:\n  addr: ':0'\nstore:\n  path: '" + statePath + "'\ndefaults:\n  account: 'default'\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Main(context.Background(), []string{"-config", cfgPath, "auth-status"}, &stdout, &stderr, Dependencies{Now: func() time.Time { return time.Unix(0, 0) }})
	if code != 0 {
		t.Fatalf("expected 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "not authenticated") {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}
}

func TestMain_UnknownCommand(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	statePath := filepath.Join(dir, "state.json")

	cfg := "store:\n  path: '" + statePath + "'\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Main(context.Background(), []string{"-config", cfgPath, "nope"}, &stdout, &stderr, Dependencies{})
	if code == 0 {
		t.Fatalf("expected non-zero")
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}
