package codex

import (
	"net/url"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func New(cred model.Credential) *model.Account {
	u, _ := url.Parse("https://chatgpt.com/backend-api/codex")
	catalog, _ := url.Parse("https://chatgpt.com/backend-api/codex/models?client_version=0.143.0")
	return &model.Account{ID: "codex", CredentialSource: "file", Endpoints: map[string]*model.Endpoint{"responses": {ID: "responses", Family: model.OpenAIResponses, BaseURL: u, Credential: cred, Headers: map[string]string{"Origin": "https://chatgpt.com"}}}, Models: map[string]*model.Model{}, Catalogs: []model.CatalogSource{{URL: catalog, EndpointID: "responses", AuthEndpointID: "responses"}}}
}
