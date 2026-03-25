package handler

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateProxyRulesRaw(t *testing.T) {
	good := `{
  "upstream_url": "http://127.0.0.1:8080",
  "dial_timeout": 5,
  "response_header_timeout": 10,
  "idle_conn_timeout": 90,
  "max_idle_conns": 100,
  "max_idle_conns_per_host": 100,
  "max_conns_per_host": 200,
  "force_http2": false,
  "disable_compression": false,
  "expect_continue_timeout": 1,
  "tls_insecure_skip_verify": false,
  "tls_client_cert": "",
  "tls_client_key": "",
  "buffer_request_body": false,
  "max_response_buffer_bytes": 0,
  "flush_interval_ms": 0,
  "health_check_path": "/healthz",
  "health_check_interval_sec": 15,
  "health_check_timeout_sec": 2
}`

	cfg, err := ValidateProxyRulesRaw(good)
	if err != nil {
		t.Fatalf("ValidateProxyRulesRaw(good): %v", err)
	}
	if cfg.UpstreamURL != "http://127.0.0.1:8080" {
		t.Fatalf("unexpected upstream: %s", cfg.UpstreamURL)
	}

	bad := strings.Replace(good, "http://127.0.0.1:8080", "ftp://127.0.0.1:8080", 1)
	if _, err := ValidateProxyRulesRaw(bad); err == nil {
		t.Fatal("expected invalid scheme error")
	}

	badPath := strings.Replace(good, `"/healthz"`, `"healthz"`, 1)
	if _, err := ValidateProxyRulesRaw(badPath); err != nil {
		t.Fatalf("health_check_path should be normalized: %v", err)
	}

	tlsBad := strings.Replace(good, `"tls_client_cert": ""`, `"tls_client_cert": "/tmp/cert.pem"`, 1)
	if _, err := ValidateProxyRulesRaw(tlsBad); err == nil {
		t.Fatal("expected mTLS pair validation error")
	}
}

func TestProxyRulesApplyAndRollback(t *testing.T) {
	tmp := t.TempDir()
	proxyPath := filepath.Join(tmp, "proxy.json")
	initial := `{
  "upstream_url": "http://127.0.0.1:8081",
  "dial_timeout": 5,
  "response_header_timeout": 10,
  "idle_conn_timeout": 90,
  "max_idle_conns": 100,
  "max_idle_conns_per_host": 100,
  "max_conns_per_host": 200,
  "force_http2": false,
  "disable_compression": false,
  "expect_continue_timeout": 1,
  "tls_insecure_skip_verify": false,
  "tls_client_cert": "",
  "tls_client_key": "",
  "buffer_request_body": false,
  "max_response_buffer_bytes": 0,
  "flush_interval_ms": 0,
  "health_check_path": "",
  "health_check_interval_sec": 15,
  "health_check_timeout_sec": 2
}`
	if err := os.WriteFile(proxyPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial proxy.json: %v", err)
	}

	if err := InitProxyRuntime(proxyPath, 2); err != nil {
		t.Fatalf("InitProxyRuntime: %v", err)
	}
	_, etag, cfg, _, depth := ProxyRulesSnapshot()
	if etag == "" {
		t.Fatal("etag should not be empty")
	}
	if depth != 0 {
		t.Fatalf("initial rollback depth=%d want=0", depth)
	}
	if cfg.UpstreamURL != "http://127.0.0.1:8081" {
		t.Fatalf("initial upstream=%s", cfg.UpstreamURL)
	}

	next := strings.Replace(initial, "127.0.0.1:8081", "127.0.0.1:8082", 1)
	next = strings.Replace(next, `"force_http2": false`, `"force_http2": true`, 1)
	newETag, newCfg, err := ApplyProxyRulesRaw(etag, next)
	if err != nil {
		t.Fatalf("ApplyProxyRulesRaw: %v", err)
	}
	if newETag == etag {
		t.Fatal("etag should change after update")
	}
	if !newCfg.ForceHTTP2 {
		t.Fatal("force_http2 should be true after apply")
	}
	if newCfg.UpstreamURL != "http://127.0.0.1:8082" {
		t.Fatalf("updated upstream=%s", newCfg.UpstreamURL)
	}

	if _, _, err := ApplyProxyRulesRaw("stale-etag", next); err == nil {
		t.Fatal("expected etag conflict")
	}

	rolledETag, rolledCfg, _, err := RollbackProxyRules()
	if err != nil {
		t.Fatalf("RollbackProxyRules: %v", err)
	}
	if rolledETag == "" {
		t.Fatal("rollback etag should not be empty")
	}
	if rolledCfg.UpstreamURL != "http://127.0.0.1:8081" {
		t.Fatalf("rolled upstream=%s", rolledCfg.UpstreamURL)
	}
	if rolledCfg.ForceHTTP2 {
		t.Fatal("force_http2 should be false after rollback")
	}
}

func TestProxyProbe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	go func() {
		_ = srv.Serve(ln)
	}()
	defer func() {
		_ = srv.Close()
	}()

	raw := `{
  "upstream_url": "http://` + ln.Addr().String() + `",
  "dial_timeout": 5,
  "response_header_timeout": 10,
  "idle_conn_timeout": 90,
  "max_idle_conns": 100,
  "max_idle_conns_per_host": 100,
  "max_conns_per_host": 200,
  "force_http2": false,
  "disable_compression": false,
  "expect_continue_timeout": 1,
  "tls_insecure_skip_verify": false,
  "tls_client_cert": "",
  "tls_client_key": "",
  "buffer_request_body": false,
  "max_response_buffer_bytes": 0,
  "flush_interval_ms": 0,
  "health_check_path": "/",
  "health_check_interval_sec": 15,
  "health_check_timeout_sec": 2
}`

	cfg, addr, latency, err := ProxyProbe(raw, 2*time.Second)
	if err != nil {
		t.Fatalf("ProxyProbe: %v", err)
	}
	if cfg.UpstreamURL == "" {
		t.Fatal("probe should return proxy config")
	}
	if addr == "" {
		t.Fatal("probe address should not be empty")
	}
	if latency < 0 {
		t.Fatalf("latency=%d", latency)
	}
}
