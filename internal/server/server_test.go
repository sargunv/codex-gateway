package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/sargunv/agent-api-gateway/internal/config"
	"github.com/sargunv/agent-api-gateway/internal/model"
)

type capture struct {
	mu     sync.Mutex
	body   []byte
	header http.Header
	path   string
}

func fixture(t *testing.T, f model.Family, upstream http.HandlerFunc) (*Server, *httptest.Server) {
	t.Helper()
	u := httptest.NewServer(upstream)
	t.Cleanup(u.Close)
	base, _ := url.Parse(u.URL + "/v1")
	a := &model.Account{ID: "fixture", CredentialSource: "env", Endpoints: map[string]*model.Endpoint{"e": {ID: "e", Family: f, BaseURL: base, Credential: model.StaticCredential{Value: "upstream-secret", Header: "Authorization", Prefix: "Bearer "}}}, Models: map[string]*model.Model{"m": {ID: "m", UpstreamID: "up", Routes: []model.Route{{EndpointID: "e", Preferred: true}}}}}
	s, err := New(&config.Config{APIKey: "downstream-secret", Accounts: []*model.Account{a}, MaxBodyBytes: 1 << 20, MaxEventBytes: 1 << 16}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s, u
}

func request(t *testing.T, h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set("Authorization", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestNativePassThroughAndSingleAuth(t *testing.T) {
	var c capture
	s, _ := fixture(t, model.OpenAIChat, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.body = b
		c.header = r.Header.Clone()
		c.path = r.URL.Path
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[],"extension":{"energy":7}}`))
	})
	in := `{ "model" : "fixture/m", "messages":[], "unknown":{"nested":[1,2]} }`
	w := request(t, s.Handler(), "POST", "/v1/chat/completions", "Bearer downstream-secret", in)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if c.path != "/v1/chat/completions" || c.header.Get("Authorization") != "Bearer upstream-secret" {
		t.Fatalf("path/header %s %q", c.path, c.header.Get("Authorization"))
	}
	if strings.Contains(string(c.body), "downstream-secret") || !strings.Contains(string(c.body), `"unknown":{"nested":[1,2]}`) || !strings.Contains(string(c.body), `"model" : "up"`) {
		t.Fatalf("body %s", c.body)
	}
	if !strings.Contains(w.Body.String(), `"energy":7`) {
		t.Fatal("native extension lost")
	}
}

func TestAuthenticationFormsAndRouteAllowlist(t *testing.T) {
	s, _ := fixture(t, model.OpenAIChat, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	})
	for _, auth := range []struct{ a, x string }{{"downstream-secret", ""}, {"Bearer downstream-secret", ""}, {"", "downstream-secret"}} {
		r := httptest.NewRequest("GET", "/v1/models", nil)
		r.Header.Set("Authorization", auth.a)
		r.Header.Set("x-api-key", auth.x)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("auth %#v => %d", auth, w.Code)
		}
	}
	w := request(t, s.Handler(), "GET", "/v1/unknown", "downstream-secret", "")
	if w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
	w = request(t, s.Handler(), "GET", "/v1/chat/completions", "downstream-secret", "")
	if w.Code != 405 {
		t.Fatalf("%d", w.Code)
	}
}

func TestAnthropicErrorShapeAndReadiness(t *testing.T) {
	c := &config.Config{APIKey: "x", MaxBodyBytes: 1024, MaxEventBytes: 1024}
	s, err := New(c, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	w := request(t, s.Handler(), "POST", "/v1/messages", "bad", `{}`)
	if w.Code != 401 || !strings.Contains(w.Body.String(), `"type":"error"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	w = request(t, s.Handler(), "GET", "/readyz", "", "")
	if w.Code != 503 {
		t.Fatalf("%d", w.Code)
	}
	w = request(t, s.Handler(), "GET", "/healthz", "", "")
	if w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
}

func TestCrossFamilyMessagesToChat(t *testing.T) {
	var got map[string]any
	s, _ := fixture(t, model.OpenAIChat, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","model":"up","choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	})
	in := `{"model":"fixture/m","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`
	w := request(t, s.Handler(), "POST", "/v1/messages", "downstream-secret", in)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if got["model"] != "up" {
		t.Fatalf("%#v", got)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"type":"message"`)) || !bytes.Contains(w.Body.Bytes(), []byte("hello")) {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestReviewRegressionsRejectBeforeUpstream(t *testing.T) {
	var calls int
	upstream := func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}

	s, _ := fixture(t, model.OpenAIResponses, upstream)
	w := request(t, s.Handler(), "POST", "/v1/chat/completions", "downstream-secret", `{"model":"fixture/m","messages":[],"stream":true}`)
	if w.Code != http.StatusUnprocessableEntity || calls != 0 {
		t.Fatalf("converted stream: status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
	}

	s, _ = fixture(t, model.OpenAIChat, upstream)
	w = request(t, s.Handler(), "POST", "/v1/chat/completions", "downstream-secret", `{"model":"safe","model":"fixture/m","messages":[]}`)
	if w.Code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("duplicate model: status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
	}

	s, _ = fixture(t, model.AnthropicMessages, upstream)
	w = request(t, s.Handler(), "POST", "/v1/messages/count_tokens", "downstream-secret", `{"model":"fixture/m","messages":[]}`)
	if w.Code != http.StatusUnprocessableEntity || calls != 0 {
		t.Fatalf("count_tokens: status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
	}
}
