package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/websocket"
)

func TestServeProxyWebSocketPassthrough(t *testing.T) {
	upstream := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		defer conn.Close()

		var in string
		if err := websocket.Message.Receive(conn, &in); err != nil {
			t.Errorf("upstream receive failed: %v", err)
			return
		}
		if err := websocket.Message.Send(conn, "echo:"+in); err != nil {
			t.Errorf("upstream send failed: %v", err)
		}
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	proxyPath := filepath.Join(tmpDir, "proxy.json")
	raw := fmt.Sprintf(`{
  "upstream_url": %q,
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
}`, upstream.URL)
	if err := os.WriteFile(proxyPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write proxy config: %v", err)
	}
	if err := InitProxyRuntime(proxyPath, 2); err != nil {
		t.Fatalf("InitProxyRuntime: %v", err)
	}

	srv := httptest.NewServer(httpHandlerFunc(ServeProxy))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/echo"
	conn, err := websocket.Dial(wsURL, "", srv.URL)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()

	if err := websocket.Message.Send(conn, "hello"); err != nil {
		t.Fatalf("websocket send failed: %v", err)
	}

	var out string
	if err := websocket.Message.Receive(conn, &out); err != nil {
		t.Fatalf("websocket receive failed: %v", err)
	}
	if out != "echo:hello" {
		t.Fatalf("unexpected websocket response: %q", out)
	}
}

type httpHandlerFunc func(http.ResponseWriter, *http.Request)

func (f httpHandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f(w, r)
}
