package neuralwatt

import (
	"net/url"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

func New(key string) *model.Account {
	u, _ := url.Parse("https://api.neuralwatt.com/v1")
	catalog, _ := url.Parse("https://api.neuralwatt.com/v1/models")
	return &model.Account{ID: "neuralwatt", CredentialSource: "env", Endpoints: map[string]*model.Endpoint{"chat": {ID: "chat", Family: model.OpenAIChat, BaseURL: u, Credential: model.StaticCredential{Value: key, Header: "Authorization", Prefix: "Bearer ", Kind: "env"}}}, Models: map[string]*model.Model{}, Catalogs: []model.CatalogSource{{URL: catalog, EndpointID: "chat", AuthEndpointID: "chat"}}}
}
