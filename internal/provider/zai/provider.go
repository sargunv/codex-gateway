package zai

import (
	"net/url"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func New(key string) *model.Account {
	chat, _ := url.Parse("https://api.z.ai/api/coding/paas/v4")
	metadata, _ := url.Parse("https://models.dev/api.json")
	c := model.StaticCredential{Value: key, Header: "Authorization", Prefix: "Bearer ", Kind: "env"}
	return &model.Account{ID: "zai", CredentialSource: "env", Endpoints: map[string]*model.Endpoint{"chat": {ID: "chat", Family: model.OpenAIChat, BaseURL: chat, Credential: c}}, Models: map[string]*model.Model{}, Catalogs: []model.CatalogSource{{URL: metadata, Format: "models.dev", ProviderID: "zai-coding-plan", NPMRoutes: map[string]string{"@ai-sdk/openai-compatible": "chat"}}}}
}
