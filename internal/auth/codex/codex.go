// Package codex implements Codex CLI auth.json borrowing and refresh.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sargunv/agent-api-gateway/internal/auth/authfile"
)

const (
	ClientID          = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultRefreshURL = "https://auth.openai.com/oauth/token"
)

type Manager struct {
	mu               sync.Mutex
	Path, RefreshURL string
	Client           *http.Client
	Now              func() time.Time
}

func New(path string) (*Manager, error) {
	m := &Manager{Path: path, RefreshURL: DefaultRefreshURL, Client: &http.Client{Timeout: 10 * time.Second}, Now: time.Now}
	_, _, _, err := m.load()
	if err != nil {
		return nil, err
	}
	return m, nil
}
func (m *Manager) Source() string { return "file" }
func (m *Manager) Headers() (map[string]string, error) {
	tok, acct, err := m.Borrow(context.Background())
	if err != nil {
		return nil, err
	}
	fedramp, structuralAccount := m.routing()
	if acct == "" {
		acct = structuralAccount
	}
	h := map[string]string{"Authorization": "Bearer " + tok, "OpenAI-Beta": "responses=experimental", "User-Agent": "agent-api-gateway"}
	if acct != "" {
		h["ChatGPT-Account-ID"] = acct
	}
	if fedramp {
		h["X-OpenAI-Fedramp"] = "true"
	}
	return h, nil
}

func (m *Manager) routing() (bool, string) {
	v, err := authfile.Read(m.Path)
	if err != nil {
		return false, ""
	}
	if ai := authfile.Object(v, "agent_identity"); ai != nil {
		fed, _ := ai["chatgpt_account_is_fedramp"].(bool)
		return fed, authfile.String(ai, "account_id")
	}
	return false, ""
}

func (m *Manager) load() (map[string]any, map[string]any, string, error) {
	v, err := authfile.Read(m.Path)
	if err != nil {
		return nil, nil, "", err
	}
	if mode := authfile.String(v, "auth_mode"); mode != "chatgpt" {
		return nil, nil, "", fmt.Errorf("expected auth_mode chatgpt, got %q", mode)
	}
	t := authfile.Object(v, "tokens")
	if t == nil {
		return nil, nil, "", errors.New("missing nested tokens object")
	}
	access := authfile.String(t, "access_token")
	if access == "" {
		return nil, nil, "", errors.New("missing access_token")
	}
	return v, t, access, nil
}

func (m *Manager) Borrow(ctx context.Context) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	unlock, err := authfile.Lock(lockCtx, m.Path+".lock")
	if err != nil {
		return "", "", err
	}
	defer unlock()
	v, t, access, err := m.load()
	if err != nil {
		return "", "", err
	}
	acct := authfile.String(t, "account_id")
	if acct == "" {
		acct = authfile.String(v, "account_id")
	}
	if valid(access, m.now().Add(30*time.Second)) {
		return access, acct, nil
	}
	oldRefresh := authfile.String(t, "refresh_token")
	if oldRefresh == "" {
		return "", "", errors.New("expired access token and no refresh_token")
	}
	r, err := m.refresh(ctx, oldRefresh)
	if err != nil {
		_, latest, latestAccess, reloadErr := m.load()
		if reloadErr == nil && authfile.String(latest, "refresh_token") != oldRefresh && valid(latestAccess, m.now().Add(30*time.Second)) {
			return latestAccess, authfile.String(latest, "account_id"), nil
		}
		return "", "", err
	}
	latestV, latestT, latestAccess, err := m.load()
	if err != nil {
		return "", "", err
	}
	if authfile.String(latestT, "refresh_token") != oldRefresh {
		if valid(latestAccess, m.now().Add(30*time.Second)) {
			return latestAccess, authfile.String(latestT, "account_id"), nil
		}
		return "", "", errors.New("codex credentials rotated during refresh but the new token is expired")
	}
	v, t = latestV, latestT
	t["access_token"] = r.AccessToken
	if r.RefreshToken != "" {
		t["refresh_token"] = r.RefreshToken
	}
	if r.IDToken != "" {
		t["id_token"] = r.IDToken
	}
	if r.AccountID != "" {
		t["account_id"] = r.AccountID
	}
	v["last_refresh"] = m.now().UTC().Format(time.RFC3339Nano)
	if err = authfile.AtomicWrite(m.Path, v); err != nil {
		return "", "", err
	}
	if r.AccountID != "" {
		acct = r.AccountID
	}
	return r.AccessToken, acct, nil
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	AccountID    string `json:"account_id"`
	Error        string `json:"error"`
}

func (m *Manager) refresh(ctx context.Context, token string) (refreshResponse, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}, "client_id": {ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.RefreshURL, strings.NewReader(form.Encode()))
	if err != nil {
		return refreshResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.Client.Do(req)
	if err != nil {
		return refreshResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r refreshResponse
	if err = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&r); err != nil {
		return r, err
	}
	if resp.StatusCode/100 != 2 || r.AccessToken == "" {
		return r, fmt.Errorf("codex token refresh failed: HTTP %d %s", resp.StatusCode, r.Error)
	}
	return r, nil
}

func valid(raw string, at time.Time) bool {
	p := jwt.NewParser()
	tok, _, err := p.ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		return false
	}
	c, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return false
	}
	exp, err := c.GetExpirationTime()
	return err == nil && exp != nil && at.Before(exp.Time)
}
func Exists(path string) bool { _, err := os.Stat(path); return err == nil }
