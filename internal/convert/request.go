// Package convert implements the explicitly supported cross-family semantic intersection.
package convert

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

type UnsupportedError struct{ Feature string }

func (e *UnsupportedError) Error() string { return "unsupported cross-family feature: " + e.Feature }

type request struct {
	Model                          string
	Stream                         bool
	Instructions                   []part
	Messages                       []message
	Tools                          []tool
	ToolChoiceKind, ToolChoiceName string
	MaxTokens                      *int
	Temperature, TopP              *float64
	Stops                          []string
	Parallel                       *bool
}
type message struct {
	Role  string
	Parts []part
}
type part struct {
	Kind, Text, URL, MediaType, ID, Name string
	JSON                                 json.RawMessage
}
type tool struct {
	Name, Description string
	Schema            json.RawMessage
}

func Request(raw []byte, from, to model.Family, upstreamModel string) ([]byte, bool, error) {
	if from == to {
		return nil, false, fmt.Errorf("conversion called for native route")
	}
	var r request
	var err error
	switch from {
	case model.OpenAIChat:
		r, err = parseChat(raw)
	case model.OpenAIResponses:
		r, err = parseResponses(raw)
	case model.AnthropicMessages:
		r, err = parseMessages(raw)
	default:
		err = &UnsupportedError{Feature: "source family"}
	}
	if err != nil {
		return nil, false, err
	}
	r.Model = upstreamModel
	var out any
	switch to {
	case model.OpenAIChat:
		out, err = renderChat(r)
	case model.OpenAIResponses:
		out, err = renderResponses(r)
	case model.AnthropicMessages:
		out, err = renderMessages(r)
	default:
		err = &UnsupportedError{Feature: "destination family"}
	}
	if err != nil {
		return nil, false, err
	}
	b, err := json.Marshal(out)
	return b, r.Stream, err
}

func rejectUnknown(m map[string]json.RawMessage, allowed map[string]bool) error {
	for k, v := range m {
		if allowed[k] {
			continue
		}
		v = bytes.TrimSpace(v)
		if bytes.Equal(v, []byte("null")) || bytes.Equal(v, []byte("false")) || bytes.Equal(v, []byte("0")) || bytes.Equal(v, []byte(`""`)) || bytes.Equal(v, []byte("[]")) || bytes.Equal(v, []byte("{}")) {
			continue
		}
		return &UnsupportedError{Feature: k}
	}
	return nil
}

func decodeMap(raw []byte, allowed ...string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, fmt.Errorf("request must be a JSON object")
	}
	a := map[string]bool{}
	for _, k := range allowed {
		a[k] = true
	}
	if err := rejectUnknown(m, a); err != nil {
		return nil, err
	}
	return m, nil
}

func rawString(v json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", err
	}
	return s, nil
}

func unmarshalOptional(m map[string]json.RawMessage, key string, dst any) error {
	v := m[key]
	if len(v) == 0 || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(v, dst); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

func textParts(raw json.RawMessage, family model.Family) ([]part, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []part{{Kind: "text", Text: s}}, nil
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, &UnsupportedError{Feature: "non-text content"}
	}
	out := []part{}
	for _, b := range blocks {
		typ, err := rawString(b["type"])
		if err != nil {
			return nil, fmt.Errorf("content block type: %w", err)
		}
		switch typ {
		case "text", "input_text", "output_text":
			if err := rejectUnknown(b, map[string]bool{"type": true, "text": true}); err != nil {
				return nil, err
			}
			s, err := rawString(b["text"])
			if err != nil {
				return nil, err
			}
			out = append(out, part{Kind: "text", Text: s})
		case "image_url":
			if err := rejectUnknown(b, map[string]bool{"type": true, "image_url": true}); err != nil {
				return nil, err
			}
			var image map[string]json.RawMessage
			if err := json.Unmarshal(b["image_url"], &image); err != nil {
				return nil, err
			}
			if err := rejectUnknown(image, map[string]bool{"url": true}); err != nil {
				return nil, err
			}
			u, err := rawString(image["url"])
			if err != nil {
				return nil, err
			}
			out = append(out, part{Kind: "image", URL: u})
		case "input_image":
			if err := rejectUnknown(b, map[string]bool{"type": true, "image_url": true}); err != nil {
				return nil, err
			}
			u, err := rawString(b["image_url"])
			if err != nil {
				return nil, err
			}
			if u == "" {
				return nil, &UnsupportedError{Feature: "file image reference"}
			}
			out = append(out, part{Kind: "image", URL: u})
		case "image":
			if err := rejectUnknown(b, map[string]bool{"type": true, "source": true}); err != nil {
				return nil, err
			}
			var source map[string]json.RawMessage
			if err := json.Unmarshal(b["source"], &source); err != nil {
				return nil, err
			}
			if err := rejectUnknown(source, map[string]bool{"type": true, "url": true, "media_type": true, "data": true}); err != nil {
				return nil, err
			}
			var src struct {
				Type      string `json:"type"`
				URL       string `json:"url"`
				MediaType string `json:"media_type"`
				Data      string `json:"data"`
			}
			if err := json.Unmarshal(b["source"], &src); err != nil {
				return nil, err
			}
			switch src.Type {
			case "url":
				out = append(out, part{Kind: "image", URL: src.URL})
			case "base64":
				out = append(out, part{Kind: "image", MediaType: src.MediaType, JSON: json.RawMessage(strJSON(src.Data))})
			default:
				return nil, &UnsupportedError{Feature: "image source"}
			}
		case "tool_use":
			if err := rejectUnknown(b, map[string]bool{"type": true, "id": true, "name": true, "input": true}); err != nil {
				return nil, err
			}
			var x struct {
				ID, Name string
				Input    json.RawMessage
			}
			if err := json.Unmarshal(mustJSON(b), &x); err != nil {
				return nil, err
			}
			out = append(out, part{Kind: "tool_call", ID: x.ID, Name: x.Name, JSON: x.Input})
		case "tool_result":
			if err := rejectUnknown(b, map[string]bool{"type": true, "tool_use_id": true, "content": true}); err != nil {
				return nil, err
			}
			var x struct {
				ToolUseID string `json:"tool_use_id"`
				Content   json.RawMessage
			}
			if err := json.Unmarshal(mustJSON(b), &x); err != nil {
				return nil, err
			}
			out = append(out, part{Kind: "tool_result", ID: x.ToolUseID, JSON: x.Content})
		case "refusal":
			if err := rejectUnknown(b, map[string]bool{"type": true, "refusal": true}); err != nil {
				return nil, err
			}
			s, err := rawString(b["refusal"])
			if err != nil {
				return nil, err
			}
			out = append(out, part{Kind: "refusal", Text: s})
		case "thinking", "redacted_thinking", "reasoning_text":
			return nil, &UnsupportedError{Feature: "signed reasoning/thinking"}
		default:
			return nil, &UnsupportedError{Feature: string(family) + " content block " + typ}
		}
	}
	return out, nil
}

func parseToolChoice(raw json.RawMessage, family model.Family) (string, string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "auto" || s == "none" || s == "required" {
			return s, "", nil
		}
		return "", "", &UnsupportedError{Feature: "tool_choice " + s}
	}
	var rawChoice map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawChoice); err != nil {
		return "", "", err
	}
	if err := rejectUnknown(rawChoice, map[string]bool{"type": true, "name": true, "function": true}); err != nil {
		return "", "", err
	}
	if functionRaw := rawChoice["function"]; len(functionRaw) > 0 {
		var function map[string]json.RawMessage
		if err := json.Unmarshal(functionRaw, &function); err != nil {
			return "", "", err
		}
		if err := rejectUnknown(function, map[string]bool{"name": true}); err != nil {
			return "", "", err
		}
	}
	var x struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &x); err != nil {
		return "", "", err
	}
	if family == model.AnthropicMessages {
		switch x.Type {
		case "auto", "none":
			return x.Type, "", nil
		case "any":
			return "required", "", nil
		case "tool":
			if x.Name != "" {
				return "named", x.Name, nil
			}
		}
	} else if x.Type == "function" {
		if x.Name != "" {
			return "named", x.Name, nil
		}
		if x.Function.Name != "" {
			return "named", x.Function.Name, nil
		}
	}
	return "", "", &UnsupportedError{Feature: "tool_choice shape"}
}

func renderToolChoice(kind, name string, family model.Family) any {
	if kind == "" {
		return nil
	}
	if kind == "named" {
		if family == model.OpenAIChat {
			return map[string]any{"type": "function", "function": map[string]any{"name": name}}
		}
		if family == model.OpenAIResponses {
			return map[string]any{"type": "function", "name": name}
		}
		return map[string]any{"type": "tool", "name": name}
	}
	if family == model.AnthropicMessages && kind == "required" {
		return map[string]any{"type": "any"}
	}
	if family == model.AnthropicMessages {
		return map[string]any{"type": kind}
	}
	return kind
}

func mustJSON(v any) []byte   { b, _ := json.Marshal(v); return b }
func strJSON(s string) []byte { b, _ := json.Marshal(s); return b }
