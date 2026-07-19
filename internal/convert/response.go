package convert

import (
	"encoding/json"
	"fmt"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

type response struct {
	ID, Model, Text, Stop     string
	ToolCalls                 []part
	InputTokens, OutputTokens int
}

func Response(raw []byte, from, to model.Family) ([]byte, error) {
	if from == to {
		return raw, nil
	}
	var r response
	var err error
	switch from {
	case model.OpenAIChat:
		r, err = parseChatResponse(raw)
	case model.OpenAIResponses:
		r, err = parseResponsesResponse(raw)
	case model.AnthropicMessages:
		r, err = parseMessagesResponse(raw)
	}
	if err != nil {
		return nil, err
	}
	var x any
	switch to {
	case model.OpenAIChat:
		x = renderChatResponse(r)
	case model.OpenAIResponses:
		x = renderResponsesResponse(r)
	case model.AnthropicMessages:
		x = renderMessagesResponse(r)
	default:
		return nil, fmt.Errorf("unsupported destination")
	}
	return json.Marshal(x)
}

func parseChatResponse(b []byte) (response, error) {
	top, err := decodeMap(b, "id", "object", "created", "model", "choices", "usage", "system_fingerprint", "service_tier")
	if err != nil {
		return response{}, err
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(top["choices"], &choices); err != nil {
		return response{}, err
	}
	for _, choice := range choices {
		if err := rejectUnknown(choice, map[string]bool{"index": true, "message": true, "finish_reason": true}); err != nil {
			return response{}, err
		}
		var message map[string]json.RawMessage
		if err := json.Unmarshal(choice["message"], &message); err != nil {
			return response{}, err
		}
		if err := rejectUnknown(message, map[string]bool{"role": true, "content": true, "refusal": true, "tool_calls": true}); err != nil {
			return response{}, err
		}
		var calls []map[string]json.RawMessage
		if err := unmarshalOptional(message, "tool_calls", &calls); err != nil {
			return response{}, err
		}
		for _, call := range calls {
			if err := rejectUnknown(call, map[string]bool{"id": true, "type": true, "function": true}); err != nil {
				return response{}, err
			}
			var function map[string]json.RawMessage
			if err := json.Unmarshal(call["function"], &function); err != nil {
				return response{}, err
			}
			if err := rejectUnknown(function, map[string]bool{"name": true, "arguments": true}); err != nil {
				return response{}, err
			}
		}
	}
	var usage map[string]json.RawMessage
	if err := unmarshalOptional(top, "usage", &usage); err != nil {
		return response{}, err
	}
	if err := rejectUnknown(usage, map[string]bool{"prompt_tokens": true, "completion_tokens": true, "total_tokens": true}); err != nil {
		return response{}, err
	}
	var x struct {
		ID, Model string
		Choices   []struct {
			Message struct {
				Content, Refusal string
				ToolCalls        []struct {
					ID       string
					Function struct{ Name, Arguments string }
				}
			}
			FinishReason string `json:"finish_reason"`
		}
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
		}
	}
	if err := json.Unmarshal(b, &x); err != nil {
		return response{}, err
	}
	if len(x.Choices) != 1 {
		return response{}, &UnsupportedError{Feature: "zero/multiple Chat choices"}
	}
	c := x.Choices[0]
	r := response{ID: x.ID, Model: x.Model, Text: c.Message.Content, Stop: c.FinishReason, InputTokens: x.Usage.Prompt, OutputTokens: x.Usage.Completion}
	if c.Message.Refusal != "" {
		r.Text = c.Message.Refusal
	}
	for _, t := range c.Message.ToolCalls {
		if !json.Valid([]byte(t.Function.Arguments)) {
			return response{}, fmt.Errorf("tool call arguments are not valid JSON")
		}
		r.ToolCalls = append(r.ToolCalls, part{Kind: "tool_call", ID: t.ID, Name: t.Function.Name, JSON: json.RawMessage(t.Function.Arguments)})
	}
	return r, nil
}

func parseResponsesResponse(b []byte) (response, error) {
	top, err := decodeMap(b,
		"id", "object", "created_at", "status", "model", "output", "usage",
		"background", "instructions", "max_output_tokens", "parallel_tool_calls",
		"previous_response_id", "prompt_cache_key", "prompt_cache_retention",
		"service_tier", "store", "temperature", "tool_choice", "tools", "top_p",
		"truncation", "metadata",
	)
	if err != nil {
		return response{}, err
	}
	var usage map[string]json.RawMessage
	if err := unmarshalOptional(top, "usage", &usage); err != nil {
		return response{}, err
	}
	if err := rejectUnknown(usage, map[string]bool{"input_tokens": true, "output_tokens": true, "total_tokens": true}); err != nil {
		return response{}, err
	}
	var x struct {
		ID, Model, Status string
		Output            []map[string]json.RawMessage
		Usage             struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		}
	}
	if err := json.Unmarshal(b, &x); err != nil {
		return response{}, err
	}
	r := response{ID: x.ID, Model: x.Model, Stop: x.Status, InputTokens: x.Usage.Input, OutputTokens: x.Usage.Output}
	for _, it := range x.Output {
		typ, err := rawString(it["type"])
		if err != nil {
			return r, err
		}
		switch typ {
		case "message":
			if err := rejectUnknown(it, map[string]bool{"type": true, "id": true, "status": true, "role": true, "content": true}); err != nil {
				return r, err
			}
			ps, e := textParts(it["content"], model.OpenAIResponses)
			if e != nil {
				return r, e
			}
			for _, p := range ps {
				if p.Kind == "text" || p.Kind == "refusal" {
					r.Text += p.Text
				}
			}
		case "function_call":
			if err := rejectUnknown(it, map[string]bool{"type": true, "id": true, "status": true, "call_id": true, "name": true, "arguments": true}); err != nil {
				return r, err
			}
			id, err := rawString(it["call_id"])
			if err != nil {
				return r, err
			}
			name, err := rawString(it["name"])
			if err != nil {
				return r, err
			}
			var args string
			if err := json.Unmarshal(it["arguments"], &args); err != nil {
				return r, err
			}
			if !json.Valid([]byte(args)) {
				return r, fmt.Errorf("function call arguments are not valid JSON")
			}
			r.ToolCalls = append(r.ToolCalls, part{Kind: "tool_call", ID: id, Name: name, JSON: json.RawMessage(args)})
		case "reasoning":
			return r, &UnsupportedError{Feature: "Responses reasoning output"}
		default:
			return r, &UnsupportedError{Feature: "Responses output " + typ}
		}
	}
	return r, nil
}

func parseMessagesResponse(b []byte) (response, error) {
	top, err := decodeMap(b, "id", "type", "role", "model", "content", "stop_reason", "usage")
	if err != nil {
		return response{}, err
	}
	var usage map[string]json.RawMessage
	if err := unmarshalOptional(top, "usage", &usage); err != nil {
		return response{}, err
	}
	if err := rejectUnknown(usage, map[string]bool{"input_tokens": true, "output_tokens": true}); err != nil {
		return response{}, err
	}
	var x struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Stop    string          `json:"stop_reason"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(b, &x); err != nil {
		return response{}, err
	}
	ps, e := textParts(x.Content, model.AnthropicMessages)
	if e != nil {
		return response{}, e
	}
	r := response{ID: x.ID, Model: x.Model, Stop: x.Stop, InputTokens: x.Usage.Input, OutputTokens: x.Usage.Output}
	for _, p := range ps {
		switch p.Kind {
		case "text":
			r.Text += p.Text
		case "tool_call":
			r.ToolCalls = append(r.ToolCalls, p)
		}
	}
	return r, nil
}

func renderChatResponse(r response) any {
	msg := map[string]any{"role": "assistant", "content": r.Text}
	if len(r.ToolCalls) > 0 {
		a := []any{}
		for _, p := range r.ToolCalls {
			a = append(a, map[string]any{"id": p.ID, "type": "function", "function": map[string]any{"name": p.Name, "arguments": string(p.JSON)}})
		}
		msg["tool_calls"] = a
	}
	return map[string]any{"id": r.ID, "object": "chat.completion", "model": r.Model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": mapStopChat(r.Stop)}}, "usage": map[string]any{"prompt_tokens": r.InputTokens, "completion_tokens": r.OutputTokens, "total_tokens": r.InputTokens + r.OutputTokens}}
}

func renderResponsesResponse(r response) any {
	content := []any{}
	if r.Text != "" {
		content = append(content, map[string]any{"type": "output_text", "text": r.Text, "annotations": []any{}})
	}
	out := []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": content}}
	for _, p := range r.ToolCalls {
		out = append(out, map[string]any{"type": "function_call", "call_id": p.ID, "name": p.Name, "arguments": string(p.JSON), "status": "completed"})
	}
	return map[string]any{"id": r.ID, "object": "response", "status": "completed", "model": r.Model, "output": out, "usage": map[string]any{"input_tokens": r.InputTokens, "output_tokens": r.OutputTokens, "total_tokens": r.InputTokens + r.OutputTokens}}
}

func renderMessagesResponse(r response) any {
	c := []any{}
	if r.Text != "" {
		c = append(c, map[string]any{"type": "text", "text": r.Text})
	}
	for _, p := range r.ToolCalls {
		c = append(c, map[string]any{"type": "tool_use", "id": p.ID, "name": p.Name, "input": rawOrEmpty(p.JSON)})
	}
	return map[string]any{"id": r.ID, "type": "message", "role": "assistant", "model": r.Model, "content": c, "stop_reason": mapStopAnthropic(r.Stop), "usage": map[string]any{"input_tokens": r.InputTokens, "output_tokens": r.OutputTokens}}
}

func mapStopChat(s string) string {
	switch s {
	case "end_turn", "stop", "completed":
		return "stop"
	case "tool_use", "tool_calls":
		return "tool_calls"
	case "max_tokens", "length":
		return "length"
	}
	return s
}

func mapStopAnthropic(s string) string {
	switch s {
	case "stop", "completed":
		return "end_turn"
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	}
	return s
}
