// Package model defines the gateway's provider, endpoint, and model route graph.
package model

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type Family string

const (
	OpenAIChat        Family = "openai-chat"
	OpenAIResponses   Family = "openai-responses"
	AnthropicMessages Family = "anthropic-messages"
)

func (f Family) Valid() bool {
	return f == OpenAIChat || f == OpenAIResponses || f == AnthropicMessages
}

type Credential interface {
	Headers() (map[string]string, error)
	Source() string
}

type StaticCredential struct{ Value, Header, Prefix, Kind string }

func (c StaticCredential) Headers() (map[string]string, error) {
	if c.Value == "" {
		return nil, fmt.Errorf("empty upstream credential")
	}
	h := c.Header
	if h == "" {
		h = "Authorization"
	}
	return map[string]string{h: c.Prefix + c.Value}, nil
}

func (c StaticCredential) Source() string {
	if c.Kind != "" {
		return c.Kind
	}
	return "env"
}

type Endpoint struct {
	ID          string
	Family      Family
	BaseURL     *url.URL
	Credential  Credential
	Headers     map[string]string
	CountTokens bool
}

type Route struct {
	EndpointID string
	Preferred  bool
}

type Model struct {
	ID         string
	UpstreamID string
	Routes     []Route
	Metadata   map[string]any
}

// CatalogSource describes an authenticated provider model catalog. EndpointID
// is empty when the catalog does not identify a safe wire route for new models.
// AuthEndpointID selects the endpoint whose credential/header policy is used.
type CatalogSource struct {
	URL            *url.URL
	EndpointID     string
	AuthEndpointID string
	Format         string
	ProviderID     string
	NPMRoutes      map[string]string
}

type Account struct {
	ID               string
	Endpoints        map[string]*Endpoint
	Models           map[string]*Model
	Catalogs         []CatalogSource
	CredentialSource string
}

func (a *Account) Validate() error {
	if a.ID == "" || strings.Contains(a.ID, "/") {
		return fmt.Errorf("invalid provider id %q", a.ID)
	}
	if len(a.Endpoints) == 0 || (len(a.Models) == 0 && len(a.Catalogs) == 0) {
		return fmt.Errorf("provider %q must have endpoints and models or catalogs", a.ID)
	}
	for id, ep := range a.Endpoints {
		if id == "" || ep == nil || ep.ID != id || !ep.Family.Valid() {
			return fmt.Errorf("provider %q has invalid endpoint %q", a.ID, id)
		}
		if ep.BaseURL == nil || ep.BaseURL.Scheme != "https" && ep.BaseURL.Scheme != "http" || ep.BaseURL.Host == "" || ep.BaseURL.User != nil || ep.BaseURL.Fragment != "" {
			return fmt.Errorf("provider %q endpoint %q has invalid base URL", a.ID, id)
		}
	}
	for _, source := range a.Catalogs {
		if source.URL == nil || source.URL.Host == "" || source.URL.Scheme != "https" && source.URL.Scheme != "http" || source.URL.User != nil || source.URL.Fragment != "" {
			return fmt.Errorf("provider %q has invalid catalog URL", a.ID)
		}
		if source.Format != "models.dev" && (source.AuthEndpointID == "" || a.Endpoints[source.AuthEndpointID] == nil) {
			return fmt.Errorf("provider %q catalog references unknown auth endpoint %q", a.ID, source.AuthEndpointID)
		}
		if source.Format != "" && source.Format != "models.dev" {
			return fmt.Errorf("provider %q catalog has unsupported format %q", a.ID, source.Format)
		}
		if source.Format == "models.dev" && source.ProviderID == "" {
			return fmt.Errorf("provider %q models.dev catalog must select a provider", a.ID)
		}
		if source.EndpointID != "" && a.Endpoints[source.EndpointID] == nil {
			return fmt.Errorf("provider %q catalog references unknown route endpoint %q", a.ID, source.EndpointID)
		}
		for npm, endpointID := range source.NPMRoutes {
			if npm == "" || a.Endpoints[endpointID] == nil {
				return fmt.Errorf("provider %q catalog has invalid npm route %q", a.ID, npm)
			}
		}

	}
	for id, m := range a.Models {
		if id == "" || strings.Contains(id, "/") || m == nil || m.ID != id || m.UpstreamID == "" || len(m.Routes) == 0 {
			return fmt.Errorf("provider %q has invalid model %q", a.ID, id)
		}
		seen := map[Family]bool{}
		preferred := 0
		for _, r := range m.Routes {
			ep := a.Endpoints[r.EndpointID]
			if ep == nil {
				return fmt.Errorf("provider %q model %q references unknown endpoint %q", a.ID, id, r.EndpointID)
			}
			if seen[ep.Family] {
				return fmt.Errorf("provider %q model %q has ambiguous %s routes", a.ID, id, ep.Family)
			}
			seen[ep.Family] = true
			if r.Preferred {
				preferred++
			}
		}
		if len(m.Routes) > 1 && preferred != 1 {
			return fmt.Errorf("provider %q model %q must declare exactly one preferred route", a.ID, id)
		}
	}
	return nil
}

func (a *Account) Select(m *Model, caller Family) (*Endpoint, bool, error) {
	for _, r := range m.Routes {
		if ep := a.Endpoints[r.EndpointID]; ep.Family == caller {
			return ep, false, nil
		}
	}
	for _, r := range m.Routes {
		if r.Preferred || len(m.Routes) == 1 {
			return a.Endpoints[r.EndpointID], true, nil
		}
	}
	return nil, false, fmt.Errorf("model %q has no selectable route", m.ID)
}

func (a *Account) Families() []Family {
	s := map[Family]bool{}
	for _, e := range a.Endpoints {
		s[e.Family] = true
	}
	out := make([]Family, 0, len(s))
	for f := range s {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
