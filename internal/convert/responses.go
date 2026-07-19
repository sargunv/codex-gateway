package convert

import (
	"encoding/json"
	"fmt"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func parseResponses(raw []byte) (request, error) {
	m, err := decodeMap(raw, "model", "instructions", "input", "tools", "tool_choice", "stream", "max_output_tokens", "temperature", "top_p", "parallel_tool_calls")
	if err != nil {
		return request{}, err
	}
	var r request
	for key, dst := range map[string]any{
		"model": &r.Model, "stream": &r.Stream, "max_output_tokens": &r.MaxTokens,
		"temperature": &r.Temperature, "top_p": &r.TopP, "parallel_tool_calls": &r.Parallel,
	} {
		if err := unmarshalOptional(m, key, dst); err != nil {
			return r, err
		}
	}
	if len(m["instructions"]) > 0 {
		p, e := textParts(m["instructions"], model.OpenAIResponses)
		if e != nil {
			return r, e
		}
		r.Instructions = p
	}
	var inputString string
	if json.Unmarshal(m["input"], &inputString) == nil {
		r.Messages = []message{{Role: "user", Parts: []part{{Kind: "text", Text: inputString}}}}
	} else {
		var items []map[string]json.RawMessage
		if err = json.Unmarshal(m["input"], &items); err != nil {
			return r, fmt.Errorf("input: %w", err)
		}
		for _, x := range items {
			typ, err := rawString(x["type"])
			if err != nil && len(x["type"]) > 0 {
				return r, fmt.Errorf("input item type: %w", err)
			}
			switch typ {
			case "message", "":
				if err := rejectUnknown(x, map[string]bool{"type": true, "role": true, "content": true}); err != nil {
					return r, err
				}
				role, err := rawString(x["role"])
				if err != nil {
					return r, err
				}
				p, e := textParts(x["content"], model.OpenAIResponses)
				if e != nil {
					return r, e
				}
				r.Messages = append(r.Messages, message{Role: role, Parts: p})
			case "function_call":
				if err := rejectUnknown(x, map[string]bool{"type": true, "call_id": true, "name": true, "arguments": true}); err != nil {
					return r, err
				}
				id, err := rawString(x["call_id"])
				if err != nil {
					return r, err
				}
				name, err := rawString(x["name"])
				if err != nil {
					return r, err
				}
				argstr, err := rawString(x["arguments"])
				if err != nil || !json.Valid([]byte(argstr)) {
					return r, fmt.Errorf("function call arguments must be a string containing valid JSON")
				}
				r.Messages = append(r.Messages, message{Role: "assistant", Parts: []part{{Kind: "tool_call", ID: id, Name: name, JSON: json.RawMessage(argstr)}}})
			case "function_call_output":
				if err := rejectUnknown(x, map[string]bool{"type": true, "call_id": true, "output": true}); err != nil {
					return r, err
				}
				id, err := rawString(x["call_id"])
				if err != nil {
					return r, err
				}
				r.Messages = append(r.Messages, message{Role: "tool", Parts: []part{{Kind: "tool_result", ID: id, JSON: x["output"]}}})
			case "reasoning":
				return r, &UnsupportedError{Feature: "Responses reasoning item"}
			default:
				return r, &UnsupportedError{Feature: "Responses input item " + typ}
			}
		}
	}
	var tools []map[string]json.RawMessage
	if err := unmarshalOptional(m, "tools", &tools); err != nil {
		return r, err
	}
	for _, x := range tools {
		if err := rejectUnknown(x, map[string]bool{"type": true, "name": true, "description": true, "parameters": true, "strict": true}); err != nil {
			return r, err
		}
		typ, err := rawString(x["type"])
		if err != nil {
			return r, err
		}
		if typ != "function" {
			return r, &UnsupportedError{Feature: "Responses tool " + typ}
		}
		var strict bool
		if err := unmarshalOptional(x, "strict", &strict); err != nil {
			return r, err
		}
		if strict {
			return r, &UnsupportedError{Feature: "strict function tool"}
		}
		name, err := rawString(x["name"])
		if err != nil {
			return r, err
		}
		var desc string
		if err := unmarshalOptional(x, "description", &desc); err != nil {
			return r, err
		}
		r.Tools = append(r.Tools, tool{Name: name, Description: desc, Schema: x["parameters"]})
	}
	r.ToolChoiceKind, r.ToolChoiceName, err = parseToolChoice(m["tool_choice"], model.OpenAIResponses)
	if err != nil {
		return r, err
	}
	return r, nil
}

func renderResponses(r request) (map[string]any, error) {
	out := map[string]any{"model": r.Model}
	if len(r.Instructions) > 0 {
		s, e := onlyText(r.Instructions)
		if e != nil {
			return nil, e
		}
		out["instructions"] = s
	}
	items := []any{}
	for _, m := range r.Messages {
		switch m.Role {
		case "user", "assistant":
			blocks := []any{}
			for _, p := range m.Parts {
				switch p.Kind {
				case "text":
					typ := "input_text"
					if m.Role == "assistant" {
						typ = "output_text"
					}
					blocks = append(blocks, map[string]any{"type": typ, "text": p.Text})
				case "image":
					if m.Role != "user" || p.URL == "" {
						return nil, &UnsupportedError{Feature: "image form"}
					}
					blocks = append(blocks, map[string]any{"type": "input_image", "image_url": p.URL})
				case "tool_call":
					items = append(items, map[string]any{"type": "function_call", "call_id": p.ID, "name": p.Name, "arguments": string(p.JSON)})
				case "refusal":
					blocks = append(blocks, map[string]any{"type": "refusal", "refusal": p.Text})
				default:
					return nil, &UnsupportedError{Feature: p.Kind}
				}
			}
			if len(blocks) > 0 {
				items = append(items, map[string]any{"type": "message", "role": m.Role, "content": blocks})
			}
		case "tool":
			for _, p := range m.Parts {
				items = append(items, map[string]any{"type": "function_call_output", "call_id": p.ID, "output": rawToString(p.JSON)})
			}
		default:
			return nil, &UnsupportedError{Feature: "role " + m.Role}
		}
	}
	out["input"] = items
	if len(r.Tools) > 0 {
		a := []any{}
		for _, t := range r.Tools {
			a = append(a, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": rawOrEmpty(t.Schema)})
		}
		out["tools"] = a
	}
	if len(r.Stops) > 0 {
		return nil, &UnsupportedError{Feature: "stop sequences to Responses"}
	}
	putCommon(out, r, "max_output_tokens", model.OpenAIResponses)
	return out, nil
}
