package convert

import (
	"encoding/json"
	"fmt"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func parseMessages(raw []byte) (request, error) {
	m, err := decodeMap(raw, "model", "max_tokens", "system", "messages", "tools", "tool_choice", "stream", "temperature", "top_p", "stop_sequences")
	if err != nil {
		return request{}, err
	}
	var r request
	for key, dst := range map[string]any{
		"model": &r.Model, "stream": &r.Stream, "max_tokens": &r.MaxTokens,
		"temperature": &r.Temperature, "top_p": &r.TopP, "stop_sequences": &r.Stops,
	} {
		if err := unmarshalOptional(m, key, dst); err != nil {
			return r, err
		}
	}
	if r.MaxTokens == nil || *r.MaxTokens <= 0 {
		return r, fmt.Errorf("max_tokens must be a positive integer")
	}
	if len(m["system"]) > 0 {
		p, e := textParts(m["system"], model.AnthropicMessages)
		if e != nil {
			return r, e
		}
		r.Instructions = p
	}
	var msgs []map[string]json.RawMessage
	if err = json.Unmarshal(m["messages"], &msgs); err != nil {
		return r, fmt.Errorf("messages: %w", err)
	}
	for _, x := range msgs {
		if err := rejectUnknown(x, map[string]bool{"role": true, "content": true}); err != nil {
			return r, err
		}
		role, err := rawString(x["role"])
		if err != nil {
			return r, fmt.Errorf("message role: %w", err)
		}
		if role != "user" && role != "assistant" {
			return r, &UnsupportedError{Feature: "Messages role " + role}
		}
		p, e := textParts(x["content"], model.AnthropicMessages)
		if e != nil {
			return r, e
		}
		segment := []part{}
		flush := func() {
			if len(segment) > 0 {
				r.Messages = append(r.Messages, message{Role: role, Parts: segment})
				segment = nil
			}
		}
		for _, q := range p {
			if q.Kind == "tool_result" {
				if role != "user" {
					return r, &UnsupportedError{Feature: "tool_result in assistant message"}
				}
				flush()
				r.Messages = append(r.Messages, message{Role: "tool", Parts: []part{q}})
			} else {
				segment = append(segment, q)
			}
		}
		flush()
	}
	var ts []struct {
		Name, Description string
		InputSchema       json.RawMessage `json:"input_schema"`
	}
	var rawTools []map[string]json.RawMessage
	if err := unmarshalOptional(m, "tools", &rawTools); err != nil {
		return r, err
	}
	for _, rawTool := range rawTools {
		if err := rejectUnknown(rawTool, map[string]bool{"name": true, "description": true, "input_schema": true}); err != nil {
			return r, err
		}
	}
	if err := unmarshalOptional(m, "tools", &ts); err != nil {
		return r, err
	}
	for _, t := range ts {
		r.Tools = append(r.Tools, tool{Name: t.Name, Description: t.Description, Schema: t.InputSchema})
	}
	r.ToolChoiceKind, r.ToolChoiceName, err = parseToolChoice(m["tool_choice"], model.AnthropicMessages)
	if err != nil {
		return r, err
	}
	return r, nil
}

func renderMessages(r request) (map[string]any, error) {
	if r.Parallel != nil {
		return nil, &UnsupportedError{Feature: "parallel_tool_calls to Messages"}
	}
	out := map[string]any{"model": r.Model}
	if r.MaxTokens == nil {
		return nil, &UnsupportedError{Feature: "required Anthropic max_tokens"}
	}
	out["max_tokens"] = *r.MaxTokens
	if len(r.Instructions) > 0 {
		s, e := onlyText(r.Instructions)
		if e != nil {
			return nil, e
		}
		out["system"] = s
	}
	msgs := []any{}
	for _, m := range r.Messages {
		role := m.Role
		if role == "tool" {
			role = "user"
		}
		blocks := []any{}
		for _, p := range m.Parts {
			switch p.Kind {
			case "text":
				blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
			case "image":
				if p.URL != "" {
					blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": p.URL}})
				} else {
					var data string
					_ = json.Unmarshal(p.JSON, &data)
					blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": p.MediaType, "data": data}})
				}
			case "tool_call":
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": p.ID, "name": p.Name, "input": rawOrEmpty(p.JSON)})
			case "tool_result":
				blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": p.ID, "content": rawToString(p.JSON)})
			case "refusal":
				blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
			default:
				return nil, &UnsupportedError{Feature: p.Kind}
			}
		}
		msgs = append(msgs, map[string]any{"role": role, "content": blocks})
	}
	out["messages"] = msgs
	if len(r.Tools) > 0 {
		a := []any{}
		for _, t := range r.Tools {
			a = append(a, map[string]any{"name": t.Name, "description": t.Description, "input_schema": rawOrEmpty(t.Schema)})
		}
		out["tools"] = a
	}
	if r.Stream {
		out["stream"] = true
	}
	if r.Temperature != nil {
		out["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		out["top_p"] = *r.TopP
	}
	if len(r.Stops) > 0 {
		out["stop_sequences"] = r.Stops
	}
	if choice := renderToolChoice(r.ToolChoiceKind, r.ToolChoiceName, model.AnthropicMessages); choice != nil {
		out["tool_choice"] = choice
	}
	return out, nil
}
