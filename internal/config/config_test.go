package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_DefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("http:\n  addr: ':1234'\nstore:\n  path: 'x.json'\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HTTP.Addr != ":1234" {
		t.Fatalf("addr mismatch: %s", cfg.HTTP.Addr)
	}
	if cfg.Store.Path != "x.json" {
		t.Fatalf("store path mismatch")
	}
	if cfg.OAuth.ClientID == "" {
		t.Fatalf("expected oauth defaults")
	}
}

func TestLoad_ReservedAccountNameAny_IsRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("store:\n  path: 'x.json'\naccounts:\n  any:\n    profile_uuids: ['p1']\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Load(p)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_DefaultsAccountAny_IsRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("store:\n  path: 'x.json'\ndefaults:\n  account: any\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Load(p)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "defaults.account") {
		t.Fatalf("unexpected error: %v", err)
	}
}
