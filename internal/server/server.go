// Package server exposes the strict downstream HTTP gateway.
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sargunv/agent-api-gateway/internal/config"
	"github.com/sargunv/agent-api-gateway/internal/convert"
	"github.com/sargunv/agent-api-gateway/internal/model"
	"github.com/sargunv/agent-api-gateway/internal/stream"
	"github.com/sargunv/agent-api-gateway/internal/upstream"
	"github.com/tidwall/sjson"
)

type Server struct {
	apiKey            string
	catalog           *model.Catalog
	accounts          []*model.Account
	upstream          *upstream.Client
	maxBody, maxEvent int64
	logger            *slog.Logger
	handler           http.Handler
}

func New(c *config.Config, rt http.RoundTripper, logger *slog.Logger) (*Server, error) {
	catalog, err := model.NewCatalog(c.Accounts)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{apiKey: c.APIKey, catalog: catalog, accounts: c.Accounts, upstream: upstream.New(rt), maxBody: c.MaxBodyBytes, maxEvent: c.MaxEventBytes, logger: logger}
	s.handler = s.routes()
	return s, nil
}
func (s *Server) Handler() http.Handler { return securityHeaders(s.handler) }
func (s *Server) HTTP(addr string) *http.Server {
	return &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Inventory() {
	for _, a := range s.accounts {
		s.logger.Info("provider loaded", "provider", a.ID, "credential_source", a.CredentialSource, "families", a.Families(), "models", len(a.Models))
	}
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, s.familyForPath(r.URL.Path), http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": s.catalog.List()})
}

func (s *Server) proxy(w http.ResponseWriter, r *http.Request) {
	caller := s.familyForPath(r.URL.Path)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, caller, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxBody))
	if err != nil {
		writeError(w, caller, http.StatusRequestEntityTooLarge, "invalid_request_error", "request body too large")
		return
	}
	if len(body) == 0 || !json.Valid(body) || firstNonSpace(body) != '{' {
		writeError(w, caller, http.StatusBadRequest, "invalid_request_error", "request body must be one JSON object")
		return
	}
	if duplicateTopLevelKey(body, "model") {
		writeError(w, caller, http.StatusBadRequest, "invalid_request_error", "duplicate model field")
		return
	}
	var envelope struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err = json.Unmarshal(body, &envelope); err != nil || model.ParsePublicID(envelope.Model) != nil {
		writeError(w, caller, http.StatusBadRequest, "invalid_request_error", "model must be an exact provider/model id")
		return
	}
	resolved, ok := s.catalog.Resolve(envelope.Model)
	if !ok {
		writeError(w, caller, http.StatusNotFound, "not_found_error", "unknown model")
		return
	}
	ep, needsConversion, err := resolved.Account.Select(resolved.Model, caller)
	if err != nil {
		writeError(w, caller, http.StatusUnprocessableEntity, "unsupported_feature", err.Error())
		return
	}
	count := r.URL.Path == "/v1/messages/count_tokens"
	if count && (ep.Family != model.AnthropicMessages || needsConversion || !ep.CountTokens) {
		writeError(w, caller, http.StatusUnprocessableEntity, "unsupported_feature", "accurate count_tokens is unavailable for this model")
		return
	}
	if needsConversion && envelope.Stream {
		writeError(w, caller, http.StatusUnprocessableEntity, "unsupported_feature", "cross-family streaming conversion is not enabled")
		return
	}
	var upBody []byte
	if needsConversion {
		upBody, _, err = convert.Request(body, caller, ep.Family, resolved.Model.UpstreamID)
		if err != nil {
			s.conversionError(w, caller, err)
			return
		}
	} else {
		upBody, err = sjson.SetBytes(body, "model", resolved.Model.UpstreamID)
		if err != nil {
			writeError(w, caller, http.StatusBadRequest, "invalid_request_error", "invalid model field")
			return
		}
	}
	resp, err := s.upstream.Do(r.Context(), ep, upBody, count, r.Header)
	if err != nil {
		s.logger.Warn("upstream request failed", "provider", resolved.Account.ID, "family", ep.Family)
		writeError(w, caller, http.StatusBadGateway, "api_error", "upstream unavailable")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	copyResponseHeaders(w.Header(), resp.Header)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if !needsConversion {
			w.WriteHeader(resp.StatusCode)
			_ = stream.Copy(r.Context(), w, resp.Body)
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		writeError(w, caller, resp.StatusCode, "api_error", fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode))
		return
	}
	isSSE := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") || envelope.Stream
	if !needsConversion {
		w.WriteHeader(resp.StatusCode)
		_ = stream.Copy(r.Context(), w, resp.Body)
		return
	}
	if isSSE {
		writeError(w, caller, http.StatusBadGateway, "api_error", "unexpected streaming response during cross-family conversion")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, resp.Body, s.maxBody))
	if err != nil {
		writeError(w, caller, http.StatusBadGateway, "api_error", "upstream response too large")
		return
	}
	out, err := convert.Response(raw, ep.Family, caller)
	if err != nil {
		s.logger.Warn("response conversion failed", "provider", resolved.Account.ID, "family", ep.Family, "error", safeError(err))
		writeError(w, caller, http.StatusBadGateway, "api_error", "upstream response uses an unsupported cross-family feature")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(out)
}

func (s *Server) conversionError(w http.ResponseWriter, f model.Family, err error) {
	var u *convert.UnsupportedError
	if errors.As(err, &u) {
		writeError(w, f, http.StatusUnprocessableEntity, "unsupported_feature", u.Error())
		return
	}
	writeError(w, f, http.StatusBadRequest, "invalid_request_error", err.Error())
}

func copyResponseHeaders(dst, src http.Header) {
	for _, k := range []string{"Content-Type", "OpenAI-Request-ID", "Request-ID", "request-id", "x-request-id", "anthropic-request-id"} {
		if v := src.Values(k); len(v) > 0 {
			for _, x := range v {
				dst.Add(k, x)
			}
		}
	}
}

func firstNonSpace(b []byte) byte {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

func duplicateTopLevelKey(body []byte, key string) bool {
	dec := json.NewDecoder(bytes.NewReader(body))
	if _, err := dec.Token(); err != nil {
		return false
	}
	seen := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		name, _ := tok.(string)
		if name == key {
			if seen {
				return true
			}
			seen = true
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return false
		}
	}
	return false
}

func safeError(err error) string {
	s := err.Error()
	if len(s) > 256 {
		s = s[:256]
	}
	return s
}
