package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type State struct {
	NextAccountIndex int                     `json:"next_account_index"`
	Accounts         map[string]AccountState `json:"accounts"`
}

type AccountState struct {
	RefreshToken         string    `json:"refresh_token"`
	AccessToken          string    `json:"access_token"`
	AccessTokenExpiresAt time.Time `json:"access_token_expires_at"`
	DefaultProfileUUIDs  []string  `json:"default_profile_uuids"`
	NextProfileIndex     int       `json:"next_profile_index"`
}

type Store interface {
	Load(ctx context.Context) (State, error)
	Save(ctx context.Context, state State) error
}

type FileStore struct {
	path string
	mu   sync.Mutex
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) Load(ctx context.Context) (State, error) {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{Accounts: map[string]AccountState{}}, nil
		}
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, err
	}
	if st.Accounts == nil {
		st.Accounts = map[string]AccountState{}
	}
	return st, nil
}

func (s *FileStore) Save(ctx context.Context, state State) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	if state.Accounts == nil {
		return errors.New("accounts is required")
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
