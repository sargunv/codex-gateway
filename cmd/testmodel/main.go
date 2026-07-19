// Command testmodel is a deterministic, loopback-only upstream used by the
// external-client compatibility tests. It must never be used as a provider.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const sentinel = "COMPAT_OK"

type summary struct {
	Method               string `json:"method"`
	Path                 string `json:"path"`
	Family               string `json:"family"`
	Model                string `json:"model"`
	Stream               bool   `json:"stream"`
	HasUserInput         bool   `json:"has_user_input"`
	ToolsPresent         bool   `json:"tools_present"`
	AuthHeader           string `json:"auth_header"`
	AuthValid            bool   `json:"auth_valid"`
	DownstreamAuthLeaked bool   `json:"downstream_auth_leaked"`
	AnthropicVersion     string `json:"anthropic_version,omitempty"`
}

type fixture struct {
	mu           sync.Mutex
	requestsFile string
	expectedAuth string
	downstream   string
	requests     []summary
}

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "loopback listen address")
	ready := flag.String("ready-file", "", "atomically published bound address")
	requests := flag.String("requests-file", "", "append-only structural JSONL recorder")
	auth := flag.String("expected-auth", "fixture-upstream-key", "expected upstream credential")
	downstream := flag.String("downstream-auth", "test-gateway-key", "credential which must not reach upstream")
	flag.Parse()
	if *ready == "" || *requests == "" {
		log.Fatal("--ready-file and --requests-file are required")
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	host, _, err := net.SplitHostPort(ln.Addr().String())
	if err != nil || host != "127.0.0.1" {
		_ = ln.Close()
		log.Fatalf("testmodel must bind literal 127.0.0.1, got %q", ln.Addr())
	}
	f := &fixture{requestsFile: *requests, expectedAuth: *auth, downstream: *downstream}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", f.health)
	mux.HandleFunc("/v1/models", f.models)
	mux.HandleFunc("/v1/chat/completions", f.chat)
	mux.HandleFunc("/v1/responses", f.responses)
	mux.HandleFunc("/v1/messages", f.messages)
	mux.HandleFunc("/v1/messages/count_tokens", f.countTokens)
	mux.HandleFunc("/requests", f.recorded)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := publishReady(*ready, ln.Addr().String()); err != nil {
		_ = ln.Close()
		log.Fatal(err)
	}
	defer func() { _ = os.Remove(*ready) }()
	if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func publishReady(path, address string) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if _, err = fmt.Fprintln(f, address); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func (f *fixture) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (f *fixture) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]any{"object": "list", "data": []any{
		map[string]any{"id": "fixture-chat", "object": "model", "created": 1, "owned_by": "fixture"},
		map[string]any{"id": "fixture-responses", "object": "model", "created": 1, "owned_by": "fixture"},
		map[string]any{"id": "fixture-messages", "object": "model", "created": 1, "owned_by": "fixture"},
	}})
}

func (f *fixture) generation(w http.ResponseWriter, r *http.Request, family string) (map[string]any, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return nil, false
	}
	defer func() { _ = r.Body.Close() }()
	var body map[string]any
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	d.UseNumber()
	if err := d.Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return nil, false
	}
	model, _ := body["model"].(string)
	stream, _ := body["stream"].(bool)
	_, toolsPresent := body["tools"]
	authHeader, authValue := auth(r)
	s := summary{
		Method:               r.Method,
		Path:                 r.URL.Path,
		Family:               family,
		Model:                model,
		Stream:               stream,
		HasUserInput:         hasUserInput(body),
		ToolsPresent:         toolsPresent,
		AuthHeader:           authHeader,
		AuthValid:            authValue == f.expectedAuth,
		DownstreamAuthLeaked: authValue == f.downstream,
		AnthropicVersion:     r.Header.Get("anthropic-version"),
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 0 {
		http.Error(w, "unexpected second generation request", http.StatusConflict)
		return nil, false
	}
	f.requests = append(f.requests, s)
	line, _ := json.Marshal(s)
	file, err := os.OpenFile(f.requestsFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		http.Error(w, "recorder unavailable", http.StatusInternalServerError)
		return nil, false
	}
	_, writeErr := fmt.Fprintln(file, string(line))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		http.Error(w, "recorder unavailable", http.StatusInternalServerError)
		return nil, false
	}
	if !s.AuthValid || s.DownstreamAuthLeaked || !s.HasUserInput || model == "" {
		http.Error(w, "fixture contract rejected request", http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

func auth(r *http.Request) (string, string) {
	if v := r.Header.Get("Authorization"); v != "" {
		return "authorization", strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
	}
	if v := r.Header.Get("x-api-key"); v != "" {
		return "x-api-key", strings.TrimSpace(v)
	}
	return "", ""
}

func hasUserInput(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, "fixture marker") || strings.Contains(x, "COMPAT_OK")
	case []any:
		for _, item := range x {
			if hasUserInput(item) {
				return true
			}
		}
	case map[string]any:
		for k, item := range x {
			if k == "instructions" || k == "input" || k == "messages" || k == "content" || k == "text" {
				if hasUserInput(item) {
					return true
				}
			}
		}
	}
	return false
}

func (f *fixture) chat(w http.ResponseWriter, r *http.Request) {
	body, ok := f.generation(w, r, "openai-chat")
	if !ok {
		return
	}
	model, _ := body["model"].(string)
	if stream, _ := body["stream"].(bool); !stream {
		writeJSON(w, map[string]any{"id": "chatcmpl_fixture", "object": "chat.completion", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": sentinel}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
		return
	}
	sseHeaders(w)
	writeSSE(w, "", map[string]any{"id": "chatcmpl_fixture", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": nil}}})
	writeSSE(w, "", map[string]any{"id": "chatcmpl_fixture", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": sentinel}, "finish_reason": nil}}})
	writeSSE(w, "", map[string]any{"id": "chatcmpl_fixture", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func (f *fixture) responses(w http.ResponseWriter, r *http.Request) {
	body, ok := f.generation(w, r, "openai-responses")
	if !ok {
		return
	}
	model, _ := body["model"].(string)
	message := map[string]any{"id": "msg_fixture", "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "annotations": []any{}, "text": sentinel}}}
	response := map[string]any{"id": "resp_fixture", "object": "response", "created_at": 1, "status": "completed", "error": nil, "incomplete_details": nil, "instructions": nil, "max_output_tokens": nil, "model": model, "output": []any{message}, "parallel_tool_calls": true, "previous_response_id": nil, "reasoning": map[string]any{"effort": nil, "summary": nil}, "store": false, "temperature": 1, "text": map[string]any{"format": map[string]any{"type": "text"}}, "tool_choice": "auto", "tools": []any{}, "top_p": 1, "truncation": "disabled", "usage": map[string]any{"input_tokens": 1, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens": 1, "output_tokens_details": map[string]any{"reasoning_tokens": 0}, "total_tokens": 2}, "user": nil, "metadata": map[string]any{}}
	if stream, _ := body["stream"].(bool); !stream {
		writeJSON(w, response)
		return
	}
	sseHeaders(w)
	created := clone(response)
	created["status"] = "in_progress"
	created["output"] = []any{}
	created["usage"] = nil
	writeSSE(w, "response.created", map[string]any{"type": "response.created", "sequence_number": 0, "response": created})
	writeSSE(w, "response.in_progress", map[string]any{"type": "response.in_progress", "sequence_number": 1, "response": created})
	itemStart := clone(message)
	itemStart["status"] = "in_progress"
	itemStart["content"] = []any{}
	writeSSE(w, "response.output_item.added", map[string]any{"type": "response.output_item.added", "sequence_number": 2, "output_index": 0, "item": itemStart})
	partStart := map[string]any{"type": "output_text", "annotations": []any{}, "text": ""}
	writeSSE(w, "response.content_part.added", map[string]any{"type": "response.content_part.added", "sequence_number": 3, "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "part": partStart})
	writeSSE(w, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "sequence_number": 4, "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "delta": sentinel, "logprobs": []any{}})
	writeSSE(w, "response.output_text.done", map[string]any{"type": "response.output_text.done", "sequence_number": 5, "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "text": sentinel, "logprobs": []any{}})
	partDone := map[string]any{"type": "output_text", "annotations": []any{}, "text": sentinel}
	writeSSE(w, "response.content_part.done", map[string]any{"type": "response.content_part.done", "sequence_number": 6, "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "part": partDone})
	writeSSE(w, "response.output_item.done", map[string]any{"type": "response.output_item.done", "sequence_number": 7, "output_index": 0, "item": message})
	writeSSE(w, "response.completed", map[string]any{"type": "response.completed", "sequence_number": 8, "response": response})
}

func clone(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (f *fixture) messages(w http.ResponseWriter, r *http.Request) {
	body, ok := f.generation(w, r, "anthropic-messages")
	if !ok {
		return
	}
	model, _ := body["model"].(string)
	if stream, _ := body["stream"].(bool); !stream {
		writeJSON(w, map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "model": model, "content": []any{map[string]any{"type": "text", "text": sentinel}}, "stop_reason": "end_turn", "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}})
		return
	}
	sseHeaders(w)
	writeSSE(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "model": model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}})
	writeSSE(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	writeSSE(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": sentinel}})
	writeSSE(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	writeSSE(w, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 1}})
	writeSSE(w, "message_stop", map[string]any{"type": "message_stop"})
}

func (f *fixture) countTokens(w http.ResponseWriter, r *http.Request) {
	if _, ok := f.generation(w, r, "anthropic-messages-count"); !ok {
		return
	}
	writeJSON(w, map[string]any{"input_tokens": 1})
}

func (f *fixture) recorded(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, f.requests)
	case http.MethodDelete:
		f.requests = nil
		if err := os.WriteFile(f.requestsFile, nil, 0o600); err != nil {
			http.Error(w, "recorder unavailable", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func sseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	data, _ := json.Marshal(value)
	if event != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
