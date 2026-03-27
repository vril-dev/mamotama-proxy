package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustValidateProxyRulesRaw(t *testing.T, raw string) ProxyRulesConfig {
	t.Helper()
	cfg, err := ValidateProxyRulesRaw(raw)
	if err != nil {
		t.Fatalf("ValidateProxyRulesRaw: %v", err)
	}
	return cfg
}

func mustResolveProxyRouteDecision(t *testing.T, cfg ProxyRulesConfig, host string, path string) proxyRouteDecision {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://proxy.local"+path, nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.Host = host
	decision, err := resolveProxyRouteDecision(req, cfg, nil)
	if err != nil {
		t.Fatalf("resolveProxyRouteDecision: %v", err)
	}
	return decision
}

func TestValidateProxyRulesRawWithRoutes(t *testing.T) {
	raw := `{
  "upstream_url": "http://127.0.0.1:8080",
  "upstreams": [
    { "name": "svc-a", "url": "http://127.0.0.1:8081", "enabled": true }
  ],
  "routes": [
    {
      "name": "service-a",
      "priority": 10,
      "match": {
        "hosts": ["API.EXAMPLE.COM.", "*.EXAMPLE.NET."],
        "path": { "type": "prefix", "value": "/servicea/" }
      },
      "action": {
        "upstream": "svc-a",
        "path_rewrite": { "prefix": "/service-a/" },
        "request_headers": {
          "set": { "X-Service": "service-a" },
          "add": { "X-Route": "service-a" },
          "remove": ["X-Debug"]
        },
        "response_headers": {
          "set": { "x-frame-options": "DENY" },
          "remove": ["x-powered-by"]
        }
      }
    }
  ],
  "default_route": {
    "name": "fallback",
    "action": {
      "upstream": "http://127.0.0.1:8082"
    }
  }
}`

	cfg := mustValidateProxyRulesRaw(t, raw)
	if len(cfg.Routes) != 1 {
		t.Fatalf("routes=%d", len(cfg.Routes))
	}
	if cfg.Routes[0].Match.Path == nil || cfg.Routes[0].Match.Path.Value != "/servicea" {
		t.Fatalf("unexpected normalized path match: %#v", cfg.Routes[0].Match.Path)
	}
	if cfg.Routes[0].Match.Hosts[0] != "api.example.com" || cfg.Routes[0].Match.Hosts[1] != "*.example.net" {
		t.Fatalf("unexpected normalized hosts: %#v", cfg.Routes[0].Match.Hosts)
	}
	if cfg.Routes[0].Action.PathRewrite == nil || cfg.Routes[0].Action.PathRewrite.Prefix != "/service-a" {
		t.Fatalf("unexpected normalized path rewrite: %#v", cfg.Routes[0].Action.PathRewrite)
	}
	if cfg.Routes[0].Action.ResponseHeaders == nil || cfg.Routes[0].Action.ResponseHeaders.Set["X-Frame-Options"] != "DENY" {
		t.Fatalf("unexpected normalized response headers: %#v", cfg.Routes[0].Action.ResponseHeaders)
	}
	if cfg.DefaultRoute == nil || cfg.DefaultRoute.Name != "fallback" {
		t.Fatalf("unexpected default route: %#v", cfg.DefaultRoute)
	}
}

func TestProxyRouteResolutionOrderAndDryRun(t *testing.T) {
	routedRaw := `{
  "upstream_url": "http://legacy.internal:8080",
  "routes": [
    {
      "name": "service-a",
      "priority": 10,
      "match": {
        "hosts": ["api.example.com"],
        "path": { "type": "prefix", "value": "/servicea/" }
      },
      "action": {
        "upstream": "http://route.internal:8080",
        "path_rewrite": { "prefix": "/" }
      }
    }
  ],
  "default_route": {
    "name": "fallback",
    "action": {
      "upstream": "http://default.internal:8080"
    }
  }
}`
	legacyRaw := `{
  "upstream_url": "http://legacy.internal:8080",
  "routes": [
    {
      "name": "service-a",
      "priority": 10,
      "match": {
        "hosts": ["api.example.com"],
        "path": { "type": "prefix", "value": "/servicea/" }
      },
      "action": {
        "upstream": "http://route.internal:8080",
        "path_rewrite": { "prefix": "/" }
      }
    }
  ]
}`

	tests := []struct {
		name              string
		cfg               ProxyRulesConfig
		host              string
		path              string
		wantSource        string
		wantRoute         string
		wantRewrittenPath string
		wantUpstream      string
		wantUpstreamURL   string
		wantFinalURL      string
	}{
		{
			name:              "route wins over default and legacy",
			cfg:               mustValidateProxyRulesRaw(t, routedRaw),
			host:              "api.example.com",
			path:              "/servicea/users",
			wantSource:        "route",
			wantRoute:         "service-a",
			wantRewrittenPath: "/users",
			wantUpstream:      "http://route.internal:8080",
			wantUpstreamURL:   "http://route.internal:8080",
			wantFinalURL:      "http://route.internal:8080/users",
		},
		{
			name:              "default route wins when no explicit route matches",
			cfg:               mustValidateProxyRulesRaw(t, routedRaw),
			host:              "www.example.com",
			path:              "/other",
			wantSource:        "default_route",
			wantRoute:         "fallback",
			wantRewrittenPath: "/other",
			wantUpstream:      "http://default.internal:8080",
			wantUpstreamURL:   "http://default.internal:8080",
			wantFinalURL:      "http://default.internal:8080/other",
		},
		{
			name:              "legacy fallback is used when default route is absent",
			cfg:               mustValidateProxyRulesRaw(t, legacyRaw),
			host:              "www.example.com",
			path:              "/other",
			wantSource:        "legacy_upstream",
			wantRoute:         "legacy-upstream",
			wantRewrittenPath: "/other",
			wantUpstream:      "primary",
			wantUpstreamURL:   "http://legacy.internal:8080",
			wantFinalURL:      "http://legacy.internal:8080/other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := mustResolveProxyRouteDecision(t, tt.cfg, tt.host, tt.path)
			dryRun, err := proxyRouteDryRun(tt.cfg, tt.host, tt.path)
			if err != nil {
				t.Fatalf("proxyRouteDryRun: %v", err)
			}

			if got := string(decision.Source); got != tt.wantSource {
				t.Fatalf("decision source=%s want=%s", got, tt.wantSource)
			}
			if got := dryRun.Source; got != tt.wantSource {
				t.Fatalf("dry-run source=%s want=%s", got, tt.wantSource)
			}
			if got := decision.RouteName; got != tt.wantRoute {
				t.Fatalf("decision route=%s want=%s", got, tt.wantRoute)
			}
			if got := dryRun.RouteName; got != tt.wantRoute {
				t.Fatalf("dry-run route=%s want=%s", got, tt.wantRoute)
			}
			if got := decision.RewrittenPath; got != tt.wantRewrittenPath {
				t.Fatalf("decision rewritten_path=%s want=%s", got, tt.wantRewrittenPath)
			}
			if got := dryRun.RewrittenPath; got != tt.wantRewrittenPath {
				t.Fatalf("dry-run rewritten_path=%s want=%s", got, tt.wantRewrittenPath)
			}
			if got := decision.SelectedUpstream; got != tt.wantUpstream {
				t.Fatalf("decision selected_upstream=%s want=%s", got, tt.wantUpstream)
			}
			if got := dryRun.SelectedUpstream; got != tt.wantUpstream {
				t.Fatalf("dry-run selected_upstream=%s want=%s", got, tt.wantUpstream)
			}
			if got := decision.SelectedUpstreamURL; got != tt.wantUpstreamURL {
				t.Fatalf("decision selected_upstream_url=%s want=%s", got, tt.wantUpstreamURL)
			}
			if got := dryRun.SelectedUpstreamURL; got != tt.wantUpstreamURL {
				t.Fatalf("dry-run selected_upstream_url=%s want=%s", got, tt.wantUpstreamURL)
			}
			if got := finalProxyRouteURL(decision.Target, decision.RewrittenPath, decision.RewrittenRawPath); got != tt.wantFinalURL {
				t.Fatalf("decision final_url=%s want=%s", got, tt.wantFinalURL)
			}
			if got := dryRun.FinalURL; got != tt.wantFinalURL {
				t.Fatalf("dry-run final_url=%s want=%s", got, tt.wantFinalURL)
			}
		})
	}
}

func TestProxyRoutePrefixRewriteBoundaries(t *testing.T) {
	match := normalizeProxyRoutePathMatch(&ProxyRoutePathMatch{Type: "prefix", Value: "/servicea/"})
	tests := []struct {
		name          string
		originalPath  string
		rewritePrefix string
		wantPath      string
	}{
		{name: "prefix root no trailing slash", originalPath: "/servicea", rewritePrefix: "/", wantPath: "/"},
		{name: "prefix root trailing slash", originalPath: "/servicea/", rewritePrefix: "/", wantPath: "/"},
		{name: "prefix nested path to root", originalPath: "/servicea/foo", rewritePrefix: "/", wantPath: "/foo"},
		{name: "prefix preserved", originalPath: "/servicea/foo", rewritePrefix: "/servicea/", wantPath: "/servicea/foo"},
		{name: "prefix renamed", originalPath: "/servicea/foo", rewritePrefix: "/service-a/", wantPath: "/service-a/foo"},
		{name: "prefix renamed no double slash", originalPath: "/servicea/", rewritePrefix: "/service-a/", wantPath: "/service-a/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rewriteProxyRoutePath(tt.originalPath, match, tt.rewritePrefix)
			if err != nil {
				t.Fatalf("rewriteProxyRoutePath: %v", err)
			}
			if got != tt.wantPath {
				t.Fatalf("rewritten_path=%s want=%s", got, tt.wantPath)
			}
			if strings.Contains(got, "//") && got != "/" {
				t.Fatalf("rewritten_path contains double slash: %s", got)
			}
		})
	}
}

func TestProxyRoutePreservesEncodedSuffixOnRewrite(t *testing.T) {
	cfg := mustValidateProxyRulesRaw(t, `{
  "upstream_url": "http://legacy.internal:8080",
  "routes": [
    {
      "name": "service-a",
      "priority": 10,
      "match": {
        "hosts": ["api.example.com"],
        "path": { "type": "prefix", "value": "/servicea/" }
      },
      "action": {
        "upstream": "http://route.internal:8080",
        "path_rewrite": { "prefix": "/service-a/" }
      }
    }
  ]
}`)

	req, err := http.NewRequest(http.MethodGet, "http://proxy.local/servicea/%2Fetc", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.Host = "api.example.com"

	decision, err := resolveProxyRouteDecision(req, cfg, nil)
	if err != nil {
		t.Fatalf("resolveProxyRouteDecision: %v", err)
	}
	if decision.RewrittenPath != "/service-a//etc" {
		t.Fatalf("rewritten_path=%s", decision.RewrittenPath)
	}
	if decision.RewrittenRawPath != "/service-a/%2Fetc" {
		t.Fatalf("rewritten_raw_path=%s", decision.RewrittenRawPath)
	}
	if got := finalProxyRouteURL(decision.Target, decision.RewrittenPath, decision.RewrittenRawPath); got != "http://route.internal:8080/service-a/%2Fetc" {
		t.Fatalf("final_url=%s", got)
	}
}

func TestProxyRouteHostMatchBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		host     string
		want     bool
	}{
		{name: "exact host match", patterns: []string{"api.example.com"}, host: "api.example.com", want: true},
		{name: "exact host strips port, case, and trailing dot", patterns: []string{"api.example.com"}, host: "API.EXAMPLE.COM.:443", want: true},
		{name: "wildcard does not match bare suffix", patterns: []string{"*.example.com"}, host: "example.com", want: false},
		{name: "wildcard matches single label", patterns: []string{"*.example.com"}, host: "a.example.com", want: true},
		{name: "wildcard matches deeper labels", patterns: []string{"*.example.com"}, host: "a.b.example.com", want: true},
		{name: "wildcard strips port and trailing dot", patterns: []string{"*.example.com."}, host: "A.B.EXAMPLE.COM.:8443", want: true},
		{name: "empty host does not match", patterns: []string{"api.example.com"}, host: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := proxyRouteHostsMatch(normalizeProxyRouteHosts(tt.patterns), tt.host)
			if got != tt.want {
				t.Fatalf("matched=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestValidateProxyRulesRawRejectsInvalidActionUpstream(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{
			name: "unknown upstream name",
			raw: `{
  "upstream_url": "http://127.0.0.1:8080",
  "routes": [
    {
      "priority": 10,
      "action": { "upstream": "missing-upstream" }
    }
  ]
}`,
			wantErr: "must be an absolute http(s) URL or a configured upstream name",
		},
		{
			name: "unsupported upstream scheme",
			raw: `{
  "upstream_url": "http://127.0.0.1:8080",
  "routes": [
    {
      "priority": 10,
      "action": { "upstream": "ftp://127.0.0.1:21" }
    }
  ]
}`,
			wantErr: "must be an absolute http(s) URL or a configured upstream name",
		},
		{
			name: "relative upstream URL",
			raw: `{
  "upstream_url": "http://127.0.0.1:8080",
  "routes": [
    {
      "priority": 10,
      "action": { "upstream": "/relative" }
    }
  ]
}`,
			wantErr: "must be an absolute http(s) URL or a configured upstream name",
		},
		{
			name: "explicit upstream required without legacy fallback",
			raw: `{
  "routes": [
    {
      "priority": 10,
      "match": {
        "path": { "type": "prefix", "value": "/" }
      },
      "action": {}
    }
  ]
}`,
			wantErr: "routes[0].action.upstream is required when upstream_url/upstreams are not set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateProxyRulesRaw(tt.raw)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error=%q want substring=%q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateProxyRulesRawRejectsRestrictedRouteHeaders(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{
			name: "reject host set",
			raw: `{
  "upstream_url": "http://127.0.0.1:8080",
  "routes": [
    {
      "priority": 10,
      "action": {
        "request_headers": {
          "set": { "Host": "malicious.example" }
        }
      }
    }
  ]
}`,
			wantErr: "header is not allowed in route request_headers",
		},
		{
			name: "reject x-forwarded add",
			raw: `{
  "upstream_url": "http://127.0.0.1:8080",
  "routes": [
    {
      "priority": 10,
      "action": {
        "request_headers": {
          "add": { "x-forwarded-for": "1.2.3.4" }
        }
      }
    }
  ]
}`,
			wantErr: "header is not allowed in route request_headers",
		},
		{
			name: "reject hop-by-hop remove",
			raw: `{
  "upstream_url": "http://127.0.0.1:8080",
  "routes": [
    {
      "priority": 10,
      "action": {
        "request_headers": {
          "remove": ["cOnNection"]
        }
      }
    }
  ]
}`,
			wantErr: "header is not allowed in route request_headers",
		},
		{
			name: "reject content-length response set",
			raw: `{
  "upstream_url": "http://127.0.0.1:8080",
  "routes": [
    {
      "priority": 10,
      "action": {
        "response_headers": {
          "set": { "Content-Length": "1" }
        }
      }
    }
  ]
}`,
			wantErr: "header is not allowed in route response_headers",
		},
		{
			name: "reject set-cookie response remove",
			raw: `{
  "upstream_url": "http://127.0.0.1:8080",
  "routes": [
    {
      "priority": 10,
      "action": {
        "response_headers": {
          "remove": ["Set-Cookie"]
        }
      }
    }
  ]
}`,
			wantErr: "header is not allowed in route response_headers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateProxyRulesRaw(tt.raw)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error=%q want substring=%q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLegacyProxyRouteCompatibilityWithoutRoutes(t *testing.T) {
	cfg := mustValidateProxyRulesRaw(t, `{
  "upstream_url": "http://legacy.internal:8080",
  "load_balancing_strategy": "round_robin"
}`)

	decision := mustResolveProxyRouteDecision(t, cfg, "api.example.com", "/healthz")
	if got := string(decision.Source); got != "legacy_upstream" {
		t.Fatalf("source=%s", got)
	}
	if decision.RouteName != "legacy-upstream" {
		t.Fatalf("route_name=%s", decision.RouteName)
	}
	if decision.SelectedUpstream != "primary" {
		t.Fatalf("selected_upstream=%s", decision.SelectedUpstream)
	}
	if got := finalProxyRouteURL(decision.Target, decision.RewrittenPath, decision.RewrittenRawPath); got != "http://legacy.internal:8080/healthz" {
		t.Fatalf("final_url=%s", got)
	}

	dryRun, err := proxyRouteDryRun(cfg, "api.example.com", "/healthz")
	if err != nil {
		t.Fatalf("proxyRouteDryRun: %v", err)
	}
	if dryRun.Source != "legacy_upstream" {
		t.Fatalf("dry-run source=%s", dryRun.Source)
	}
	if dryRun.FinalURL != "http://legacy.internal:8080/healthz" {
		t.Fatalf("dry-run final_url=%s", dryRun.FinalURL)
	}
}

func TestServeProxyAppliesRouteRewriteAndHeaders(t *testing.T) {
	var gotPath string
	var gotSet string
	var gotAdd string
	var gotRemoved string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSet = r.Header.Get("X-Service")
		gotAdd = r.Header.Get("X-Route")
		gotRemoved = r.Header.Get("X-Debug")
		w.Header().Set("X-Upstream-Replace", "origin")
		w.Header().Add("X-Upstream-Add", "origin")
		w.Header().Set("X-Upstream-Remove", "remove-me")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	tmp := t.TempDir()
	proxyPath := filepath.Join(tmp, "proxy.json")
	raw := `{
  "upstream_url": "` + upstream.URL + `",
  "routes": [
    {
      "name": "service-a",
      "priority": 10,
      "match": {
        "hosts": ["api.example.com"],
        "path": { "type": "prefix", "value": "/servicea/" }
      },
      "action": {
        "path_rewrite": { "prefix": "/service-a/" },
        "request_headers": {
          "set": { "X-Service": "service-a" },
          "add": { "X-Route": "service-a" },
          "remove": ["X-Debug"]
        },
        "response_headers": {
          "set": { "X-Upstream-Replace": "rewritten", "X-Route-Response": "service-a" },
          "add": { "X-Upstream-Add": "added" },
          "remove": ["X-Upstream-Remove"]
        }
      }
    }
  ]
}`
	if err := os.WriteFile(proxyPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write proxy.json: %v", err)
	}
	if err := InitProxyRuntime(proxyPath, 2); err != nil {
		t.Fatalf("InitProxyRuntime: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://proxy.local/servicea/users", nil)
	req.Host = "api.example.com"
	req.Header.Set("X-Debug", "remove-me")
	decision, err := resolveProxyRouteDecision(req, currentProxyConfig(), proxyRuntimeHealth())
	if err != nil {
		t.Fatalf("resolveProxyRouteDecision: %v", err)
	}
	req = req.WithContext(withProxyRouteDecision(req.Context(), decision))

	rec := httptest.NewRecorder()
	ServeProxy(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d", rec.Code)
	}
	if gotPath != "/service-a/users" {
		t.Fatalf("path=%s", gotPath)
	}
	if gotSet != "service-a" {
		t.Fatalf("X-Service=%s", gotSet)
	}
	if gotAdd != "service-a" {
		t.Fatalf("X-Route=%s", gotAdd)
	}
	if gotRemoved != "" {
		t.Fatalf("X-Debug=%s", gotRemoved)
	}
	if got := rec.Header().Get("X-Upstream-Replace"); got != "rewritten" {
		t.Fatalf("X-Upstream-Replace=%s", got)
	}
	if got := rec.Header().Values("X-Upstream-Add"); len(got) != 2 || got[0] != "origin" || got[1] != "added" {
		t.Fatalf("X-Upstream-Add=%v", got)
	}
	if got := rec.Header().Get("X-Upstream-Remove"); got != "" {
		t.Fatalf("X-Upstream-Remove=%s", got)
	}
	if got := rec.Header().Get("X-Route-Response"); got != "service-a" {
		t.Fatalf("X-Route-Response=%s", got)
	}
}
