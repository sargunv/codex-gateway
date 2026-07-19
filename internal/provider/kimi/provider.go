package kimi

import (
	"net/url"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func New(cred model.Credential) *model.Account {
	u, _ := url.Parse("https://api.kimi.com/coding/v1")
	catalog, _ := url.Parse("https://models.dev/api.json")
	return &model.Account{ID: "kimi", CredentialSource: "file", Endpoints: map[string]*model.Endpoint{"messages": {ID: "messages", Family: model.AnthropicMessages, BaseURL: u, Credential: cred}}, Models: map[string]*model.Model{}, Catalogs: []model.CatalogSource{{URL: catalog, Format: "models.dev", ProviderID: "kimi-for-coding", NPMRoutes: map[string]string{"@ai-sdk/anthropic": "messages"}}}}
}
