package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hybrowse/hytale-session-token-broker/internal/config"
	"github.com/hybrowse/hytale-session-token-broker/internal/hytale"
	"github.com/hybrowse/hytale-session-token-broker/internal/oauth"
	"github.com/hybrowse/hytale-session-token-broker/internal/store"
)

type NowFunc func() time.Time

type ctxKey string

const requestIDKey ctxKey = "request_id"

var requestSeq uint64

func nextRequestID() string {
	v := atomic.AddUint64(&requestSeq, 1)
	return fmt.Sprintf("%06d", v)
}

func requestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

func shortUUID(v string) string {
	v = strings.TrimSpace(v)
	if len(v) <= 8 {
		return v
	}
	return v[:8] + "…"
}

type Dependencies struct {
	Config config.Config
	Store  store.Store
	Now    NowFunc
}

type Broker struct {
	cfg   config.Config
	store store.Store
	now   NowFunc

	httpClient *http.Client

	accountLocks sync.Map
}

type NotAuthenticatedError struct {
	Account string
}

func (e NotAuthenticatedError) Error() string {
	if strings.ToLower(strings.TrimSpace(e.Account)) == "any" {
		return "no accounts are authenticated"
	}
	return fmt.Sprintf("account %q is not authenticated", e.Account)
}

type ReauthRequiredError struct {
	Account   string
	OAuthCode string
}

func (e ReauthRequiredError) Error() string {
	if e.OAuthCode == "" {
		return fmt.Sprintf("account %q needs re-authentication", e.Account)
	}
	return fmt.Sprintf("account %q needs re-authentication (%s)", e.Account, e.OAuthCode)
}

func New(deps Dependencies) *Broker {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Broker{
		cfg:        deps.Config,
		store:      deps.Store,
		now:        deps.Now,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (b *Broker) oauthClient() oauth.Client {
	return oauth.Client{
		HTTP:          b.httpClient,
		ClientID:      b.cfg.OAuth.ClientID,
		Scope:         b.cfg.OAuth.Scope,
		DeviceAuthURL: b.cfg.OAuth.DeviceAuthURL,
		TokenURL:      b.cfg.OAuth.TokenURL,
	}
}

func (b *Broker) hytaleClient() hytale.Client {
	return hytale.Client{
		HTTP:               b.httpClient,
		AccountDataBaseURL: b.cfg.Hytale.AccountDataBaseURL,
		SessionsBaseURL:    b.cfg.Hytale.SessionsBaseURL,
	}
}

func (b *Broker) accountLock(account string) *sync.Mutex {
	v, _ := b.accountLocks.LoadOrStore(account, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (b *Broker) Serve(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	mux.HandleFunc("/v1/game-session", b.authMiddleware(b.handleCreateGameSession))

	srv := &http.Server{
		Addr:              b.cfg.HTTP.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", b.cfg.HTTP.Addr)
	err := srv.ListenAndServe()
	if err != nil && errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (b *Broker) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if b.cfg.HTTP.BearerToken == "" {
			next(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		want := "Bearer " + b.cfg.HTTP.BearerToken
		if auth != want {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprintf(w, `{"error":%q}`, "unauthorized")
			return
		}
		next(w, r)
	}
}

type createGameSessionRequest struct {
	Account      string   `json:"account"`
	ProfileUUIDs []string `json:"profile_uuids"`
}

type createGameSessionResponse struct {
	SessionToken  string    `json:"session_token"`
	IdentityToken string    `json:"identity_token"`
	ExpiresAt     time.Time `json:"expires_at"`
}

func (b *Broker) handleCreateGameSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = fmt.Fprintf(w, `{"error":%q}`, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req createGameSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("create game session: invalid json: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":%q}`, "invalid json body")
		return
	}

	account := req.Account
	account = strings.TrimSpace(account)

	ctx := r.Context()
	ctx = context.WithValue(ctx, requestIDKey, nextRequestID())
	reqID := requestIDFromContext(ctx)

	log.Printf("req=%s api: POST /v1/game-session account=%q profile_uuids=%d", reqID, account, len(req.ProfileUUIDs))

	resp, err := b.MintGameSession(ctx, account, req.ProfileUUIDs)
	if err != nil {
		log.Printf("req=%s api: mint failed: %v", reqID, err)
		status := http.StatusBadRequest
		var na NotAuthenticatedError
		var ra ReauthRequiredError
		if errors.As(err, &na) {
			status = http.StatusUnauthorized
		} else if errors.As(err, &ra) {
			status = http.StatusUnauthorized
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":%q}`, err.Error())
		return
	}

	expiresIn := resp.ExpiresAt.Sub(b.now()).Round(time.Second)
	log.Printf("req=%s api: mint ok expires_at=%s expires_in=%s", reqID, resp.ExpiresAt.UTC().Format(time.RFC3339), expiresIn)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (b *Broker) getAccessToken(ctx context.Context, account string) (store.AccountState, string, error) {
	st, err := b.store.Load(ctx)
	if err != nil {
		return store.AccountState{}, "", err
	}
	acc := st.Accounts[account]
	if acc.RefreshToken == "" {
		return store.AccountState{}, "", NotAuthenticatedError{Account: account}
	}

	accessToken := acc.AccessToken
	if accessToken == "" || b.now().Add(2*time.Minute).After(acc.AccessTokenExpiresAt) {
		tok, err := b.oauthClient().RefreshToken(ctx, acc.RefreshToken)
		if err != nil {
			return store.AccountState{}, "", err
		}
		if tok.Error != "" {
			if tok.Error == "invalid_grant" {
				acc.RefreshToken = ""
				acc.AccessToken = ""
				acc.AccessTokenExpiresAt = time.Time{}
				st.Accounts[account] = acc
				if err := b.store.Save(ctx, st); err != nil {
					return store.AccountState{}, "", err
				}
				return store.AccountState{}, "", ReauthRequiredError{Account: account, OAuthCode: tok.Error}
			}
			return store.AccountState{}, "", fmt.Errorf("oauth refresh: %s", tok.Error)
		}
		acc.AccessToken = tok.AccessToken
		acc.AccessTokenExpiresAt = oauth.ExpiresAt(b.now(), tok.ExpiresIn)
		acc.RefreshToken = tok.RefreshToken
		st.Accounts[account] = acc
		if err := b.store.Save(ctx, st); err != nil {
			return store.AccountState{}, "", err
		}
		accessToken = acc.AccessToken
	}

	return acc, accessToken, nil
}

func (b *Broker) MintGameSession(ctx context.Context, account string, profileUUIDs []string) (createGameSessionResponse, error) {
	reqID := requestIDFromContext(ctx)
	log.Printf("req=%s mint: start requested_account=%q explicit_profiles=%d", reqID, strings.TrimSpace(account), len(profileUUIDs))

	st, err := b.store.Load(ctx)
	if err != nil {
		return createGameSessionResponse{}, err
	}

	accounts, accStart, accRREnabled, err := b.accountCandidates(st, account)
	if err != nil {
		return createGameSessionResponse{}, err
	}
	log.Printf("req=%s mint: account candidates=%d rr=%t cursor=%d", reqID, len(accounts), accRREnabled, accStart)

	var lastErr error
	needSave := false
	rotatedAccounts := rotate(accounts, accStart)
	for ai, accName := range rotatedAccounts {
		log.Printf("req=%s mint: trying account=%q", reqID, accName)
		accState := st.Accounts[accName]
		if strings.TrimSpace(accState.RefreshToken) == "" {
			if strings.TrimSpace(account) != "" && strings.ToLower(strings.TrimSpace(account)) != "any" {
				return createGameSessionResponse{}, NotAuthenticatedError{Account: accName}
			}
			log.Printf("req=%s mint: skip account=%q (not authenticated)", reqID, accName)
			continue
		}

		updatedAcc, accessToken, err := b.ensureAccessTokenLocked(ctx, accName, false)
		st.Accounts[accName] = updatedAcc
		if err != nil {
			log.Printf("req=%s mint: account=%q token error: %v", reqID, accName, err)
			if strings.TrimSpace(account) != "" && strings.ToLower(strings.TrimSpace(account)) != "any" {
				return createGameSessionResponse{}, err
			}
			lastErr = err
			continue
		}

		candidates, rrStart, rrEnabled, err := b.profileCandidates(ctx, accName, accessToken, updatedAcc, profileUUIDs)
		if err != nil {
			log.Printf("req=%s mint: account=%q profile candidates error: %v", reqID, accName, err)
			if strings.TrimSpace(account) != "" && strings.ToLower(strings.TrimSpace(account)) != "any" {
				return createGameSessionResponse{}, err
			}
			lastErr = err
			continue
		}
		if len(candidates) == 0 {
			lastErr = errors.New("no profile candidates")
			log.Printf("req=%s mint: account=%q has no profile candidates", reqID, accName)
			continue
		}
		log.Printf("req=%s mint: account=%q profiles=%d rr=%t cursor=%d", reqID, accName, len(candidates), rrEnabled, rrStart)

		rotatedProfiles := rotate(candidates, rrStart)
		for i, uuid := range rotatedProfiles {
			log.Printf("req=%s mint: trying profile=%s (account=%q)", reqID, shortUUID(uuid), accName)
			sess, err := b.hytaleClient().CreateGameSession(ctx, accessToken, uuid)
			if err != nil && strings.Contains(err.Error(), "create session status 403") && strings.Contains(strings.ToLower(err.Error()), "invalid token") {
				updatedAcc, accessToken, rerr := b.ensureAccessTokenLocked(ctx, accName, true)
				st.Accounts[accName] = updatedAcc
				if rerr == nil {
					sess, err = b.hytaleClient().CreateGameSession(ctx, accessToken, uuid)
				}
			}
			if err != nil {
				lastErr = err
				log.Printf("req=%s mint: profile=%s failed (account=%q): %v", reqID, shortUUID(uuid), accName, err)
				continue
			}

			needSave = false
			if rrEnabled {
				updatedAcc.NextProfileIndex = (rrStart + i + 1) % len(candidates)
				needSave = true
			}
			if accRREnabled {
				st.NextAccountIndex = (accStart + ai + 1) % len(accounts)
				needSave = true
			}
			if needSave {
				cur, err := b.store.Load(ctx)
				if err != nil {
					return createGameSessionResponse{}, err
				}
				if rrEnabled {
					acc := cur.Accounts[accName]
					acc.NextProfileIndex = updatedAcc.NextProfileIndex
					cur.Accounts[accName] = acc
				}
				if accRREnabled {
					cur.NextAccountIndex = st.NextAccountIndex
				}
				if err := b.store.Save(ctx, cur); err != nil {
					return createGameSessionResponse{}, err
				}
			}
			expiresIn := sess.ExpiresAt.Sub(b.now()).Round(time.Second)
			log.Printf("req=%s mint: success account=%q profile=%s expires_at=%s expires_in=%s", reqID, accName, shortUUID(uuid), sess.ExpiresAt.UTC().Format(time.RFC3339), expiresIn)

			return createGameSessionResponse{
				SessionToken:  sess.SessionToken,
				IdentityToken: sess.IdentityToken,
				ExpiresAt:     sess.ExpiresAt,
			}, nil
		}
	}

	if lastErr == nil {
		lastErr = NotAuthenticatedError{Account: "any"}
	}
	return createGameSessionResponse{}, lastErr
}

func (b *Broker) profileCandidates(ctx context.Context, account string, accessToken string, acc store.AccountState, profileUUIDs []string) ([]string, int, bool, error) {
	reqID := requestIDFromContext(ctx)
	if len(profileUUIDs) > 0 {
		log.Printf("req=%s mint: profile pool source=request candidates=%d", reqID, len(profileUUIDs))
		return dedupeNonEmpty(profileUUIDs), 0, false, nil
	}

	if len(acc.DefaultProfileUUIDs) > 0 {
		candidates := dedupeNonEmpty(acc.DefaultProfileUUIDs)
		start := 0
		if len(candidates) > 0 && acc.NextProfileIndex > 0 {
			start = acc.NextProfileIndex % len(candidates)
		}
		log.Printf("req=%s mint: profile pool source=state candidates=%d cursor=%d", reqID, len(candidates), start)
		return candidates, start, true, nil
	}

	if cfgAcc, ok := b.cfg.Accounts[account]; ok {
		if len(cfgAcc.ProfileUUIDs) > 0 {
			candidates := dedupeNonEmpty(cfgAcc.ProfileUUIDs)
			start := 0
			if len(candidates) > 0 && acc.NextProfileIndex > 0 {
				start = acc.NextProfileIndex % len(candidates)
			}
			log.Printf("req=%s mint: profile pool source=config(account) candidates=%d cursor=%d", reqID, len(candidates), start)
			return candidates, start, true, nil
		}
	}

	if len(b.cfg.Defaults.ProfileUUIDs) > 0 {
		candidates := dedupeNonEmpty(b.cfg.Defaults.ProfileUUIDs)
		start := 0
		if len(candidates) > 0 && acc.NextProfileIndex > 0 {
			start = acc.NextProfileIndex % len(candidates)
		}
		log.Printf("req=%s mint: profile pool source=config(defaults) candidates=%d cursor=%d", reqID, len(candidates), start)
		return candidates, start, true, nil
	}

	profiles, err := b.hytaleClient().GetProfiles(ctx, accessToken)
	if err != nil {
		return nil, 0, false, err
	}
	if len(profiles.Profiles) == 0 {
		return nil, 0, false, errors.New("no profiles available")
	}
	candidates := make([]string, 0, len(profiles.Profiles))
	for _, p := range profiles.Profiles {
		candidates = append(candidates, p.UUID)
	}
	candidates = dedupeNonEmpty(candidates)
	start := 0
	if len(candidates) > 0 && acc.NextProfileIndex > 0 {
		start = acc.NextProfileIndex % len(candidates)
	}
	log.Printf("req=%s mint: profile pool source=hytale-api candidates=%d cursor=%d", reqID, len(candidates), start)
	return candidates, start, len(candidates) > 1, nil
}

func (b *Broker) ensureAccessTokenLocked(ctx context.Context, account string, force bool) (store.AccountState, string, error) {
	reqID := requestIDFromContext(ctx)
	mu := b.accountLock(account)
	mu.Lock()
	defer mu.Unlock()

	st, err := b.store.Load(ctx)
	if err != nil {
		return store.AccountState{}, "", err
	}
	acc := st.Accounts[account]
	if strings.TrimSpace(acc.RefreshToken) == "" {
		return store.AccountState{}, "", NotAuthenticatedError{Account: account}
	}

	accessToken := acc.AccessToken
	if force || accessToken == "" || b.now().Add(2*time.Minute).After(acc.AccessTokenExpiresAt) {
		log.Printf("req=%s mint: refreshing access token (account=%q)", reqID, account)
		tok, err := b.oauthClient().RefreshToken(ctx, acc.RefreshToken)
		if err != nil {
			return store.AccountState{}, "", err
		}
		if tok.Error != "" {
			if tok.Error == "invalid_grant" {
				log.Printf("req=%s mint: refresh token invalid (account=%q): invalid_grant", reqID, account)
				acc.RefreshToken = ""
				acc.AccessToken = ""
				acc.AccessTokenExpiresAt = time.Time{}
				st.Accounts[account] = acc
				if err := b.store.Save(ctx, st); err != nil {
					return store.AccountState{}, "", err
				}
				return acc, "", ReauthRequiredError{Account: account, OAuthCode: tok.Error}
			}
			return store.AccountState{}, "", fmt.Errorf("oauth refresh: %s", tok.Error)
		}
		log.Printf("req=%s mint: access token refreshed (account=%q)", reqID, account)
		acc.AccessToken = tok.AccessToken
		acc.AccessTokenExpiresAt = oauth.ExpiresAt(b.now(), tok.ExpiresIn)
		acc.RefreshToken = tok.RefreshToken
		st.Accounts[account] = acc
		if err := b.store.Save(ctx, st); err != nil {
			return store.AccountState{}, "", err
		}
		return acc, acc.AccessToken, nil
	}
	return acc, accessToken, nil
}

func (b *Broker) accountCandidates(st store.State, requested string) ([]string, int, bool, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" && strings.ToLower(requested) != "any" {
		return []string{requested}, 0, false, nil
	}

	accounts := make([]string, 0, len(st.Accounts))
	for name, acc := range st.Accounts {
		if strings.TrimSpace(acc.RefreshToken) == "" {
			continue
		}
		accounts = append(accounts, name)
	}
	if len(accounts) == 0 {
		return nil, 0, false, NotAuthenticatedError{Account: "any"}
	}
	sort.Strings(accounts)

	start := 0
	if st.NextAccountIndex > 0 {
		start = st.NextAccountIndex % len(accounts)
	}
	return accounts, start, true, nil
}

func (b *Broker) SetDefaultProfiles(ctx context.Context, account string, profileUUIDs []string) error {
	account = strings.TrimSpace(account)
	if account == "" {
		return errors.New("account is required")
	}
	profileUUIDs = dedupeNonEmpty(profileUUIDs)
	if len(profileUUIDs) == 0 {
		return errors.New("at least one profile is required")
	}

	st, err := b.store.Load(ctx)
	if err != nil {
		return err
	}
	acc := st.Accounts[account]
	if acc.RefreshToken == "" {
		return fmt.Errorf("account %q is not authenticated", account)
	}
	acc.DefaultProfileUUIDs = profileUUIDs
	acc.NextProfileIndex = 0
	st.Accounts[account] = acc
	return b.store.Save(ctx, st)
}

func (b *Broker) AuthLoginDevice(ctx context.Context, out io.Writer, account string) error {
	if account == "" {
		return errors.New("account is required")
	}

	d, err := b.oauthClient().StartDeviceAuth(ctx)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "Visit: %s\nCode: %s\n", firstNonEmpty(d.VerificationURIComplete, d.VerificationURI), d.UserCode)

	deadline := b.now().Add(time.Duration(d.ExpiresIn) * time.Second)
	interval := time.Duration(d.Interval) * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if b.now().After(deadline) {
			return errors.New("device code expired")
		}
		tok, err := b.oauthClient().PollDeviceToken(ctx, d.DeviceCode)
		if err != nil {
			return err
		}
		if tok.Error == "authorization_pending" {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
			continue
		}
		if tok.Error != "" {
			return fmt.Errorf("oauth: %s", tok.Error)
		}

		st, err := b.store.Load(ctx)
		if err != nil {
			return err
		}
		acc := st.Accounts[account]
		acc.RefreshToken = tok.RefreshToken
		acc.AccessToken = tok.AccessToken
		acc.AccessTokenExpiresAt = oauth.ExpiresAt(b.now(), tok.ExpiresIn)
		st.Accounts[account] = acc
		if err := b.store.Save(ctx, st); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "Authentication successful for account %q\n", account)
		return nil
	}
}

func (b *Broker) AuthStatus(ctx context.Context, out io.Writer, account string) error {
	st, err := b.store.Load(ctx)
	if err != nil {
		return err
	}
	acc := st.Accounts[account]
	if acc.RefreshToken == "" {
		_, _ = fmt.Fprintf(out, "account %q: not authenticated\n", account)
		return nil
	}
	_, _ = fmt.Fprintf(out, "account %q: authenticated\naccess token expires: %s\ndefault profiles: %s\n", account, acc.AccessTokenExpiresAt.Format(time.RFC3339), strings.Join(acc.DefaultProfileUUIDs, ","))
	return nil
}

func (b *Broker) PrintProfiles(ctx context.Context, out io.Writer, account string) error {
	_, accessToken, err := b.getAccessToken(ctx, account)
	if err != nil {
		return err
	}

	profiles, err := b.hytaleClient().GetProfiles(ctx, accessToken)
	if err != nil {
		return err
	}

	for _, p := range profiles.Profiles {
		_, _ = fmt.Fprintf(out, "%s %s\n", p.UUID, p.Username)
	}
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func dedupeNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func rotate(in []string, start int) []string {
	if len(in) == 0 {
		return nil
	}
	if start <= 0 {
		return in
	}
	start = start % len(in)
	return append(append([]string{}, in[start:]...), in[:start]...)
}
