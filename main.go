// Package main implements the codex-gateway CLI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

const (
	upstreamURL = "https://chatgpt.com/backend-api/codex"
	refreshURL  = "https://auth.openai.com/oauth/token"
	// clientID is the public OAuth client id baked into the Codex CLI;
	// it's not a secret and is required to refresh ChatGPT subscription tokens.
	clientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	refreshSkew = 30 * time.Second
	defaultAddr = "127.0.0.1:8080"
	// clientVersion is reported to the upstream as the Codex CLI version. The
	// backend gates /models (and some features) by client_version against each
	// model's minimal_client_version, so we claim a high one to see everything.
	// /responses is version-agnostic in practice. Bump if a model needs more.
	clientVersion = "0.999.0"
)

var (
	version = "0.0.0-dev"
	commit  = "unknown"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "codex-gateway",
		Short: "Expose your Codex (ChatGPT) subscription as a local OpenAI Responses API endpoint.",
		Long: "codex-gateway borrows the OAuth tokens stored by the OpenAI Codex CLI (~/.codex/auth.json) " +
			"and proxies an OpenAI Responses API endpoint on localhost, forwarding requests to the " +
			"ChatGPT backend-api/codex endpoint with access tokens and account id injected.",
		Version:      version,
		SilenceUsage: true,
	}
	root.AddCommand(newServeCmd())
	return root
}

func newServeCmd() *cobra.Command {
	var addr string
	var authFile string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the local gateway HTTP server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(addr, authFile)
		},
	}
	cmd.Flags().StringVarP(&addr, "addr", "a", defaultAddr, "address (host:port) to listen on")
	cmd.Flags().StringVar(&authFile, "auth-file", "", "path to the Codex CLI auth.json (defaults to $CODEX_HOME/auth.json or ~/.codex/auth.json)")
	return cmd
}

func runServe(addr, authFile string) error {
	auth, err := newCodexAuth(authFile)
	if err != nil {
		return err
	}
	target, err := url.Parse(upstreamURL)
	if err != nil {
		return fmt.Errorf("parsing upstream URL: %w", err)
	}
	proxy := newReverseProxy(target, auth)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") && r.URL.Path != "/v1" {
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("codex-gateway %s (%s) listening on %s -> %s", version, commit, addr, target.String())
	return srv.ListenAndServe()
}

func newReverseProxy(target *url.URL, auth *codexAuth) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			accessToken, accountID, err := auth.borrow()
			if err != nil {
				log.Printf("auth error: %v", err)
				return
			}
			r.URL.Scheme = target.Scheme
			r.URL.Host = target.Host
			r.URL.Path = target.Path + strings.TrimPrefix(r.URL.Path, "/v1")
			r.Host = target.Host
			q := r.URL.Query()
			if q.Get("client_version") == "" {
				q.Set("client_version", clientVersion)
				r.URL.RawQuery = q.Encode()
			}
			r.Header.Set("Authorization", "Bearer "+accessToken)
			if accountID != "" {
				r.Header.Set("ChatGPT-Account-ID", accountID)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error: %v", err)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprintln(w, "gateway error:", err)
		},
	}
}

var codexOAuthConfig = oauth2.Config{
	ClientID: clientID,
	Endpoint: oauth2.Endpoint{
		TokenURL: refreshURL,
	},
}

func refreshToken(refreshToken string) (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return codexOAuthConfig.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
}

// --- Codex CLI auth: read ~/.codex/auth.json, refresh tokens as needed ---

// authFile mirrors the Codex CLI's ~/.codex/auth.json. Schema sourced from the
// AuthDotJson struct in the codex-rs login crate:
// https://github.com/openai/codex/blob/main/codex-rs/login/src/auth/storage.rs
type authFile struct {
	AuthMode  string `json:"auth_mode"`
	AccountID string `json:"account_id"`
	Tokens    struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
	} `json:"tokens"`
}

// codexAuth reads and refreshes the Codex CLI's OAuth tokens. The mutex
// serializes borrow() so concurrent requests don't race on the refresh-and-
// writeback path, which would corrupt the rotated refresh_token in auth.json.
type codexAuth struct {
	mu       sync.Mutex
	authPath string
}

func newCodexAuth(override string) (*codexAuth, error) {
	p := override
	if p == "" {
		if h := os.Getenv("CODEX_HOME"); h != "" {
			p = filepath.Join(h, "auth.json")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("resolving auth.json path: %w", err)
			}
			p = filepath.Join(home, ".codex", "auth.json")
		}
	}
	if _, err := os.Stat(p); err != nil {
		return nil, fmt.Errorf("codex auth file not found at %s: run `codex login` first: %w", p, err)
	}
	return &codexAuth{authPath: p}, nil
}

// borrow returns a fresh access token (refreshing if needed) and the account id.
func (c *codexAuth) borrow() (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.authPath)
	if err != nil {
		return "", "", fmt.Errorf("reading auth.json: %w", err)
	}
	var af authFile
	if err := json.Unmarshal(data, &af); err != nil {
		return "", "", fmt.Errorf("parsing auth.json: %w", err)
	}
	if af.AuthMode != "chatgpt" {
		return "", "", fmt.Errorf("expected auth_mode 'chatgpt', got %q", af.AuthMode)
	}
	if af.Tokens.AccessToken == "" {
		return "", "", errors.New("no access_token in auth.json; run `codex login` first")
	}

	// Still valid? Skip refresh.
	if claims := jwtClaims(af.Tokens.AccessToken); claims != nil {
		if exp, _ := claims.GetExpirationTime(); exp != nil &&
			time.Now().Add(refreshSkew).Before(exp.Time) {
			return af.Tokens.AccessToken, af.AccountID, nil
		}
	}

	if af.Tokens.RefreshToken == "" {
		return "", "", errors.New("access token expired and no refresh_token; run `codex login` again")
	}

	newTok, err := refreshToken(af.Tokens.RefreshToken)
	if err != nil {
		return "", "", err
	}
	af.Tokens.AccessToken = newTok.AccessToken
	if newTok.RefreshToken != "" {
		af.Tokens.RefreshToken = newTok.RefreshToken
	}
	if idTok, ok := newTok.Extra("id_token").(string); ok && idTok != "" {
		af.Tokens.IDToken = idTok
	}
	if id := accountIDFromToken(newTok.AccessToken); id != "" {
		af.AccountID = id
	}

	// Write in place rather than via a temp file + rename. Not atomic, but it
	// works with a read-write bind mount of just auth.json (no sibling tmp file
	// needed). Worst case: a crash mid-write truncates auth.json and the user
	// re-runs `codex login`. Acceptable for a single-user local gateway.
	out, err := json.MarshalIndent(af, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(c.authPath, out, 0o600); err != nil {
		return "", "", err
	}
	return af.Tokens.AccessToken, af.AccountID, nil
}

func accountIDFromToken(tok string) string {
	claims := jwtClaims(tok)
	if id, ok := claims["https://api.openai.com/auth|account_id"].(string); ok && id != "" {
		return id
	}
	if id, ok := claims["chatgpt_account_id"].(string); ok {
		return id
	}
	return ""
}

func jwtClaims(tok string) jwt.MapClaims {
	t, _, err := jwt.NewParser().ParseUnverified(tok, jwt.MapClaims{})
	if err != nil {
		return nil
	}
	if mc, ok := t.Claims.(jwt.MapClaims); ok {
		return mc
	}
	return nil
}
