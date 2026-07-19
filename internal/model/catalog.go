package model

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Resolved struct {
	PublicID string
	Account  *Account
	Model    *Model
}

type Catalog struct {
	mu      sync.RWMutex
	entries map[string]Resolved
}

func NewCatalog(accounts []*Account) (*Catalog, error) {
	c := &Catalog{entries: map[string]Resolved{}}
	for _, a := range accounts {
		if err := a.Validate(); err != nil {
			return nil, err
		}
		for id, m := range a.Models {
			public := a.ID + "/" + id
			if _, ok := c.entries[public]; ok {
				return nil, fmt.Errorf("model collision %q", public)
			}
			c.entries[public] = Resolved{public, a, m}
		}
	}
	return c, nil
}

func (c *Catalog) Resolve(id string) (Resolved, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.entries[id]
	return r, ok
}
func (c *Catalog) Len() int { c.mu.RLock(); defer c.mu.RUnlock(); return len(c.entries) }
func (c *Catalog) List() []map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.entries))
	for id := range c.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		r := c.entries[id]
		v := map[string]any{}
		for k, x := range r.Model.Metadata {
			v[k] = x
		}
		v["id"] = id
		families := make([]string, 0, len(r.Model.Routes))
		preferredFamily := ""
		for _, route := range r.Model.Routes {
			family := string(r.Account.Endpoints[route.EndpointID].Family)
			families = append(families, family)
			if route.Preferred || len(r.Model.Routes) == 1 {
				preferredFamily = family
			}
		}
		sort.Strings(families)
		v["endpoint_families"] = families
		v["preferred_endpoint_family"] = preferredFamily
		if _, ok := v["object"]; !ok {
			v["object"] = "model"
		}
		if _, ok := v["created"]; !ok {
			v["created"] = int64(0)
		}
		if _, ok := v["owned_by"]; !ok {
			v["owned_by"] = r.Account.ID
		}
		if _, ok := v["type"]; !ok {
			v["type"] = "model"
		}
		if _, ok := v["display_name"]; !ok {
			v["display_name"] = id
		}
		if _, ok := v["created_at"]; !ok {
			v["created_at"] = time.Unix(unixSeconds(v["created"]), 0).UTC().Format(time.RFC3339)
		}
		out = append(out, v)
	}
	return out
}

func unixSeconds(value any) int64 {
	switch value := value.(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func ParsePublicID(id string) error {
	p := strings.Split(id, "/")
	if len(p) != 2 || p[0] == "" || p[1] == "" {
		return fmt.Errorf("model must be an exact provider/model id")
	}
	return nil
}
