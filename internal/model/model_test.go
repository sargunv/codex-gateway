package model

import (
	"net/url"
	"testing"
)

func TestSelectNativeThenPreferred(t *testing.T) {
	u, _ := url.Parse("https://example.test/v1")
	a := &Account{ID: "p", Endpoints: map[string]*Endpoint{"c": {ID: "c", Family: OpenAIChat, BaseURL: u}, "m": {ID: "m", Family: AnthropicMessages, BaseURL: u}}, Models: map[string]*Model{"x": {ID: "x", UpstreamID: "up", Routes: []Route{{EndpointID: "c", Preferred: true}, {EndpointID: "m"}}}}}
	c, err := NewCatalog([]*Account{a})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := c.Resolve("p/x")
	ep, conv, _ := a.Select(r.Model, AnthropicMessages)
	if ep.ID != "m" || conv {
		t.Fatalf("native route not selected")
	}
	ep, conv, _ = a.Select(r.Model, OpenAIResponses)
	if ep.ID != "c" || !conv {
		t.Fatalf("preferred conversion route not selected")
	}
}

func TestRejectAmbiguousRoutes(t *testing.T) {
	u, _ := url.Parse("https://example.test")
	a := &Account{ID: "p", Endpoints: map[string]*Endpoint{"a": {ID: "a", Family: OpenAIChat, BaseURL: u}, "b": {ID: "b", Family: OpenAIChat, BaseURL: u}}, Models: map[string]*Model{"x": {ID: "x", UpstreamID: "x", Routes: []Route{{EndpointID: "a", Preferred: true}, {EndpointID: "b"}}}}}
	if _, err := NewCatalog([]*Account{a}); err == nil {
		t.Fatal("expected ambiguous route rejection")
	}
}
