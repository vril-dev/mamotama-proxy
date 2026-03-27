package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

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
        "hosts": ["api.example.com", "*.example.net"],
        "path": { "type": "prefix", "value": "/servicea/" }
      },
      "action": {
        "upstream": "svc-a",
        "path_rewrite": { "prefix": "/service-a/" },
        "request_headers": {
          "set": { "X-Service": "service-a" },
          "add": { "X-Route": "service-a" },
          "remove": ["X-Debug"]
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

	cfg, err := ValidateProxyRulesRaw(raw)
	if err != nil {
		t.Fatalf("ValidateProxyRulesRaw(routes): %v", err)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("routes=%d", len(cfg.Routes))
	}
	if cfg.Routes[0].Match.Path == nil || cfg.Routes[0].Match.Path.Value != "/servicea" {
		t.Fatalf("unexpected normalized path match: %#v", cfg.Routes[0].Match.Path)
	}
	if cfg.Routes[0].Action.PathRewrite == nil || cfg.Routes[0].Action.PathRewrite.Prefix != "/service-a" {
		t.Fatalf("unexpected normalized path rewrite: %#v", cfg.Routes[0].Action.PathRewrite)
	}
	if cfg.DefaultRoute == nil || cfg.DefaultRoute.Name != "fallback" {
		t.Fatalf("unexpected default route: %#v", cfg.DefaultRoute)
	}
}

func TestValidateProxyRulesRawRejectsInvalidRouteHeader(t *testing.T) {
	raw := `{
  "upstream_url": "http://127.0.0.1:8080",
  "routes": [
    {
      "priority": 10,
      "match": { "path": { "type": "prefix", "value": "/servicea" } },
      "action": {
        "request_headers": {
          "set": { "Host": "malicious.example" }
        }
      }
    }
  ]
}`

	if _, err := ValidateProxyRulesRaw(raw); err == nil {
		t.Fatal("expected restricted header validation error")
	}
}

func TestProxyRouteDryRun(t *testing.T) {
	raw := `{
  "upstream_url": "http://legacy.internal:8080",
  "upstreams": [
    { "name": "svc-a", "url": "http://sv3.internal:8080", "enabled": true }
  ],
  "routes": [
    {
      "name": "service-a",
      "priority": 10,
      "match": {
        "hosts": ["api.example.com"],
        "path": { "type": "prefix", "value": "/servicea/" }
      },
      "action": {
        "upstream": "svc-a",
        "path_rewrite": { "prefix": "/service-a/" }
      }
    }
  ],
  "default_route": {
    "name": "fallback",
    "action": {
      "upstream": "http://fallback.internal:8080"
    }
  }
}`

	cfg, err := ValidateProxyRulesRaw(raw)
	if err != nil {
		t.Fatalf("ValidateProxyRulesRaw: %v", err)
	}

	result, err := proxyRouteDryRun(cfg, "api.example.com", "/servicea/users")
	if err != nil {
		t.Fatalf("proxyRouteDryRun: %v", err)
	}
	if result.RouteName != "service-a" {
		t.Fatalf("route=%s", result.RouteName)
	}
	if result.Source != "route" {
		t.Fatalf("source=%s", result.Source)
	}
	if result.RewrittenPath != "/service-a/users" {
		t.Fatalf("rewritten_path=%s", result.RewrittenPath)
	}
	if result.SelectedUpstream != "svc-a" {
		t.Fatalf("selected_upstream=%s", result.SelectedUpstream)
	}
	if result.FinalURL != "http://sv3.internal:8080/service-a/users" {
		t.Fatalf("final_url=%s", result.FinalURL)
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
}
