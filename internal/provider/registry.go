package provider

import "github.com/sargunv/agent-api-gateway/internal/model"

type Registry struct {
	Accounts []*Account
	Catalog  *model.Catalog
}

func NewRegistry(accounts []*Account) (*Registry, error) {
	c, err := model.NewCatalog(accounts)
	if err != nil {
		return nil, err
	}
	return &Registry{Accounts: accounts, Catalog: c}, nil
}
