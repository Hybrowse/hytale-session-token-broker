package hytale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	HTTP HTTPDoer

	AccountDataBaseURL string
	SessionsBaseURL    string
}

type ProfilesResponse struct {
	Owner    string    `json:"owner"`
	Profiles []Profile `json:"profiles"`
}

type Profile struct {
	UUID     string `json:"uuid"`
	Username string `json:"username"`
}

type CreateSessionRequest struct {
	UUID string `json:"uuid"`
}

type CreateSessionResponse struct {
	SessionToken  string    `json:"sessionToken"`
	IdentityToken string    `json:"identityToken"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

func (c Client) GetProfiles(ctx context.Context, accessToken string) (ProfilesResponse, error) {
	u, err := url.Parse(c.AccountDataBaseURL)
	if err != nil {
		return ProfilesResponse{}, err
	}
	u.Path = joinPathPreserveLeadingSlash(u.Path, "my-account", "get-profiles")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ProfilesResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ProfilesResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return ProfilesResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProfilesResponse{}, fmt.Errorf("get profiles status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var out ProfilesResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return ProfilesResponse{}, err
	}
	return out, nil
}

func joinPathPreserveLeadingSlash(basePath string, elems ...string) string {
	out := basePath
	if out == "" {
		out = "/"
	}
	parts := append([]string{out}, elems...)
	joined := path.Join(parts...)
	if !strings.HasPrefix(joined, "/") {
		joined = "/" + joined
	}
	return joined
}

func (c Client) CreateGameSession(ctx context.Context, accessToken string, profileUUID string) (CreateSessionResponse, error) {
	if profileUUID == "" {
		return CreateSessionResponse{}, errors.New("profile is required")
	}

	u, err := url.Parse(c.SessionsBaseURL)
	if err != nil {
		return CreateSessionResponse{}, err
	}
	u.Path = joinPathPreserveLeadingSlash(u.Path, "game-session", "new")

	payload, err := json.Marshal(CreateSessionRequest{UUID: profileUUID})
	if err != nil {
		return CreateSessionResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return CreateSessionResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return CreateSessionResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return CreateSessionResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CreateSessionResponse{}, fmt.Errorf("create session status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var out CreateSessionResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return CreateSessionResponse{}, err
	}
	if out.SessionToken == "" || out.IdentityToken == "" {
		return CreateSessionResponse{}, errors.New("missing session tokens")
	}
	return out, nil
}
