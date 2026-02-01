package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExpiresAt(t *testing.T) {
	now := time.Unix(0, 0)
	got := ExpiresAt(now, 10)
	if !got.Equal(time.Unix(10, 0)) {
		t.Fatalf("unexpected expires: %v", got)
	}
}

func TestClient_StartDeviceAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		vals, _ := url.ParseQuery(string(b))
		if vals.Get("client_id") != "hytale-server" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"d","user_code":"u","verification_uri":"v","expires_in":900,"interval":5}`))
	}))
	defer srv.Close()

	c := Client{HTTP: srv.Client(), ClientID: "hytale-server", Scope: "openid", DeviceAuthURL: srv.URL, TokenURL: srv.URL}
	resp, err := c.StartDeviceAuth(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if resp.DeviceCode != "d" || resp.UserCode != "u" {
		t.Fatalf("unexpected resp")
	}
}

func TestClient_RefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"a","expires_in":3600}`))
	}))
	defer srv.Close()

	c := Client{HTTP: srv.Client(), ClientID: "hytale-server", TokenURL: srv.URL}
	resp, err := c.RefreshToken(context.Background(), "r")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if resp.AccessToken != "a" {
		t.Fatalf("access token mismatch")
	}
	if resp.RefreshToken != "r" {
		t.Fatalf("expected refresh token passthrough")
	}
}
