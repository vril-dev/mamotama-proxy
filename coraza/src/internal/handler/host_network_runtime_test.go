package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"mamotama/internal/config"
)

func TestHostNetworkHandlersPersistConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	raw := `{
		"server": {"listen_addr": ":9090"},
		"host_network": {"enabled": false, "backend": "sysctl", "sysctl_profile": "baseline"},
		"admin": {
			"api_base_path": "/mamotama-api",
			"ui_base_path": "/mamotama-ui",
			"api_key_primary": "very-strong-random-api-key-12345"
		},
		"paths": {
			"proxy_config_file": "conf/proxy.json",
			"rules_file": "rules/mamotama.conf"
		},
		"proxy": {"rollback_history_size": 8},
		"fp_tuner": {"mode": "mock", "timeout_sec": 15, "approval_ttl_sec": 600},
		"storage": {"backend": "file", "db_driver": "sqlite"}
	}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config.ConfigFile = cfgPath
	if err := InitHostNetworkRuntime(config.HostNetworkConfig{}); err != nil {
		t.Fatalf("init host network runtime: %v", err)
	}

	r := gin.New()
	r.GET("/host-network", GetHostNetwork)
	r.POST("/host-network:validate", ValidateHostNetwork)
	r.PUT("/host-network", PutHostNetwork)

	reqGet := httptest.NewRequest(http.MethodGet, "/host-network", nil)
	recGet := httptest.NewRecorder()
	r.ServeHTTP(recGet, reqGet)
	if recGet.Code != http.StatusOK {
		t.Fatalf("unexpected get status: %d", recGet.Code)
	}
	var getBody map[string]any
	if err := json.Unmarshal(recGet.Body.Bytes(), &getBody); err != nil {
		t.Fatalf("decode get body: %v", err)
	}
	etag, _ := getBody["etag"].(string)
	if etag == "" {
		t.Fatal("expected etag")
	}

	putBody := []byte(`{"raw":"{\"enabled\":true,\"backend\":\"sysctl\",\"sysctl_profile\":\"baseline\",\"sysctls\":{\"net.core.somaxconn\":\"8192\"},\"state_file\":\"/var/lib/mamotama-proxy/host_network_state.json\"}"}`)
	reqPut := httptest.NewRequest(http.MethodPut, "/host-network", bytes.NewReader(putBody))
	reqPut.Header.Set("Content-Type", "application/json")
	reqPut.Header.Set("If-Match", etag)
	recPut := httptest.NewRecorder()
	r.ServeHTTP(recPut, reqPut)
	if recPut.Code != http.StatusOK {
		t.Fatalf("unexpected put status: %d body=%s", recPut.Code, recPut.Body.String())
	}
	if !strings.Contains(recPut.Body.String(), `"restart_required":true`) {
		t.Fatalf("expected restart_required=true: %s", recPut.Body.String())
	}

	persisted, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if !strings.Contains(string(persisted), `"host_network"`) {
		t.Fatalf("expected host_network in config: %s", string(persisted))
	}
	if !strings.Contains(string(persisted), `"net.core.somaxconn": "8192"`) {
		t.Fatalf("expected persisted sysctl override: %s", string(persisted))
	}
}
