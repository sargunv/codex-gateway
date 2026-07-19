package custom

import "github.com/sargunv/agent-api-gateway/internal/model"

func New(a *model.Account) (*model.Account, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return a, nil
}
