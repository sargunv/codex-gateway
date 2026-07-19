// Package kimi implements Kimi CLI credential-file borrowing and refresh.
package kimi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sargunv/agent-api-gateway/internal/auth/authfile"
)

const (
	DefaultRefreshURL = "https://api.kimi.com/oauth2/token"
	ClientID          = "17e5f671-d194-4dfb-9706-5516cb48c098"
)

type Manager struct {
	mu                 sync.Mutex
	Path, RefreshURL   string
	Client             *http.Client
	Now                func() time.Time
	DeviceID, Platform string
}

func New(path string) (*Manager, error) {
	m := &Manager{Path: path, RefreshURL: DefaultRefreshURL, Client: &http.Client{Timeout: 10 * time.Second}, Now: time.Now, Platform: "agent-api-gateway"}
	_, err := m.load()
	if err != nil {
		return nil, err
	}
	return m, nil
}
func (m *Manager) Source() string { return "file" }
func (m *Manager) Headers() (map[string]string, error) {
	t, err := m.Borrow(context.Background())
	if err != nil {
		return nil, err
	}
	h := map[string]string{"Authorization": "Bearer " + t, "User-Agent": "agent-api-gateway"}
	if m.DeviceID != "" {
		h["X-Msh-Device-Id"] = m.DeviceID
	}
	h["X-Msh-Platform"] = m.Platform
	return h, nil
}

func (m *Manager) load() (map[string]any, error) {
	v, err := authfile.Read(m.Path)
	if err != nil {
		return nil, err
	}
	if authfile.String(v, "access_token") == "" {
		return nil, errors.New("missing access_token")
	}
	return v, nil
}

func (m *Manager) Borrow(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lockPath := strings.TrimSuffix(m.Path, filepath.Ext(m.Path)) + ".lock"
	unlock, err := authfile.Lock(lockCtx, lockPath)
	if err != nil {
		return "", err
	}
	defer unlock()
	v, err := m.load()
	if err != nil {
		return "", err
	}
	if !expired(v, m.now().Add(30*time.Second)) {
		return authfile.String(v, "access_token"), nil
	}
	old := authfile.String(v, "refresh_token")
	if old == "" {
		return "", errors.New("expired Kimi token and no refresh_token")
	}
	r, err := m.refresh(ctx, old)
	if err != nil {
		latest, e := m.load()
		if e == nil && authfile.String(latest, "refresh_token") != old && !expired(latest, m.now().Add(30*time.Second)) {
			return authfile.String(latest, "access_token"), nil
		}
		return "", err
	}
	latest, err := m.load()
	if err != nil {
		return "", err
	}
	if authfile.String(latest, "refresh_token") != old {
		if !expired(latest, m.now().Add(30*time.Second)) {
			return authfile.String(latest, "access_token"), nil
		}
		return "", errors.New("kimi credentials rotated during refresh but the new token is expired")
	}
	v = latest
	v["access_token"] = r.AccessToken
	if r.RefreshToken != "" {
		v["refresh_token"] = r.RefreshToken
	}
	if r.ExpiresIn > 0 {
		v["expires_at"] = m.now().Add(time.Duration(r.ExpiresIn) * time.Second).Unix()
		v["expires_in"] = r.ExpiresIn
	}
	if err = authfile.AtomicWrite(m.Path, v); err != nil {
		return "", err
	}
	return r.AccessToken, nil
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func expired(v map[string]any, at time.Time) bool {
	switch x := v["expires_at"].(type) {
	case float64:
		return at.Unix() >= int64(x)
	case string:
		if n, e := strconv.ParseInt(x, 10, 64); e == nil {
			return at.Unix() >= n
		}
		if t, e := time.Parse(time.RFC3339, x); e == nil {
			return !at.Before(t)
		}
	}
	return false
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
}

func (m *Manager) refresh(ctx context.Context, t string) (tokenResponse, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t}, "client_id": {ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.RefreshURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Msh-Platform", m.Platform)
	if m.DeviceID != "" {
		req.Header.Set("X-Msh-Device-Id", m.DeviceID)
	}
	resp, err := m.Client.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r tokenResponse
	if err = json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return r, err
	}
	if resp.StatusCode/100 != 2 || r.AccessToken == "" {
		return r, fmt.Errorf("kimi token refresh failed: HTTP %d %s", resp.StatusCode, r.Error)
	}
	return r, nil
}
