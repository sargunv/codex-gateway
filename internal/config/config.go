// Package config loads strict environment and TOML gateway configuration.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	codexauth "github.com/sargunv/agent-api-gateway/internal/auth/codex"
	kimiauth "github.com/sargunv/agent-api-gateway/internal/auth/kimi"
	"github.com/sargunv/agent-api-gateway/internal/model"
	codexprovider "github.com/sargunv/agent-api-gateway/internal/provider/codex"
	kimiprovider "github.com/sargunv/agent-api-gateway/internal/provider/kimi"
	"github.com/sargunv/agent-api-gateway/internal/provider/neuralwatt"
	"github.com/sargunv/agent-api-gateway/internal/provider/opencodego"
	"github.com/sargunv/agent-api-gateway/internal/provider/zai"
)

type Config struct {
	APIKey, Addr, ReadyFile     string
	Accounts                    []*model.Account
	MaxBodyBytes, MaxEventBytes int64
}
type Env func(string) string

func OSEnv(k string) string { return os.Getenv(k) }

func Load(get Env) (*Config, error) {
	key := get("GATEWAY_API_KEY")
	if key == "" {
		return nil, errors.New("GATEWAY_API_KEY is required")
	}
	c := &Config{APIKey: key, Addr: "127.0.0.1:8080", MaxBodyBytes: 16 << 20, MaxEventBytes: 1 << 20}
	if x := get("GATEWAY_ADDR"); x != "" {
		if _, _, err := net.SplitHostPort(x); err != nil {
			return nil, fmt.Errorf("invalid GATEWAY_ADDR: %w", err)
		}
		c.Addr = x
	}
	c.ReadyFile = get("GATEWAY_READY_FILE")
	home := get("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if p := get("CODEX_AUTH_FILE"); p != "" {
		m, err := codexauth.New(p)
		if err != nil {
			return nil, fmt.Errorf("CODEX_AUTH_FILE: %w", err)
		}
		c.Accounts = append(c.Accounts, codexprovider.New(m))
	} else if p = filepath.Join(home, ".codex", "auth.json"); exists(p) {
		m, err := codexauth.New(p)
		if err != nil {
			return nil, fmt.Errorf("default Codex credentials: %w", err)
		}
		c.Accounts = append(c.Accounts, codexprovider.New(m))
	}
	if p := get("KIMI_AUTH_FILE"); p != "" {
		m, err := kimiauth.New(p)
		if err != nil {
			return nil, fmt.Errorf("KIMI_AUTH_FILE: %w", err)
		}
		c.Accounts = append(c.Accounts, kimiprovider.New(m))
	} else if p = filepath.Join(home, ".kimi", "credentials", "kimi-code.json"); exists(p) {
		m, err := kimiauth.New(p)
		if err != nil {
			return nil, fmt.Errorf("default Kimi credentials: %w", err)
		}
		c.Accounts = append(c.Accounts, kimiprovider.New(m))
	}
	if z := get("ZAI_API_KEY"); z != "" {
		c.Accounts = append(c.Accounts, zai.New(z))
	}
	if k := get("NEURALWATT_API_KEY"); k != "" {
		c.Accounts = append(c.Accounts, neuralwatt.New(k))
	}
	if k := get("OPENCODE_GO_API_KEY"); k != "" {
		c.Accounts = append(c.Accounts, opencodego.New(k))
	}
	if p := get("GATEWAY_CONFIG"); p != "" {
		a, err := loadFile(p, get)
		if err != nil {
			return nil, err
		}
		c.Accounts = append(c.Accounts, a...)
	}
	if _, err := model.NewCatalog(c.Accounts); err != nil {
		return nil, err
	}
	return c, nil
}
func exists(p string) bool { _, err := os.Stat(p); return err == nil }
func validateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("invalid endpoint URL %q", raw)
	}
	if u.RawQuery != "" {
		return nil, fmt.Errorf("endpoint URL must not contain a query")
	}
	if u.Scheme != "https" {
		host := u.Hostname()
		ip := net.ParseIP(host)
		if u.Scheme != "http" || host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("endpoint URL must use https (http is allowed only for literal loopback/localhost)")
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

var _ fs.FileInfo
