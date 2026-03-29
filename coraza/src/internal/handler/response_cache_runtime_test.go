package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"mamotama/internal/cacheconf"
)

func TestServeProxyWithCacheHitAndClear(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var upstreamRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRequests.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello proxy cache"))
	}))
	defer upstream.Close()

	proxyCfgPath := filepath.Join(t.TempDir(), "proxy.json")
	if err := os.WriteFile(proxyCfgPath, []byte(`{"upstream_url":`+strconv.Quote(upstream.URL)+`}`), 0o600); err != nil {
		t.Fatalf("write proxy config: %v", err)
	}
	if err := InitProxyRuntime(proxyCfgPath, 8); err != nil {
		t.Fatalf("init proxy runtime: %v", err)
	}

	cacheStoreDir := t.TempDir()
	cacheStoreCfgPath := filepath.Join(t.TempDir(), "cache-store.json")
	if err := os.WriteFile(cacheStoreCfgPath, []byte(`{"enabled":true,"store_dir":`+strconv.Quote(cacheStoreDir)+`,"max_bytes":1048576}`), 0o600); err != nil {
		t.Fatalf("write cache store config: %v", err)
	}
	if err := InitResponseCacheRuntime(cacheStoreCfgPath); err != nil {
		t.Fatalf("init response cache runtime: %v", err)
	}

	rs, err := cacheconf.LoadFromString(`ALLOW prefix=/static methods=GET,HEAD ttl=60 vary=Accept-Encoding`)
	if err != nil {
		t.Fatalf("load cache rules: %v", err)
	}
	cacheconf.Set(rs)

	r := gin.New()
	r.NoRoute(ProxyHandler)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req1, _ := http.NewRequest(http.MethodGet, srv.URL+"/static/app.js", nil)
	req1.Header.Set("Accept-Encoding", "gzip")
	res1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	defer res1.Body.Close()
	if got := res1.Header.Get(proxyResponseCacheHeader); got != "MISS" {
		t.Fatalf("unexpected first cache header: %q", got)
	}

	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/static/app.js", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	defer res2.Body.Close()
	if got := res2.Header.Get(proxyResponseCacheHeader); got != "HIT" {
		t.Fatalf("unexpected second cache header: %q", got)
	}
	if got := upstreamRequests.Load(); got != 1 {
		t.Fatalf("unexpected upstream count before clear: %d", got)
	}

	clearResult, err := ClearResponseCache()
	if err != nil {
		t.Fatalf("clear cache: %v", err)
	}
	if clearResult.ClearedEntries != 1 {
		t.Fatalf("unexpected cleared entries: %d", clearResult.ClearedEntries)
	}

	req3, _ := http.NewRequest(http.MethodGet, srv.URL+"/static/app.js", nil)
	req3.Header.Set("Accept-Encoding", "gzip")
	res3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("third request failed: %v", err)
	}
	defer res3.Body.Close()
	if got := res3.Header.Get(proxyResponseCacheHeader); got != "MISS" {
		t.Fatalf("unexpected third cache header: %q", got)
	}
	if got := upstreamRequests.Load(); got != 2 {
		t.Fatalf("unexpected upstream count after clear: %d", got)
	}
}
