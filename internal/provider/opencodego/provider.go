package opencodego

import (
	"net/url"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func New(key string) *model.Account {
	u, _ := url.Parse("https://opencode.ai/zen/go/v1")
	catalog, _ := url.Parse("https://models.dev/api.json")
	c := model.StaticCredential{Value: key, Header: "Authorization", Prefix: "Bearer ", Kind: "env"}
	// OpenCode maps @ai-sdk/openai to Responses and @ai-sdk/openai-compatible
	// to Chat Completions; model-level npm overrides support mixed-family catalogs.
	// https://github.com/anomalyco/opencode/blob/70b56a0a93d366889cae950379cc9d2537148fa2/packages/opencode/src/session/llm/native-request.ts#L153-L178
	return &model.Account{ID: "opencode-go", CredentialSource: "env", Endpoints: map[string]*model.Endpoint{"chat": {ID: "chat", Family: model.OpenAIChat, BaseURL: u, Credential: c, Headers: map[string]string{"User-Agent": "agent-api-gateway"}}, "responses": {ID: "responses", Family: model.OpenAIResponses, BaseURL: u, Credential: c, Headers: map[string]string{"User-Agent": "agent-api-gateway"}}, "messages": {ID: "messages", Family: model.AnthropicMessages, BaseURL: u, Credential: c, Headers: map[string]string{"User-Agent": "agent-api-gateway"}}}, Models: map[string]*model.Model{}, Catalogs: []model.CatalogSource{{URL: catalog, Format: "models.dev", ProviderID: "opencode-go", NPMRoutes: map[string]string{"@ai-sdk/openai-compatible": "chat", "@ai-sdk/openai": "responses", "@ai-sdk/anthropic": "messages"}}}}
}
