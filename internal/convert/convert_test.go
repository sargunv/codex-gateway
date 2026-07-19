package convert

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func TestChatToResponsesPreservesTools(t *testing.T) {
	in := []byte(`{"model":"p/m","messages":[{"role":"developer","content":"be terse"},{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{\"x\":1}"}}]},{"role":"tool","tool_call_id":"call_1","content":"ok"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}],"max_completion_tokens":12}`)
	out, _, err := Request(in, model.OpenAIChat, model.OpenAIResponses, "up")
	if err != nil {
		t.Fatal(err)
	}
	var x map[string]any
	if json.Unmarshal(out, &x) != nil || x["model"] != "up" {
		t.Fatalf("%s", out)
	}
	b, _ := json.Marshal(x["input"])
	if !json.Valid(b) {
		t.Fatal("invalid")
	}
}

func TestUnsupportedNeverSilentlyDropped(t *testing.T) {
	_, _, err := Request([]byte(`{"model":"p/m","messages":[],"audio":{"voice":"x"}}`), model.OpenAIChat, model.OpenAIResponses, "up")
	var u *UnsupportedError
	if !errors.As(err, &u) {
		t.Fatalf("%v", err)
	}
}

func TestThinkingSignatureRejected(t *testing.T) {
	_, _, err := Request([]byte(`{"model":"p/m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"x","signature":"sig"}]}]}`), model.AnthropicMessages, model.OpenAIChat, "up")
	if err == nil {
		t.Fatal("expected rejection")
	}
}

func TestMalformedAndNestedSemanticFieldsReject(t *testing.T) {
	for _, in := range []string{
		`{"model":"p/m","messages":[],"temperature":"hot"}`,
		`{"model":"p/m","messages":[],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"},"strict":true}}]}`,
		`{"model":"p/m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/x","detail":"high"}}]}]}`,
		`{"model":"p/m","messages":[{"role":"tool","tool_call_id":"x","content":"bad","is_error":true}]}`,
	} {
		if _, _, err := Request([]byte(in), model.OpenAIChat, model.OpenAIResponses, "up"); err == nil {
			t.Fatalf("expected rejection for %s", in)
		}
	}
}

func TestProviderResponseExtensionsRejectCrossFamily(t *testing.T) {
	_, err := Response([]byte(`{"id":"x","model":"m","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1},"energy":7}`), model.OpenAIChat, model.OpenAIResponses)
	if err == nil {
		t.Fatal("expected provider-only response extension rejection")
	}
}

func TestReviewerNestedReprosReject(t *testing.T) {
	requests := []struct {
		body     string
		from, to model.Family
	}{
		{`{"model":"p/m","messages":[{"role":"tool","tool_call_id":7,"content":"result"}]}`, model.OpenAIChat, model.OpenAIResponses},
		{`{"model":"p/m","messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","future":true,"function":{"name":"f","arguments":"{}"}}]}]}`, model.OpenAIChat, model.OpenAIResponses},
		{`{"model":"p/m","max_tokens":8,"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"auto","disable_parallel_tool_use":true}}`, model.AnthropicMessages, model.OpenAIChat},
		{`{"model":"p/m","input":[{"type":"function_call","call_id":"c","name":"f","arguments":7}]}`, model.OpenAIResponses, model.OpenAIChat},
	}
	for _, tc := range requests {
		if _, _, err := Request([]byte(tc.body), tc.from, tc.to, "up"); err == nil {
			t.Fatalf("expected nested request rejection for %s", tc.body)
		}
	}

	chatWithAudio := []byte(`{"id":"x","model":"m","choices":[{"message":{"role":"assistant","content":"ok","audio":{"id":"a"}},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	if _, err := Response(chatWithAudio, model.OpenAIChat, model.OpenAIResponses); err == nil {
		t.Fatal("expected nested Chat response audio rejection")
	}
}
