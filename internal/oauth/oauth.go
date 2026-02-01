package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	HTTP     HTTPDoer
	ClientID string
	Scope    string

	DeviceAuthURL string
	TokenURL      string
}

type DeviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func (c Client) StartDeviceAuth(ctx context.Context) (DeviceAuthResponse, error) {
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("scope", c.Scope)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.DeviceAuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceAuthResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return DeviceAuthResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return DeviceAuthResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DeviceAuthResponse{}, fmt.Errorf("device auth status %d: %s", resp.StatusCode, string(b))
	}

	var out DeviceAuthResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return DeviceAuthResponse{}, err
	}
	if out.Interval == 0 {
		out.Interval = 5
	}
	return out, nil
}

func (c Client) PollDeviceToken(ctx context.Context, deviceCode string) (TokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("device_code", deviceCode)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return TokenResponse{}, err
	}

	var out TokenResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return TokenResponse{}, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out.AccessToken == "" {
			return TokenResponse{}, errors.New("missing access_token")
		}
		return out, nil
	}
	if out.Error == "" {
		out.Error = strings.TrimSpace(string(b))
	}
	return out, nil
}

func (c Client) RefreshToken(ctx context.Context, refreshToken string) (TokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return TokenResponse{}, err
	}

	var out TokenResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return TokenResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if out.Error == "" {
			out.Error = strings.TrimSpace(string(b))
		}
		return out, nil
	}
	if out.AccessToken == "" {
		return TokenResponse{}, errors.New("missing access_token")
	}
	if out.RefreshToken == "" {
		out.RefreshToken = refreshToken
	}
	return out, nil
}

func ExpiresAt(now time.Time, expiresInSeconds int) time.Time {
	if expiresInSeconds <= 0 {
		return now
	}
	return now.Add(time.Duration(expiresInSeconds) * time.Second)
}
