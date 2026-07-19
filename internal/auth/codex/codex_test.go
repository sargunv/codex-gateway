package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sargunv/agent-api-gateway/internal/auth/authfile"
)

func token(exp int64) string {
	return rawToken(fmt.Sprintf(`{"exp":%d}`, exp))
}

func rawToken(payload string) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	p := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return h + "." + p + "."
}

func TestRefreshAtomicPreservesUnknownAndConcurrent(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	d := t.TempDir()
	p := filepath.Join(d, "auth.json")
	old := token(now.Add(-time.Hour).Unix())
	doc := fmt.Sprintf(`{"auth_mode":"chatgpt","unknown":{"nested":true},"tokens":{"access_token":%q,"refresh_token":"old","account_id":"acct","token_unknown":7}}`, old)
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token(now.Add(time.Hour).Unix()), "refresh_token": "new", "id_token": "id", "account_id": "newacct"})
	}))
	defer srv.Close()
	m, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	managers := make([]*Manager, 8)
	managers[0] = m
	for i := 1; i < len(managers); i++ {
		managers[i], err = New(p)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, manager := range managers {
		manager.Now = func() time.Time { return now }
		manager.RefreshURL = srv.URL
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(managers))
	for _, manager := range managers {
		wg.Add(1)
		go func(m *Manager) {
			defer wg.Done()
			_, acct, e := m.Borrow(context.Background())
			if e == nil && acct != "newacct" {
				e = fmt.Errorf("account %q", acct)
			}
			errs <- e
		}(manager)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls %d", calls.Load())
	}
	v, err := authfile.Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if authfile.Object(v, "unknown")["nested"] != true || authfile.Object(v, "tokens")["token_unknown"] == nil {
		t.Fatal("unknown fields lost")
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
}

func TestRejectedRefreshRecoversExternalRotation(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	d := t.TempDir()
	p := filepath.Join(d, "auth.json")
	write := func(access, refresh string) {
		v := map[string]any{"auth_mode": "chatgpt", "tokens": map[string]any{"access_token": access, "refresh_token": refresh, "account_id": "acct"}}
		if err := authfile.AtomicWrite(p, v); err != nil {
			t.Fatal(err)
		}
	}
	write(token(now.Add(-time.Hour).Unix()), "stale")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write(token(now.Add(time.Hour).Unix()), "rotated")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
	}))
	defer srv.Close()
	m, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	m.Now = func() time.Time { return now }
	m.RefreshURL = srv.URL
	got, _, err := m.Borrow(context.Background())
	if err != nil || !valid(got, now) {
		t.Fatalf("%v %q", err, got)
	}
}
