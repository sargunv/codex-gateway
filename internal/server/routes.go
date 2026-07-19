package server

import (
	"net/http"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.Handle("/v1/models", s.withAuth(http.HandlerFunc(s.models)))
	mux.Handle("/v1/chat/completions", s.withAuth(http.HandlerFunc(s.proxy)))
	mux.Handle("/v1/responses", s.withAuth(http.HandlerFunc(s.proxy)))
	mux.Handle("/v1/messages", s.withAuth(http.HandlerFunc(s.proxy)))
	mux.Handle("/v1/messages/count_tokens", s.withAuth(http.HandlerFunc(s.proxy)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		if pattern == "" {
			writeError(w, s.familyForPath(r.URL.Path), http.StatusNotFound, "not_found_error", "route not found")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) familyForPath(p string) model.Family {
	switch p {
	case "/v1/messages", "/v1/messages/count_tokens":
		return model.AnthropicMessages
	case "/v1/responses":
		return model.OpenAIResponses
	default:
		return model.OpenAIChat
	}
}
