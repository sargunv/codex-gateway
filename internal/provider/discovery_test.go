package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func TestDiscoverAddsCompleteClassifiedModelAndPassesMetadata(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"id": "new-model", "context_window": 128000, "max_output_tokens": 8192,
			"supports_tools": true, "supports_reasoning": false, "input_modalities": []string{"text", "image"}, "vendor_extension": map[string]any{"tier": "fast"}, "base_instructions": "must not be proxied",
		}}})
	}))
	defer server.Close()

	account := testDiscoveryAccount(t, server.URL, "chat")
	warnings := Discover(context.Background(), []*model.Account{account}, server.Client())
	if hasWarning(warnings, "new-model", "metadata_incomplete") {
		t.Fatalf("unexpected incomplete warning: %#v", warnings)
	}
	if authorization != "Bearer secret" {
		t.Fatalf("catalog auth = %q", authorization)
	}
	entry := account.Models["new-model"]
	if entry == nil || len(entry.Routes) != 1 || entry.Routes[0].EndpointID != "chat" || !entry.Routes[0].Preferred {
		t.Fatalf("new model route = %#v", entry)
	}
	if entry.Metadata["context_window"] != int64(128000) || entry.Metadata["supports_tools"] != true {
		t.Fatalf("normalized metadata = %#v", entry.Metadata)
	}
	catalog, err := model.NewCatalog([]*model.Account{account})
	if err != nil {
		t.Fatal(err)
	}
	var listed map[string]any
	for _, item := range catalog.List() {
		if item["id"] == "test/new-model" {
			listed = item
		}
	}
	if listed == nil || listed["vendor_extension"] == nil || listed["preferred_endpoint_family"] != "openai-chat" || listed["base_instructions"] != nil || listed["type"] != "model" {
		t.Fatalf("listed metadata = %#v", listed)
	}
}

func TestDiscoverCatalogOverridesConfiguredMetadataWithWarning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"id":"configured","context_window":256000}]}`))
	}))
	defer server.Close()

	account := testDiscoveryAccount(t, server.URL, "chat")
	account.Models["configured"] = &model.Model{ID: "configured", UpstreamID: "configured", Routes: []model.Route{{EndpointID: "chat", Preferred: true}}, Metadata: map[string]any{"context_window": int64(128000)}}
	warnings := Discover(context.Background(), []*model.Account{account}, server.Client())
	if !hasWarning(warnings, "configured", "metadata_conflict") || !hasWarning(warnings, "configured", "metadata_incomplete") {
		t.Fatalf("warnings = %#v", warnings)
	}
	if account.Models["configured"].Metadata["context_window"] != int64(256000) {
		t.Fatalf("catalog metadata did not win: %#v", account.Models["configured"].Metadata)
	}
}

func TestDiscoverWithholdsUnclassifiedModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"ambiguous"}]}`))
	}))
	defer server.Close()

	account := testDiscoveryAccount(t, server.URL, "")
	warnings := Discover(context.Background(), []*model.Account{account}, server.Client())
	if account.Models["ambiguous"] != nil || !hasWarning(warnings, "ambiguous", "route_unclassified") {
		t.Fatalf("models=%#v warnings=%#v", account.Models, warnings)
	}
}

func TestModelsDevIsSharedCredentialFreeCatalog(t *testing.T) {
	var authorization atomic.Value
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		authorization.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"fixture":{"npm":"@sdk/chat","models":{"model":{"limit":{"context":200000,"output":10000},"tool_call":true,"reasoning":false,"modalities":{"input":["text"],"output":["text"]}}}},"fixture2":{"npm":"@sdk/chat","models":{"model":{"limit":{"context":200000,"output":10000},"tool_call":true,"reasoning":false,"modalities":{"input":["text"],"output":["text"]}}}}}`))
	}))
	defer server.Close()

	account := testDiscoveryAccount(t, server.URL, "")
	account.Catalogs = []model.CatalogSource{{URL: account.Catalogs[0].URL, Format: "models.dev", ProviderID: "fixture", NPMRoutes: map[string]string{"@sdk/chat": "chat"}}}
	account2 := testDiscoveryAccount(t, server.URL, "")
	account2.ID = "test2"
	account2.Catalogs = []model.CatalogSource{{URL: account2.Catalogs[0].URL, Format: "models.dev", ProviderID: "fixture2", NPMRoutes: map[string]string{"@sdk/chat": "chat"}}}
	warnings := Discover(context.Background(), []*model.Account{account, account2}, server.Client())
	if got, _ := authorization.Load().(string); got != "" {
		t.Fatalf("credential leaked to public catalog: %q", got)
	}
	if requests.Load() != 1 {
		t.Fatalf("public catalog requests = %d, want 1", requests.Load())
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	entry := account.Models["model"]
	if entry == nil || entry.Routes[0].EndpointID != "chat" || entry.Metadata["context_window"] != int64(200000) {
		t.Fatalf("model = %#v", entry)
	}
}

func TestModelsDevRouteReplacesConfiguredRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"fixture":{"npm":"@sdk/messages","models":{"model":{"limit":{"context":1000,"output":100},"tool_call":true,"reasoning":false,"modalities":{"input":["text"],"output":["text"]}}}}}`))
	}))
	defer server.Close()

	account := testDiscoveryAccount(t, server.URL, "")
	account.Endpoints["messages"] = &model.Endpoint{ID: "messages", Family: model.AnthropicMessages, BaseURL: account.Endpoints["chat"].BaseURL, Credential: account.Endpoints["chat"].Credential}
	account.Models["model"] = &model.Model{ID: "model", UpstreamID: "model", Routes: []model.Route{{EndpointID: "chat", Preferred: true}}}
	account.Catalogs = []model.CatalogSource{{URL: account.Catalogs[0].URL, Format: "models.dev", ProviderID: "fixture", NPMRoutes: map[string]string{"@sdk/messages": "messages"}}}
	warnings := Discover(context.Background(), []*model.Account{account}, server.Client())
	if account.Models["model"].Routes[0].EndpointID != "messages" || !hasWarning(warnings, "model", "route_conflict") {
		t.Fatalf("model=%#v warnings=%#v", account.Models["model"], warnings)
	}
}

func TestCatalogFailureDiscoversNoModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("sensitive upstream body"))
	}))
	defer server.Close()

	account := testDiscoveryAccount(t, server.URL, "chat")
	warnings := Discover(context.Background(), []*model.Account{account}, server.Client())
	if len(account.Models) != 0 || !hasWarning(warnings, "", "catalog_unavailable") {
		t.Fatalf("models=%#v warnings=%#v", account.Models, warnings)
	}
	for _, warning := range warnings {
		if warning.Detail == "sensitive upstream body" {
			t.Fatal("upstream response body leaked into warning")
		}
	}
}

func testDiscoveryAccount(t *testing.T, catalogURL, routeEndpoint string) *model.Account {
	t.Helper()
	u, err := url.Parse(catalogURL)
	if err != nil {
		t.Fatal(err)
	}
	credential := model.StaticCredential{Value: "secret", Header: "Authorization", Prefix: "Bearer ", Kind: "test"}
	return &model.Account{
		ID:        "test",
		Endpoints: map[string]*model.Endpoint{"chat": {ID: "chat", Family: model.OpenAIChat, BaseURL: u, Credential: credential}},
		Models:    map[string]*model.Model{},
		Catalogs:  []model.CatalogSource{{URL: u, EndpointID: routeEndpoint, AuthEndpointID: "chat"}},
	}
}

func hasWarning(warnings []DiscoveryWarning, modelID, kind string) bool {
	for _, warning := range warnings {
		if warning.Model == modelID && warning.Kind == kind {
			return true
		}
	}
	return false
}
