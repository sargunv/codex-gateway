package kimi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sargunv/agent-api-gateway/internal/auth/authfile"
)

func TestRefreshPreservesNativeFileAndHeaders(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	p := filepath.Join(t.TempDir(), "kimi-code.json")
	if err := os.WriteFile(p, []byte(`{"access_token":"old","refresh_token":"refresh","expires_at":1,"scope":"coding","unknown":{"x":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("client_id") != ClientID || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh" {
			t.Errorf("unexpected refresh form: %#v", r.Form)
		}
		if r.Header.Get("X-Msh-Device-Id") != "device" {
			t.Errorf("device header missing")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new", "refresh_token": "rotated", "expires_in": 3600})
	}))
	defer srv.Close()
	m, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	m.Now = func() time.Time { return now }
	m.RefreshURL = srv.URL
	m.DeviceID = "device"
	got, err := m.Borrow(context.Background())
	if err != nil || got != "new" {
		t.Fatalf("%q %v", got, err)
	}
	v, _ := authfile.Read(p)
	if authfile.Object(v, "unknown")["x"].(float64) != 1 || authfile.String(v, "scope") != "coding" {
		t.Fatal("unknown native fields lost")
	}
	h, err := m.Headers()
	if err != nil || h["Authorization"] != "Bearer new" || h["X-Msh-Device-Id"] != "device" {
		t.Fatalf("%#v %v", h, err)
	}
}
