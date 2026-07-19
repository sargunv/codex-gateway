package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) Env { return func(k string) string { return m[k] } }
func TestLoadCustomStrictAndImplicit(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "p.toml")
	doc := `[[providers]]
id="fixture"
api_key_env="FIXTURE_KEY"
[[providers.endpoints]]
id="chat"
family="openai-chat"
base_url="http://127.0.0.1:1234/v1"
models_url="http://127.0.0.1:1234/v1/models"
[[providers.models]]
id="m"
upstream_id="up"
routes=["chat"]
preferred_route="chat"
context_window=128000
max_output_tokens=8192
supports_tools=true
supports_reasoning=false
input_modalities=["text","image"]
`
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(env(map[string]string{"GATEWAY_API_KEY": "down", "GATEWAY_CONFIG": p, "FIXTURE_KEY": "up", "HOME": d}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Accounts) != 1 || c.Accounts[0].ID != "fixture" {
		t.Fatalf("accounts %#v", c.Accounts)
	}
	account := c.Accounts[0]
	if len(account.Catalogs) != 1 || account.Catalogs[0].EndpointID != "chat" {
		t.Fatalf("catalogs %#v", account.Catalogs)
	}
	if account.Models["m"].Metadata["context_window"] != int64(128000) || account.Models["m"].Metadata["supports_tools"] != true || account.Models["m"].Metadata["supports_reasoning"] != false {
		t.Fatalf("metadata %#v", account.Models["m"].Metadata)
	}
}

func TestStrictUnknownAndSecretFreeErrors(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "p.toml")
	_ = os.WriteFile(p, []byte("unknown='secret-value'\n"), 0o600)
	_, err := Load(env(map[string]string{"GATEWAY_API_KEY": "down", "GATEWAY_CONFIG": p, "HOME": d}))
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("%v", err)
	}
}

func TestExplicitMissingCredentialFailsDefaultMissingDisables(t *testing.T) {
	d := t.TempDir()
	c, err := Load(env(map[string]string{"GATEWAY_API_KEY": "x", "HOME": d}))
	if err != nil || len(c.Accounts) != 0 {
		t.Fatalf("%v %#v", err, c)
	}
	_, err = Load(env(map[string]string{"GATEWAY_API_KEY": "x", "HOME": d, "CODEX_AUTH_FILE": filepath.Join(d, "missing")}))
	if err == nil {
		t.Fatal("expected failure")
	}
}

func TestValidateURLAllowsOnlyHTTPSOrLoopbackHTTP(t *testing.T) {
	for _, raw := range []string{"http://localhost:1234/v1", "http://127.0.0.1:1234/v1", "http://[::1]:1234/v1", "https://203.0.113.1/v1"} {
		if _, err := validateURL(raw); err != nil {
			t.Errorf("validateURL(%q): %v", raw, err)
		}
	}
	for _, raw := range []string{"http://203.0.113.1:1234/v1", "http://8.8.8.8/v1", "http://example.test/v1"} {
		if _, err := validateURL(raw); err == nil {
			t.Errorf("validateURL(%q) unexpectedly succeeded", raw)
		}
	}
}
