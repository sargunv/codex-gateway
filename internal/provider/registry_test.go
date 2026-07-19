package provider_test

import (
	"testing"

	"github.com/sargunv/agent-api-gateway/internal/model"
	"github.com/sargunv/agent-api-gateway/internal/provider/codex"
	"github.com/sargunv/agent-api-gateway/internal/provider/kimi"
	"github.com/sargunv/agent-api-gateway/internal/provider/neuralwatt"
	"github.com/sargunv/agent-api-gateway/internal/provider/opencodego"
	"github.com/sargunv/agent-api-gateway/internal/provider/zai"
)

func TestBuiltInsHaveCatalogsNotStaticModels(t *testing.T) {
	credential := model.StaticCredential{Value: "fixture", Header: "Authorization", Prefix: "Bearer "}
	accounts := []*model.Account{
		codex.New(credential),
		kimi.New(credential),
		zai.New("fixture"),
		neuralwatt.New("fixture"),
		opencodego.New("fixture"),
	}
	for _, account := range accounts {
		if len(account.Models) != 0 {
			t.Errorf("%s constructor has %d static models", account.ID, len(account.Models))
		}
		if len(account.Catalogs) != 1 {
			t.Errorf("%s catalogs = %d, want 1", account.ID, len(account.Catalogs))
		}
		if err := account.Validate(); err != nil {
			t.Errorf("%s: %v", account.ID, err)
		}
	}
}
