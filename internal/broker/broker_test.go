package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hybrowse/hytale-session-token-broker/internal/config"
	"github.com/hybrowse/hytale-session-token-broker/internal/store"
)

type memStore struct {
	st store.State
}

func TestBroker_MintGameSession_ConcurrentCalls_OnlyRefreshOnce(t *testing.T) {
	now := time.Unix(100, 0)

	var refreshCalls int32
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","expires_in":3600,"refresh_token":"new-refresh"}`))
	}))
	defer oauthSrv.Close()

	sessionsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessionToken":"s","identityToken":"i","expiresAt":"1970-01-01T00:30:00Z"}`))
	}))
	defer sessionsSrv.Close()

	cfg := config.Config{}
	cfg.HTTP.Addr = ":0"
	cfg.Defaults.Account = "default"
	cfg.Store.Path = "ignored"
	cfg.OAuth.ClientID = "hytale-server"
	cfg.OAuth.Scope = "openid offline auth:server"
	cfg.OAuth.DeviceAuthURL = oauthSrv.URL
	cfg.OAuth.TokenURL = oauthSrv.URL
	cfg.Hytale.AccountDataBaseURL = "http://invalid"
	cfg.Hytale.SessionsBaseURL = sessionsSrv.URL
	cfg.Accounts = map[string]config.AccountConfig{
		"default": {ProfileUUIDs: []string{"p1"}},
	}

	ms := &memStore{st: store.State{Accounts: map[string]store.AccountState{"default": {RefreshToken: "r", AccessToken: "old-access", AccessTokenExpiresAt: now.Add(-time.Minute)}}}}

	b := New(Dependencies{Config: cfg, Store: ms, Now: func() time.Time { return now }})

	start := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := b.MintGameSession(context.Background(), "default", nil)
		errCh <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := b.MintGameSession(context.Background(), "default", nil)
		errCh <- err
	}()

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
	}

	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("expected exactly 1 refresh call, got %d", got)
	}
	if ms.st.Accounts["default"].RefreshToken != "new-refresh" {
		t.Fatalf("expected refresh token to be updated")
	}
	if ms.st.Accounts["default"].AccessToken != "new-access" {
		t.Fatalf("expected access token to be updated")
	}
}

func TestBroker_MintGameSession_InvalidGrant_ClearsTokensAndReturnsReauthRequired(t *testing.T) {
	now := time.Unix(100, 0)

	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer oauthSrv.Close()

	cfg := config.Config{}
	cfg.HTTP.Addr = ":0"
	cfg.Defaults.Account = "default"
	cfg.Store.Path = "ignored"
	cfg.OAuth.ClientID = "hytale-server"
	cfg.OAuth.Scope = "openid offline auth:server"
	cfg.OAuth.DeviceAuthURL = oauthSrv.URL
	cfg.OAuth.TokenURL = oauthSrv.URL
	cfg.Hytale.AccountDataBaseURL = "http://invalid"
	cfg.Hytale.SessionsBaseURL = "http://invalid"

	ms := &memStore{st: store.State{Accounts: map[string]store.AccountState{"default": {RefreshToken: "r", AccessToken: "a", AccessTokenExpiresAt: now.Add(-time.Minute)}}}}

	b := New(Dependencies{Config: cfg, Store: ms, Now: func() time.Time { return now }})

	_, err := b.MintGameSession(context.Background(), "default", nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	var ra ReauthRequiredError
	if !errors.As(err, &ra) {
		t.Fatalf("expected ReauthRequiredError, got %T: %v", err, err)
	}
	if ms.st.Accounts["default"].RefreshToken != "" {
		t.Fatalf("expected refresh token to be cleared")
	}
	if ms.st.Accounts["default"].AccessToken != "" {
		t.Fatalf("expected access token to be cleared")
	}
}

func TestBroker_HTTP_CreateGameSession_InvalidGrant_IsUnauthorized(t *testing.T) {
	now := time.Unix(100, 0)

	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer oauthSrv.Close()

	cfg := config.Config{}
	cfg.HTTP.Addr = ":0"
	cfg.Defaults.Account = "default"
	cfg.Store.Path = "ignored"
	cfg.OAuth.ClientID = "hytale-server"
	cfg.OAuth.Scope = "openid offline auth:server"
	cfg.OAuth.DeviceAuthURL = oauthSrv.URL
	cfg.OAuth.TokenURL = oauthSrv.URL
	cfg.Hytale.AccountDataBaseURL = "http://invalid"
	cfg.Hytale.SessionsBaseURL = "http://invalid"

	ms := &memStore{st: store.State{Accounts: map[string]store.AccountState{"default": {RefreshToken: "r", AccessToken: "a", AccessTokenExpiresAt: now.Add(-time.Minute)}}}}

	b := New(Dependencies{Config: cfg, Store: ms, Now: func() time.Time { return now }})

	reqBody, _ := json.Marshal(map[string]any{"account": "default"})
	req := httptest.NewRequest(http.MethodPost, "/v1/game-session", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	h := b.authMiddleware(b.handleCreateGameSession)
	h(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func (m *memStore) Load(ctx context.Context) (store.State, error) {
	_ = ctx
	if m.st.Accounts == nil {
		m.st.Accounts = map[string]store.AccountState{}
	}
	return m.st, nil
}

func (m *memStore) Save(ctx context.Context, st store.State) error {
	_ = ctx
	m.st = st
	return nil
}

func TestBroker_MintGameSession_RefreshesAccessTokenAndMintsSession(t *testing.T) {
	now := time.Unix(100, 0)

	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","expires_in":3600,"refresh_token":"new-refresh"}`))
	}))
	defer oauthSrv.Close()

	accountSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"owner":"o","profiles":[{"uuid":"p1","username":"u1"}]}`))
	}))
	defer accountSrv.Close()

	sessionsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessionToken":"s","identityToken":"i","expiresAt":"1970-01-01T00:30:00Z"}`))
	}))
	defer sessionsSrv.Close()

	cfg := config.Config{}
	cfg.HTTP.Addr = ":0"
	cfg.Defaults.Account = "default"
	cfg.Store.Path = "ignored"
	cfg.OAuth.ClientID = "hytale-server"
	cfg.OAuth.Scope = "openid offline auth:server"
	cfg.OAuth.DeviceAuthURL = oauthSrv.URL
	cfg.OAuth.TokenURL = oauthSrv.URL
	cfg.Hytale.AccountDataBaseURL = accountSrv.URL
	cfg.Hytale.SessionsBaseURL = sessionsSrv.URL

	ms := &memStore{st: store.State{Accounts: map[string]store.AccountState{"default": {RefreshToken: "r", AccessTokenExpiresAt: now.Add(-time.Minute)}}}}

	b := New(Dependencies{Config: cfg, Store: ms, Now: func() time.Time { return now }})

	resp, err := b.MintGameSession(context.Background(), "default", nil)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if resp.SessionToken != "s" || resp.IdentityToken != "i" {
		t.Fatalf("tokens mismatch")
	}
	if ms.st.Accounts["default"].AccessToken != "new-access" {
		t.Fatalf("expected refreshed access token")
	}
	if ms.st.Accounts["default"].RefreshToken != "new-refresh" {
		t.Fatalf("expected refresh token update")
	}
}

func TestBroker_HTTP_CreateGameSession_RequiresBearerToken(t *testing.T) {
	cfg := config.Config{}
	cfg.HTTP.Addr = ":0"
	cfg.HTTP.BearerToken = "secret"
	cfg.Defaults.Account = "default"
	cfg.Store.Path = "ignored"
	cfg.OAuth.ClientID = "hytale-server"
	cfg.OAuth.Scope = "openid offline auth:server"
	cfg.OAuth.DeviceAuthURL = "http://invalid"
	cfg.OAuth.TokenURL = "http://invalid"
	cfg.Hytale.AccountDataBaseURL = "http://invalid"
	cfg.Hytale.SessionsBaseURL = "http://invalid"

	b := New(Dependencies{Config: cfg, Store: &memStore{}, Now: time.Now})

	reqBody, _ := json.Marshal(map[string]any{"account": "default"})
	req := httptest.NewRequest(http.MethodPost, "/v1/game-session", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	h := b.authMiddleware(b.handleCreateGameSession)
	h(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestBroker_MintGameSession_ProfileFallback(t *testing.T) {
	now := time.Unix(100, 0)

	called := []string{}
	sessionsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req map[string]any
		_ = json.Unmarshal(b, &req)
		uuid, _ := req["uuid"].(string)
		called = append(called, uuid)
		if uuid == "p1" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("fail"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessionToken":"s","identityToken":"i","expiresAt":"1970-01-01T00:30:00Z"}`))
	}))
	defer sessionsSrv.Close()

	cfg := config.Config{}
	cfg.HTTP.Addr = ":0"
	cfg.Defaults.Account = "default"
	cfg.Store.Path = "ignored"
	cfg.OAuth.ClientID = "hytale-server"
	cfg.OAuth.Scope = "openid offline auth:server"
	cfg.OAuth.DeviceAuthURL = "http://invalid"
	cfg.OAuth.TokenURL = "http://invalid"
	cfg.Hytale.AccountDataBaseURL = "http://invalid"
	cfg.Hytale.SessionsBaseURL = sessionsSrv.URL
	cfg.Accounts = map[string]config.AccountConfig{
		"default": {ProfileUUIDs: []string{"p1", "p2"}},
	}

	ms := &memStore{st: store.State{Accounts: map[string]store.AccountState{"default": {RefreshToken: "r", AccessToken: "a", AccessTokenExpiresAt: now.Add(time.Hour)}}}}

	b := New(Dependencies{Config: cfg, Store: ms, Now: func() time.Time { return now }})

	resp, err := b.MintGameSession(context.Background(), "default", nil)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if resp.SessionToken != "s" {
		t.Fatalf("expected success")
	}
	if len(called) != 2 || called[0] != "p1" || called[1] != "p2" {
		t.Fatalf("unexpected call order: %+v", called)
	}
}

func TestBroker_MintGameSession_RoundRobinAcrossCalls(t *testing.T) {
	now := time.Unix(100, 0)

	called := []string{}
	sessionsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req map[string]any
		_ = json.Unmarshal(b, &req)
		uuid, _ := req["uuid"].(string)
		called = append(called, uuid)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessionToken":"s","identityToken":"i","expiresAt":"1970-01-01T00:30:00Z"}`))
	}))
	defer sessionsSrv.Close()

	cfg := config.Config{}
	cfg.HTTP.Addr = ":0"
	cfg.Defaults.Account = "default"
	cfg.Store.Path = "ignored"
	cfg.OAuth.ClientID = "hytale-server"
	cfg.OAuth.Scope = "openid offline auth:server"
	cfg.OAuth.DeviceAuthURL = "http://invalid"
	cfg.OAuth.TokenURL = "http://invalid"
	cfg.Hytale.AccountDataBaseURL = "http://invalid"
	cfg.Hytale.SessionsBaseURL = sessionsSrv.URL
	cfg.Accounts = map[string]config.AccountConfig{
		"default": {ProfileUUIDs: []string{"p1", "p2"}},
	}

	ms := &memStore{st: store.State{Accounts: map[string]store.AccountState{"default": {RefreshToken: "r", AccessToken: "a", AccessTokenExpiresAt: now.Add(time.Hour)}}}}

	b := New(Dependencies{Config: cfg, Store: ms, Now: func() time.Time { return now }})

	_, err := b.MintGameSession(context.Background(), "default", nil)
	if err != nil {
		t.Fatalf("mint 1: %v", err)
	}
	_, err = b.MintGameSession(context.Background(), "default", nil)
	if err != nil {
		t.Fatalf("mint 2: %v", err)
	}

	if len(called) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(called))
	}
	if called[0] != "p1" || called[1] != "p2" {
		t.Fatalf("expected round-robin p1 then p2, got %+v", called)
	}
}

func TestBroker_MintGameSession_AnyAccountSelection_RoundRobin(t *testing.T) {
	now := time.Unix(100, 0)

	called := []string{}
	sessionsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req map[string]any
		_ = json.Unmarshal(b, &req)
		uuid, _ := req["uuid"].(string)
		called = append(called, uuid)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessionToken":"s","identityToken":"i","expiresAt":"1970-01-01T00:30:00Z"}`))
	}))
	defer sessionsSrv.Close()

	cfg := config.Config{}
	cfg.HTTP.Addr = ":0"
	cfg.Store.Path = "ignored"
	cfg.OAuth.ClientID = "hytale-server"
	cfg.OAuth.Scope = "openid offline auth:server"
	cfg.OAuth.DeviceAuthURL = "http://invalid"
	cfg.OAuth.TokenURL = "http://invalid"
	cfg.Hytale.AccountDataBaseURL = "http://invalid"
	cfg.Hytale.SessionsBaseURL = sessionsSrv.URL
	cfg.Accounts = map[string]config.AccountConfig{
		"a": {ProfileUUIDs: []string{"p1"}},
		"b": {ProfileUUIDs: []string{"p2"}},
	}

	ms := &memStore{st: store.State{Accounts: map[string]store.AccountState{
		"a": {RefreshToken: "ra", AccessToken: "aa", AccessTokenExpiresAt: now.Add(time.Hour)},
		"b": {RefreshToken: "rb", AccessToken: "ab", AccessTokenExpiresAt: now.Add(time.Hour)},
	}}}

	b := New(Dependencies{Config: cfg, Store: ms, Now: func() time.Time { return now }})

	_, err := b.MintGameSession(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("mint 1: %v", err)
	}
	_, err = b.MintGameSession(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("mint 2: %v", err)
	}

	if len(called) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(called))
	}
	if called[0] != "p1" || called[1] != "p2" {
		t.Fatalf("expected any-account rr p1 then p2, got %+v", called)
	}
}
