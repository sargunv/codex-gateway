package convert

import (
	"encoding/json"
	"fmt"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func parseChat(raw []byte) (request, error) {
	m, err := decodeMap(raw, "model", "messages", "tools", "tool_choice", "stream", "max_tokens", "max_completion_tokens", "temperature", "top_p", "stop", "parallel_tool_calls")
	if err != nil {
		return request{}, err
	}
	var r request
	for key, dst := range map[string]any{
		"model": &r.Model, "stream": &r.Stream, "temperature": &r.Temperature,
		"top_p": &r.TopP, "parallel_tool_calls": &r.Parallel,
	} {
		if err := unmarshalOptional(m, key, dst); err != nil {
			return r, err
		}
	}
	if len(m["max_completion_tokens"]) > 0 {
		err = unmarshalOptional(m, "max_completion_tokens", &r.MaxTokens)
	} else {
		err = unmarshalOptional(m, "max_tokens", &r.MaxTokens)
	}
	if err != nil {
		return r, err
	}
	if v := m["stop"]; len(v) > 0 {
		if json.Unmarshal(v, &r.Stops) != nil {
			var s string
			if json.Unmarshal(v, &s) == nil {
				r.Stops = []string{s}
			} else {
				return r, &UnsupportedError{Feature: "stop"}
			}
		}
	}
	var msgs []map[string]json.RawMessage
	if err = json.Unmarshal(m["messages"], &msgs); err != nil {
		return r, fmt.Errorf("messages: %w", err)
	}
	seenNonInstruction := false
	for _, x := range msgs {
		role, err := rawString(x["role"])
		if err != nil {
			return r, fmt.Errorf("message role: %w", err)
		}
		allowed := map[string]bool{"role": true, "content": true}
		switch role {
		case "assistant":
			allowed["tool_calls"] = true
			allowed["refusal"] = true
		case "tool":
			allowed["tool_call_id"] = true
		}
		if err := rejectUnknown(x, allowed); err != nil {
			return r, err
		}
		if role == "system" || role == "developer" {
			if seenNonInstruction {
				return r, &UnsupportedError{Feature: "interleaved system/developer instructions"}
			}
			p, e := textParts(x["content"], model.OpenAIChat)
			if e != nil {
				return r, e
			}
			r.Instructions = append(r.Instructions, p...)
			continue
		}
		seenNonInstruction = true
		p, e := textParts(x["content"], model.OpenAIChat)
		if e != nil {
			return r, e
		}
		if role == "tool" {
			id, err := rawString(x["tool_call_id"])
			if err != nil || id == "" {
				return r, fmt.Errorf("tool message requires string tool_call_id")
			}
			p = []part{{Kind: "tool_result", ID: id, JSON: x["content"]}}
		}
		if role == "assistant" {
			var calls []struct {
				ID, Type string
				Function struct{ Name, Arguments string }
			}
			var rawCalls []map[string]json.RawMessage
			if err := unmarshalOptional(x, "tool_calls", &rawCalls); err != nil {
				return r, err
			}
			for _, rawCall := range rawCalls {
				if err := rejectUnknown(rawCall, map[string]bool{"id": true, "type": true, "function": true}); err != nil {
					return r, err
				}
				var function map[string]json.RawMessage
				if err := json.Unmarshal(rawCall["function"], &function); err != nil {
					return r, fmt.Errorf("tool call function: %w", err)
				}
				if err := rejectUnknown(function, map[string]bool{"name": true, "arguments": true}); err != nil {
					return r, err
				}
			}
			if err := unmarshalOptional(x, "tool_calls", &calls); err != nil {
				return r, err
			}
			for _, c := range calls {
				if c.Type != "" && c.Type != "function" {
					return r, &UnsupportedError{Feature: "non-function tool call"}
				}
				if c.ID == "" || c.Function.Name == "" || !json.Valid([]byte(c.Function.Arguments)) {
					return r, fmt.Errorf("function tool call requires id, name, and valid JSON arguments")
				}
				p = append(p, part{Kind: "tool_call", ID: c.ID, Name: c.Function.Name, JSON: json.RawMessage(c.Function.Arguments)})
			}
			var refusal string
			if err := unmarshalOptional(x, "refusal", &refusal); err != nil {
				return r, err
			}
			if refusal != "" {
				p = append(p, part{Kind: "refusal", Text: refusal})
			}
		}
		if role != "user" && role != "assistant" && role != "tool" {
			return r, &UnsupportedError{Feature: "message role " + role}
		}
		r.Messages = append(r.Messages, message{Role: role, Parts: p})
	}
	var ts []struct {
		Type     string
		Function struct {
			Name, Description string
			Parameters        json.RawMessage
			Strict            *bool
		}
	}
	var rawTools []map[string]json.RawMessage
	if err := unmarshalOptional(m, "tools", &rawTools); err != nil {
		return r, err
	}
	for _, rawTool := range rawTools {
		if err := rejectUnknown(rawTool, map[string]bool{"type": true, "function": true}); err != nil {
			return r, err
		}
		var function map[string]json.RawMessage
		if err := json.Unmarshal(rawTool["function"], &function); err != nil {
			return r, fmt.Errorf("tool function: %w", err)
		}
		if err := rejectUnknown(function, map[string]bool{"name": true, "description": true, "parameters": true, "strict": true}); err != nil {
			return r, err
		}
	}
	if err := unmarshalOptional(m, "tools", &ts); err != nil {
		return r, err
	}
	for _, t := range ts {
		if t.Type != "function" {
			return r, &UnsupportedError{Feature: "non-function tool"}
		}
		if t.Function.Strict != nil && *t.Function.Strict {
			return r, &UnsupportedError{Feature: "strict function tool"}
		}
		r.Tools = append(r.Tools, tool{Name: t.Function.Name, Description: t.Function.Description, Schema: t.Function.Parameters})
	}
	r.ToolChoiceKind, r.ToolChoiceName, err = parseToolChoice(m["tool_choice"], model.OpenAIChat)
	if err != nil {
		return r, err
	}
	return r, nil
}

func renderChat(r request) (map[string]any, error) {
	out := map[string]any{"model": r.Model}
	msgs := []any{}
	if len(r.Instructions) > 0 {
		txt, err := onlyText(r.Instructions)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, map[string]any{"role": "developer", "content": txt})
	}
	for _, m := range r.Messages {
		switch m.Role {
		case "user", "assistant":
			content := []any{}
			calls := []any{}
			var refusal string
			for _, p := range m.Parts {
				switch p.Kind {
				case "text":
					content = append(content, map[string]any{"type": "text", "text": p.Text})
				case "image":
					if p.URL == "" {
						return nil, &UnsupportedError{Feature: "base64 image to Chat data URL"}
					}
					content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": p.URL}})
				case "tool_call":
					calls = append(calls, map[string]any{"id": p.ID, "type": "function", "function": map[string]any{"name": p.Name, "arguments": string(p.JSON)}})
				case "refusal":
					refusal = p.Text
				default:
					return nil, &UnsupportedError{Feature: p.Kind + " in " + m.Role}
				}
			}
			x := map[string]any{"role": m.Role}
			if len(content) > 0 {
				x["content"] = content
			}
			if len(calls) > 0 {
				x["tool_calls"] = calls
			}
			if refusal != "" {
				x["refusal"] = refusal
			}
			msgs = append(msgs, x)
		case "tool":
			for _, p := range m.Parts {
				if p.Kind != "tool_result" {
					return nil, &UnsupportedError{Feature: p.Kind}
				}
				msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": p.ID, "content": rawToString(p.JSON)})
			}
		default:
			return nil, &UnsupportedError{Feature: "role " + m.Role}
		}
	}
	out["messages"] = msgs
	if len(r.Tools) > 0 {
		a := []any{}
		for _, t := range r.Tools {
			a = append(a, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": rawOrEmpty(t.Schema)}})
		}
		out["tools"] = a
	}
	putCommon(out, r, "max_completion_tokens", model.OpenAIChat)
	return out, nil
}

func onlyText(ps []part) (string, error) {
	s := ""
	for _, p := range ps {
		if p.Kind != "text" {
			return "", &UnsupportedError{Feature: "non-text instructions"}
		}
		s += p.Text
	}
	return s, nil
}

func rawToString(v json.RawMessage) any {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return v
}

func rawOrEmpty(v json.RawMessage) any {
	if len(v) == 0 {
		return map[string]any{"type": "object"}
	}
	return v
}

func putCommon(out map[string]any, r request, max string, family model.Family) {
	if r.Stream {
		out["stream"] = true
	}
	if r.MaxTokens != nil {
		out[max] = *r.MaxTokens
	}
	if r.Temperature != nil {
		out["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		out["top_p"] = *r.TopP
	}
	if len(r.Stops) > 0 {
		if max == "max_tokens" {
			out["stop_sequences"] = r.Stops
		} else {
			out["stop"] = r.Stops
		}
	}
	if r.Parallel != nil {
		out["parallel_tool_calls"] = *r.Parallel
	}
	if choice := renderToolChoice(r.ToolChoiceKind, r.ToolChoiceName, family); choice != nil {
		out["tool_choice"] = choice
	}
}
