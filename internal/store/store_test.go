package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	s := NewFileStore(p)

	ctx := context.Background()

	st := State{Accounts: map[string]AccountState{"default": {RefreshToken: "r", AccessToken: "a", AccessTokenExpiresAt: time.Unix(123, 0)}}}
	if err := s.Save(ctx, st); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.Accounts["default"].RefreshToken != "r" {
		t.Fatalf("refresh_token mismatch")
	}
	if got.Accounts["default"].AccessToken != "a" {
		t.Fatalf("access_token mismatch")
	}
	if !got.Accounts["default"].AccessTokenExpiresAt.Equal(time.Unix(123, 0)) {
		t.Fatalf("expires mismatch")
	}
}

func TestFileStore_LoadMissing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "does-not-exist.json")
	s := NewFileStore(p)

	got, err := s.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Accounts == nil || len(got.Accounts) != 0 {
		t.Fatalf("expected empty state")
	}
}

func TestFileStore_SaveCreatesDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nested", "state.json")
	s := NewFileStore(p)

	if err := s.Save(context.Background(), State{Accounts: map[string]AccountState{}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := os.Stat(p); err != nil {
		t.Fatalf("stat: %v", err)
	}
}
