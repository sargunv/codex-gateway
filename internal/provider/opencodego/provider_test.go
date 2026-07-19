package opencodego

import (
	"testing"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func TestCatalogDefinesRoutesWithoutStaticModels(t *testing.T) {
	a := New("test-key")
	if len(a.Models) != 0 {
		t.Fatalf("constructor models = %d, want 0", len(a.Models))
	}
	if len(a.Catalogs) != 1 {
		t.Fatalf("catalogs = %d, want 1", len(a.Catalogs))
	}
	source := a.Catalogs[0]
	if source.Format != "models.dev" || source.ProviderID != "opencode-go" {
		t.Fatalf("unexpected catalog: %#v", source)
	}
	if source.NPMRoutes["@ai-sdk/openai-compatible"] != "chat" || source.NPMRoutes["@ai-sdk/openai"] != "responses" || source.NPMRoutes["@ai-sdk/anthropic"] != "messages" {
		t.Fatalf("unexpected npm routes: %#v", source.NPMRoutes)
	}
	if a.Endpoints["responses"].Family != model.OpenAIResponses {
		t.Fatalf("responses endpoint family = %q", a.Endpoints["responses"].Family)
	}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
}
