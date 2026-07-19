// Package upstream performs safe, family-aware upstream HTTP requests.
package upstream

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/sargunv/agent-api-gateway/internal/model"
)

type Client struct{ HTTP *http.Client }

func New(rt http.RoundTripper) *Client {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &Client{HTTP: &http.Client{Transport: rt, Timeout: 0}}
}

func Path(ep *model.Endpoint, count bool) string {
	p := strings.TrimRight(ep.BaseURL.Path, "/")
	switch ep.Family {
	case model.OpenAIChat:
		return p + "/chat/completions"
	case model.OpenAIResponses:
		return p + "/responses"
	case model.AnthropicMessages:
		if !strings.HasSuffix(p, "/v1") {
			p += "/v1"
		}
		if count {
			return p + "/messages/count_tokens"
		}
		return p + "/messages"
	}
	return path.Clean(p)
}

func (c *Client) Do(ctx context.Context, ep *model.Endpoint, body []byte, count bool, requestHeaders http.Header) (*http.Response, error) {
	u := *ep.BaseURL
	u.Path = Path(ep, count)
	u.RawQuery = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if ep.Family == model.AnthropicMessages {
		v := requestHeaders.Get("anthropic-version")
		if v == "" {
			v = "2023-06-01"
		}
		req.Header.Set("anthropic-version", v)
	}
	for k, v := range ep.Headers {
		req.Header.Set(k, v)
	}
	if ep.Credential != nil {
		h, err := ep.Credential.Headers()
		if err != nil {
			return nil, err
		}
		for k, v := range h {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream request: %w", err)
	}
	return resp, nil
}

var (
	_ = url.URL{}
	_ = time.Second
)
