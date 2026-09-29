// Package openai adapts the non-streaming Chat Completions protocol to model.Model.
package openai

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Config struct {
	BaseURL          string
	APIKey           string
	Model            string
	MaxResponseBytes int64
	// An injected client owns its redirect, TLS, proxy and timeout policies.
	// Configure it before New and do not mutate its transport during use.
	HTTPClient *http.Client
}

func New(cfg Config) (*Provider, error) {
	endpoint, err := completionURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Model) == "" || !utf8.ValidString(cfg.Model) {
		return nil, &ConfigError{Field: "Model"}
	}
	// Leave room for the extra byte used to detect an oversized body.
	if cfg.MaxResponseBytes <= 0 || cfg.MaxResponseBytes == 1<<63-1 {
		return nil, &ConfigError{Field: "MaxResponseBytes"}
	}
	for _, c := range cfg.APIKey {
		if c < 0x21 || c > 0x7e {
			return nil, &ConfigError{Field: "APIKey"}
		}
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{
			Transport: &http.Transport{
				Proxy:                  http.ProxyFromEnvironment,
				DialContext:            (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext,
				ForceAttemptHTTP2:      true,
				MaxIdleConns:           100,
				IdleConnTimeout:        90 * time.Second,
				MaxResponseHeaderBytes: 64 * 1024,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	clientCopy := *client
	return &Provider{endpoint: endpoint, apiKey: cfg.APIKey, model: cfg.Model, maxResponseBytes: cfg.MaxResponseBytes, client: &clientCopy}, nil
}

func completionURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || strings.Contains(base, "#") {
		return "", &ConfigError{Field: "BaseURL"}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", &ConfigError{Field: "BaseURL"}
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", &ConfigError{Field: "BaseURL"}
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", &ConfigError{Field: "BaseURL"}
		}
	}
	if u.Scheme == "http" && !loopbackHost(u.Hostname()) {
		return "", &ConfigError{Field: "BaseURL"}
	}
	return u.JoinPath("chat", "completions").String(), nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return false
	}
	return (ip.Is4() && ip.As4()[0] == 127) || ip == netip.IPv6Loopback()
}
