package hytale

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_GetProfiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"owner":"o","profiles":[{"uuid":"p","username":"u"}]}`))
	}))
	defer srv.Close()

	c := Client{HTTP: srv.Client(), AccountDataBaseURL: srv.URL}
	resp, err := c.GetProfiles(context.Background(), "a")
	if err != nil {
		t.Fatalf("get profiles: %v", err)
	}
	if len(resp.Profiles) != 1 || resp.Profiles[0].UUID != "p" {
		t.Fatalf("unexpected profiles")
	}
}

func TestClient_CreateGameSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessionToken":"s","identityToken":"i","expiresAt":"1970-01-01T00:00:01Z"}`))
	}))
	defer srv.Close()

	c := Client{HTTP: srv.Client(), SessionsBaseURL: srv.URL}
	resp, err := c.CreateGameSession(context.Background(), "a", "p")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if resp.SessionToken != "s" || resp.IdentityToken != "i" {
		t.Fatalf("unexpected tokens")
	}
}
