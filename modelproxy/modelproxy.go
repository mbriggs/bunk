// Package modelproxy gives a box Claude without giving it a credential. The
// box's Claude sends a sentinel bearer token to this reverse proxy, which
// forwards to api.anthropic.com with the build machine's real OAuth token in
// its place. The token is reread on each request, so a refresh by the host's
// own Claude is picked up. It is never logged.
package modelproxy

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// Sentinel is the token a box's Claude is configured with.
const Sentinel = "sentinel"

var upstream = &url.URL{Scheme: "https", Host: "api.anthropic.com"}

// Credentials says where the real token comes from: a file holding just the
// token (TokenFile, as `claude setup-token` prints it), or else Claude's
// credentials JSON, from the file at Path or from Cmd's output (on macOS,
// Claude keeps it in the Keychain).
//
// Claude's credentials hold a short-lived token that only the host's own
// Claude refreshes, so they suit a machine where Claude runs every day. A
// token from `claude setup-token` lasts about a year, and suits a build
// machine where nothing else runs Claude.
type Credentials struct {
	TokenFile string
	Path      string
	Cmd       string
}

// Handler returns the proxy. Requests that don't carry the sentinel pass
// through untouched.
func Handler(creds Credentials) http.Handler {
	return &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			r.Out.Host = upstream.Host
			r.Out.Header.Del("X-Forwarded-For")
			if r.Out.Header.Get("Authorization") != "Bearer "+Sentinel {
				return
			}
			tok, err := creds.token()
			if err != nil {
				log.Printf("modelproxy: reading the token: %v", err)
				return
			}
			r.Out.Header.Set("Authorization", "Bearer "+tok)
		},
		ModifyResponse: func(resp *http.Response) error {
			log.Printf("modelproxy: %s %s -> %d", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode)
			return nil
		},
	}
}

func (c Credentials) token() (string, error) {
	if c.TokenFile != "" {
		raw, err := os.ReadFile(c.TokenFile)
		if err != nil {
			return "", err
		}
		if tok := strings.TrimSpace(string(raw)); tok != "" {
			return tok, nil
		}
		return "", errors.New("the token file is empty")
	}
	var raw []byte
	var err error
	if c.Cmd != "" {
		raw, err = exec.Command("/bin/sh", "-c", c.Cmd).Output()
	} else {
		raw, err = os.ReadFile(c.Path)
	}
	if err != nil {
		return "", err
	}
	var v struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	if v.ClaudeAiOauth.AccessToken == "" {
		return "", errors.New("no accessToken in Claude's credentials")
	}
	return v.ClaudeAiOauth.AccessToken, nil
}
