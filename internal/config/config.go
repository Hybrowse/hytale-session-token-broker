package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HTTP struct {
		Addr        string `yaml:"addr"`
		BearerToken string `yaml:"bearer_token"`
	} `yaml:"http"`

	Store struct {
		Path string `yaml:"path"`
	} `yaml:"store"`

	OAuth struct {
		ClientID      string `yaml:"client_id"`
		Scope         string `yaml:"scope"`
		DeviceAuthURL string `yaml:"device_auth_url"`
		TokenURL      string `yaml:"token_url"`
	} `yaml:"oauth"`

	Hytale struct {
		AccountDataBaseURL string `yaml:"account_data_base_url"`
		SessionsBaseURL    string `yaml:"sessions_base_url"`
	} `yaml:"hytale"`

	Accounts map[string]AccountConfig `yaml:"accounts"`

	Defaults struct {
		Account      string   `yaml:"account"`
		ProfileUUIDs []string `yaml:"profile_uuids"`
	} `yaml:"defaults"`
}

type AccountConfig struct {
	ProfileUUIDs []string `yaml:"profile_uuids"`
}

func Load(path string) (Config, error) {
	cfg := Config{}
	cfg.HTTP.Addr = ":8080"
	cfg.Store.Path = "/app/data/state.json"
	cfg.OAuth.ClientID = "hytale-server"
	cfg.OAuth.Scope = "openid offline auth:server"
	cfg.OAuth.DeviceAuthURL = "https://oauth.accounts.hytale.com/oauth2/device/auth"
	cfg.OAuth.TokenURL = "https://oauth.accounts.hytale.com/oauth2/token"
	cfg.Hytale.AccountDataBaseURL = "https://account-data.hytale.com"
	cfg.Hytale.SessionsBaseURL = "https://sessions.hytale.com"
	cfg.Accounts = map[string]AccountConfig{}
	cfg.Defaults.Account = "default"
	cfg.Defaults.ProfileUUIDs = nil

	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Store.Path == "" {
		return Config{}, errors.New("store.path is required")
	}
	if cfg.OAuth.ClientID == "" {
		return Config{}, errors.New("oauth.client_id is required")
	}
	if cfg.OAuth.DeviceAuthURL == "" {
		return Config{}, errors.New("oauth.device_auth_url is required")
	}
	if cfg.OAuth.TokenURL == "" {
		return Config{}, errors.New("oauth.token_url is required")
	}
	if cfg.Hytale.AccountDataBaseURL == "" {
		return Config{}, errors.New("hytale.account_data_base_url is required")
	}
	if cfg.Hytale.SessionsBaseURL == "" {
		return Config{}, errors.New("hytale.sessions_base_url is required")
	}
	if cfg.HTTP.Addr == "" {
		cfg.HTTP.Addr = ":8080"
	}
	if src := os.Getenv("HYTALE_SESSION_TOKEN_BROKER_BEARER_TOKEN_SRC"); strings.TrimSpace(src) != "" {
		b, err := os.ReadFile(strings.TrimSpace(src))
		if err != nil {
			return Config{}, fmt.Errorf("read bearer token src: %w", err)
		}
		cfg.HTTP.BearerToken = strings.TrimSpace(string(b))
	}
	if v := os.Getenv("HYTALE_SESSION_TOKEN_BROKER_BEARER_TOKEN"); v != "" {
		cfg.HTTP.BearerToken = v
	}
	if cfg.Defaults.Account == "" {
		cfg.Defaults.Account = "default"
	}
	cfg.Defaults.Account = strings.TrimSpace(cfg.Defaults.Account)

	if strings.EqualFold(cfg.Defaults.Account, "any") {
		return Config{}, errors.New("defaults.account must not be 'any'")
	}
	for name := range cfg.Accounts {
		if strings.EqualFold(strings.TrimSpace(name), "any") {
			return Config{}, errors.New("account name 'any' is reserved")
		}
	}
	return cfg, nil
}
