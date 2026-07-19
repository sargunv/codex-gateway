package server

import (
	"encoding/json"
	"net/http"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func writeError(w http.ResponseWriter, f model.Family, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if f == model.AnthropicMessages {
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": msg}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg, "type": typ, "param": nil, "code": nil}})
}
