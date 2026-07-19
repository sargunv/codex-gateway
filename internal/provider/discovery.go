package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

const maxCatalogBytes = 8 << 20

var requiredMetadata = []string{"context_window", "max_output_tokens", "supports_tools", "supports_reasoning", "input_modalities"}

// DiscoveryWarning is safe to log: it contains no response body, credential, or
// request header values.
type DiscoveryWarning struct {
	Provider string
	Model    string
	Kind     string
	Detail   string
}

type catalogResult struct {
	account *model.Account
	source  model.CatalogSource
	models  []map[string]any
	status  int
	err     error
}

type publicCatalogFetch struct {
	done   chan struct{}
	body   []byte
	status int
	err    error
}

type publicCatalogCache struct {
	mu      sync.Mutex
	fetches map[string]*publicCatalogFetch
}

// Discover loads configured catalogs into the route graph. Catalog failures and
// incomplete entries are warnings; models without an unambiguous route are not
// exposed.
func Discover(ctx context.Context, accounts []*model.Account, client *http.Client) []DiscoveryWarning {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	results := fetchCatalogs(ctx, accounts, client)
	sort.Slice(results, func(i, j int) bool {
		if results[i].account.ID != results[j].account.ID {
			return results[i].account.ID < results[j].account.ID
		}
		return results[i].source.URL.String() < results[j].source.URL.String()
	})

	var warnings []DiscoveryWarning
	for _, result := range results {
		account := result.account
		if result.err != nil || result.status < 200 || result.status >= 300 {
			detail := "request failed"
			if result.status != 0 {
				detail = fmt.Sprintf("HTTP %d", result.status)
			}
			warnings = append(warnings, warning(account.ID, "", "catalog_unavailable", detail))
			continue
		}
		for _, raw := range result.models {
			id, ok := modelID(raw)
			if !ok {
				warnings = append(warnings, warning(account.ID, "", "catalog_entry_invalid", "missing string id"))
				continue
			}
			endpointID := result.source.EndpointID
			if endpointID == "" {
				npm, _ := raw["provider_npm"].(string)
				endpointID = result.source.NPMRoutes[npm]
			}
			entry := account.Models[id]
			if entry == nil && endpointID == "" {
				warnings = append(warnings, warning(account.ID, id, "route_unclassified", "model withheld"))
				continue
			}
			if entry == nil {
				entry = &model.Model{ID: id, UpstreamID: id, Metadata: map[string]any{}}
				account.Models[id] = entry
			}
			if entry.Metadata == nil {
				entry.Metadata = map[string]any{}
			}
			for key, value := range normalizeMetadata(raw) {
				if old, found := entry.Metadata[key]; found && !reflect.DeepEqual(old, value) {
					warnings = append(warnings, warning(account.ID, id, "metadata_conflict", key+"; catalog value used"))
				}
				entry.Metadata[key] = value
			}
			if endpointID == "" {
				continue
			}
			if len(result.source.NPMRoutes) != 0 {
				if len(entry.Routes) != 1 || entry.Routes[0].EndpointID != endpointID {
					if len(entry.Routes) != 0 {
						warnings = append(warnings, warning(account.ID, id, "route_conflict", "catalog route replaced configured route"))
					}
					entry.Routes = []model.Route{{EndpointID: endpointID, Preferred: true}}
				}
			} else if conflict := addRoute(account, entry, endpointID); conflict != "" {
				warnings = append(warnings, warning(account.ID, id, "route_conflict", conflict))
			}
		}
	}

	for _, account := range accounts {
		ids := make([]string, 0, len(account.Models))
		for id := range account.Models {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if missing := missingMetadata(account.Models[id].Metadata); len(missing) != 0 {
				warnings = append(warnings, warning(account.ID, id, "metadata_incomplete", "missing "+strings.Join(missing, ",")))
			}
		}
	}
	sort.Slice(warnings, func(i, j int) bool {
		a, b := warnings[i], warnings[j]
		return a.Provider+"\x00"+a.Model+"\x00"+a.Kind+"\x00"+a.Detail < b.Provider+"\x00"+b.Model+"\x00"+b.Kind+"\x00"+b.Detail
	})
	return warnings
}

func fetchCatalogs(ctx context.Context, accounts []*model.Account, client *http.Client) []catalogResult {
	var wg sync.WaitGroup
	public := &publicCatalogCache{fetches: map[string]*publicCatalogFetch{}}
	results := make(chan catalogResult)
	count := 0
	for _, account := range accounts {
		for _, source := range account.Catalogs {
			count++
			wg.Add(1)
			go func(account *model.Account, source model.CatalogSource) {
				defer wg.Done()
				results <- fetchCatalog(ctx, account, source, client, public)
			}(account, source)
		}
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	out := make([]catalogResult, 0, count)
	for result := range results {
		out = append(out, result)
	}
	return out
}

func fetchCatalog(ctx context.Context, account *model.Account, source model.CatalogSource, client *http.Client, public *publicCatalogCache) catalogResult {
	result := catalogResult{account: account, source: source}
	if source.Format == "models.dev" {
		body, status, err := public.fetch(ctx, source.URL.String(), client)
		result.status, result.err = status, err
		if err == nil && status >= 200 && status < 300 {
			result.models, result.err = decodeCatalog(body, source)
		}
		return result
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL.String(), nil)
	if err != nil {
		result.err = err
		return result
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agent-api-gateway")
	auth := account.Endpoints[source.AuthEndpointID]
	if auth == nil {
		result.err = fmt.Errorf("missing auth endpoint")
		return result
	}
	for key, value := range auth.Headers {
		req.Header.Set(key, value)
	}
	headers, err := auth.Credential.Headers()
	if err != nil {
		result.err = err
		return result
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		result.err = err
		return result
	}
	defer func() { _ = resp.Body.Close() }()
	result.status = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil || len(body) > maxCatalogBytes {
		result.err = fmt.Errorf("invalid catalog body")
		return result
	}
	result.models, err = decodeCatalog(body, source)
	if err != nil {
		result.err = err
	}
	return result
}

func (cache *publicCatalogCache) fetch(ctx context.Context, rawURL string, client *http.Client) ([]byte, int, error) {
	cache.mu.Lock()
	fetch := cache.fetches[rawURL]
	if fetch == nil {
		fetch = &publicCatalogFetch{done: make(chan struct{})}
		cache.fetches[rawURL] = fetch
		cache.mu.Unlock()
		fetch.body, fetch.status, fetch.err = fetchPublicCatalog(ctx, rawURL, client)
		close(fetch.done)
	} else {
		cache.mu.Unlock()
		select {
		case <-fetch.done:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	return fetch.body, fetch.status, fetch.err
}

func fetchPublicCatalog(ctx context.Context, rawURL string, client *http.Client) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agent-api-gateway")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil || len(body) > maxCatalogBytes {
		return nil, resp.StatusCode, fmt.Errorf("invalid catalog body")
	}
	return body, resp.StatusCode, nil
}

func decodeCatalog(body []byte, source model.CatalogSource) ([]map[string]any, error) {
	if source.Format == "models.dev" {
		var providers map[string]struct {
			NPM    string                    `json:"npm"`
			Models map[string]map[string]any `json:"models"`
		}
		if err := json.Unmarshal(body, &providers); err != nil {
			return nil, fmt.Errorf("invalid models.dev JSON")
		}
		providerEntry, ok := providers[source.ProviderID]
		if !ok {
			return nil, fmt.Errorf("models.dev provider is absent")
		}
		ids := make([]string, 0, len(providerEntry.Models))
		for id := range providerEntry.Models {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		entries := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			entry := providerEntry.Models[id]
			if entry == nil {
				entry = map[string]any{}
			}
			entry["id"] = id
			provider, _ := entry["provider"].(map[string]any)
			npm, _ := provider["npm"].(string)
			if npm == "" {
				npm = providerEntry.NPM
			}
			if npm != "" {
				entry["provider_npm"] = npm
			}
			if limit, ok := entry["limit"].(map[string]any); ok {
				entry["context_window"] = limit["context"]
				entry["max_output_tokens"] = limit["output"]
			}
			if tools, ok := entry["tool_call"].(bool); ok {
				entry["supports_tools"] = tools
			}
			if reasoning, ok := entry["reasoning"].(bool); ok {
				entry["supports_reasoning"] = reasoning
			}
			if modalities, ok := entry["modalities"].(map[string]any); ok {
				entry["input_modalities"] = modalities["input"]
			}
			entries = append(entries, entry)
		}
		return entries, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("invalid catalog JSON")
	}
	raw := root["data"]
	if len(raw) == 0 {
		raw = root["models"]
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("catalog has no data or models array")
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("invalid catalog entries")
	}
	return entries, nil
}

func modelID(raw map[string]any) (string, bool) {
	for _, key := range []string{"id", "slug"} {
		if value, ok := raw[key].(string); ok && value != "" && !strings.Contains(value, "/") {
			return value, true
		}
	}
	return "", false
}

func normalizeMetadata(raw map[string]any) map[string]any {
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		switch key {
		case "id", "base_instructions", "model_messages":
			continue
		default:
			out[key] = value
		}
	}
	for _, key := range []string{"context_window", "max_output_tokens"} {
		if value, ok := positiveInt(raw[key]); ok {
			out[key] = value
		}
	}
	if value, ok := raw["supports_tools"].(bool); ok {
		out["supports_tools"] = value
	}
	if value, ok := raw["supports_reasoning"].(bool); ok {
		out["supports_reasoning"] = value
	} else if levels, ok := raw["supported_reasoning_levels"].([]any); ok {
		out["supports_reasoning"] = len(levels) > 0
	}
	if value, ok := stringList(raw["input_modalities"]); ok {
		out["input_modalities"] = value
	}
	return out
}

func positiveInt(value any) (int64, bool) {
	switch value := value.(type) {
	case int:
		return int64(value), value > 0
	case int64:
		return value, value > 0
	case float64:
		return int64(value), value > 0 && value == float64(int64(value))
	case json.Number:
		number, err := value.Int64()
		return number, err == nil && number > 0
	default:
		return 0, false
	}
}

func stringList(value any) ([]string, bool) {
	if strings, ok := value.([]string); ok && len(strings) != 0 {
		return strings, true
	}
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok || text == "" {
			return nil, false
		}
		out = append(out, text)
	}
	return out, true
}

func addRoute(account *model.Account, entry *model.Model, endpointID string) string {
	endpoint := account.Endpoints[endpointID]
	for _, route := range entry.Routes {
		if route.EndpointID == endpointID {
			return ""
		}
		if account.Endpoints[route.EndpointID].Family == endpoint.Family {
			return "catalog endpoint duplicates an existing wire family; route ignored"
		}
	}
	preferred := len(entry.Routes) == 0
	entry.Routes = append(entry.Routes, model.Route{EndpointID: endpointID, Preferred: preferred})
	return ""
}

func missingMetadata(metadata map[string]any) []string {
	var missing []string
	for _, key := range requiredMetadata {
		if metadata == nil {
			missing = append(missing, key)
			continue
		}
		value, ok := metadata[key]
		if !ok || value == nil {
			missing = append(missing, key)
		}
	}
	return missing
}

func warning(provider, modelID, kind, detail string) DiscoveryWarning {
	return DiscoveryWarning{Provider: provider, Model: modelID, Kind: kind, Detail: detail}
}
