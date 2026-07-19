// Package responses contains the supported OpenAI Responses conversion subset.
package responses

import "encoding/json"

type Request struct {
	Model             string            `json:"model"`
	Instructions      json.RawMessage   `json:"instructions,omitempty"`
	Input             json.RawMessage   `json:"input"`
	Tools             []json.RawMessage `json:"tools,omitempty"`
	ToolChoice        json.RawMessage   `json:"tool_choice,omitempty"`
	Stream            bool              `json:"stream,omitempty"`
	MaxOutputTokens   *int              `json:"max_output_tokens,omitempty"`
	Temperature       *float64          `json:"temperature,omitempty"`
	TopP              *float64          `json:"top_p,omitempty"`
	ParallelToolCalls *bool             `json:"parallel_tool_calls,omitempty"`
}
type Item struct {
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	ID        string          `json:"id,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
}
