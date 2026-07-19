package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/sargunv/agent-api-gateway/internal/model"
)

type fileConfig struct {
	Providers []fileProvider `toml:"providers"`
}
type fileProvider struct {
	ID        string         `toml:"id"`
	APIKeyEnv string         `toml:"api_key_env"`
	Endpoints []fileEndpoint `toml:"endpoints"`
	Models    []fileModel    `toml:"models"`
}
type fileEndpoint struct {
	ID          string       `toml:"id"`
	Family      model.Family `toml:"family"`
	BaseURL     string       `toml:"base_url"`
	ModelsURL   string       `toml:"models_url"`
	AuthHeader  string       `toml:"auth_header"`
	AuthPrefix  string       `toml:"auth_prefix"`
	CountTokens bool         `toml:"count_tokens"`
}
type fileModel struct {
	ID                string   `toml:"id"`
	UpstreamID        string   `toml:"upstream_id"`
	Routes            []string `toml:"routes"`
	PreferredRoute    string   `toml:"preferred_route"`
	ContextWindow     int64    `toml:"context_window"`
	MaxOutputTokens   int64    `toml:"max_output_tokens"`
	SupportsTools     *bool    `toml:"supports_tools"`
	SupportsReasoning *bool    `toml:"supports_reasoning"`
	InputModalities   []string `toml:"input_modalities"`
}

func loadFile(path string, get Env) ([]*model.Account, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("GATEWAY_CONFIG: %w", err)
	}
	var f fileConfig
	d := toml.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err = d.Decode(&f); err != nil {
		return nil, fmt.Errorf("GATEWAY_CONFIG: %w", err)
	}
	seen := map[string]bool{}
	out := make([]*model.Account, 0, len(f.Providers))
	for _, p := range f.Providers {
		if p.ID == "" || seen[p.ID] {
			return nil, fmt.Errorf("duplicate or empty custom provider id %q", p.ID)
		}
		seen[p.ID] = true
		if p.APIKeyEnv == "" {
			return nil, fmt.Errorf("provider %q api_key_env is required", p.ID)
		}
		key := get(p.APIKeyEnv)
		if key == "" {
			return nil, fmt.Errorf("provider %q references missing environment variable %s", p.ID, p.APIKeyEnv)
		}
		a := &model.Account{ID: p.ID, CredentialSource: "env", Endpoints: map[string]*model.Endpoint{}, Models: map[string]*model.Model{}}
		for _, e := range p.Endpoints {
			if _, ok := a.Endpoints[e.ID]; ok || e.ID == "" {
				return nil, fmt.Errorf("provider %q duplicate endpoint %q", p.ID, e.ID)
			}
			if !e.Family.Valid() {
				return nil, fmt.Errorf("provider %q endpoint %q unsupported family %q", p.ID, e.ID, e.Family)
			}
			u, err := validateURL(e.BaseURL)
			if err != nil {
				return nil, err
			}
			header, prefix := e.AuthHeader, e.AuthPrefix
			if header == "" {
				if e.Family == model.AnthropicMessages {
					header = "x-api-key"
				} else {
					header = "Authorization"
					prefix = "Bearer "
				}
			}
			if !strings.EqualFold(header, "Authorization") && !strings.EqualFold(header, "x-api-key") {
				return nil, fmt.Errorf("provider %q endpoint %q unsupported auth_header", p.ID, e.ID)
			}
			a.Endpoints[e.ID] = &model.Endpoint{ID: e.ID, Family: e.Family, BaseURL: u, Credential: model.StaticCredential{Value: key, Header: header, Prefix: prefix, Kind: "env"}, CountTokens: e.CountTokens}
			if e.ModelsURL != "" {
				catalogURL, err := validateURL(e.ModelsURL)
				if err != nil {
					return nil, fmt.Errorf("provider %q endpoint %q models_url: %w", p.ID, e.ID, err)
				}
				a.Catalogs = append(a.Catalogs, model.CatalogSource{URL: catalogURL, EndpointID: e.ID, AuthEndpointID: e.ID})
			}
		}
		for _, m := range p.Models {
			if _, ok := a.Models[m.ID]; ok || m.ID == "" {
				return nil, fmt.Errorf("provider %q duplicate model %q", p.ID, m.ID)
			}
			up := m.UpstreamID
			if up == "" {
				up = m.ID
			}
			routes := make([]model.Route, 0, len(m.Routes))
			for _, r := range m.Routes {
				routes = append(routes, model.Route{EndpointID: r, Preferred: r == m.PreferredRoute})
			}
			metadata := map[string]any{}
			if m.ContextWindow > 0 {
				metadata["context_window"] = m.ContextWindow
			}
			if m.MaxOutputTokens > 0 {
				metadata["max_output_tokens"] = m.MaxOutputTokens
			}
			if m.SupportsTools != nil {
				metadata["supports_tools"] = *m.SupportsTools
			}
			if m.SupportsReasoning != nil {
				metadata["supports_reasoning"] = *m.SupportsReasoning
			}
			if len(m.InputModalities) != 0 {
				metadata["input_modalities"] = m.InputModalities
			}
			a.Models[m.ID] = &model.Model{ID: m.ID, UpstreamID: up, Routes: routes, Metadata: metadata}
		}
		if err := a.Validate(); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}
