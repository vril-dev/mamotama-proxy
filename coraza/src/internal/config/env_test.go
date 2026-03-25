package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsWeakAPIKey(t *testing.T) {
	cases := []struct {
		key  string
		weak bool
	}{
		{key: "", weak: true},
		{key: "short", weak: true},
		{key: "change-me", weak: true},
		{key: "replace-with-long-random-api-key", weak: true},
		{key: "dev-only-change-this-key-please", weak: false},
		{key: "n2H8x9fQ4mL7pRt2", weak: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.key, func(t *testing.T) {
			if got := isWeakAPIKey(tc.key); got != tc.weak {
				t.Fatalf("isWeakAPIKey(%q) = %v, want %v", tc.key, got, tc.weak)
			}
		})
	}
}

func TestTruthyFalsy(t *testing.T) {
	if !isTruthy("1") || !isTruthy("true") || !isTruthy("Yes") || !isTruthy("on") {
		t.Fatal("isTruthy() failed for truthy values")
	}
	if isTruthy("0") || isTruthy("off") || isTruthy("nope") {
		t.Fatal("isTruthy() returned true for falsy values")
	}

	if !isFalsy("0") || !isFalsy("false") || !isFalsy("NO") || !isFalsy("off") {
		t.Fatal("isFalsy() failed for falsy values")
	}
	if isFalsy("1") || isFalsy("on") || isFalsy("yes") {
		t.Fatal("isFalsy() returned true for truthy values")
	}
}

func TestParseCSV(t *testing.T) {
	got := parseCSV(" https://admin.example.com, http://localhost:5173 ,,")
	if len(got) != 2 {
		t.Fatalf("parseCSV() len=%d, want 2", len(got))
	}
	if got[0] != "https://admin.example.com" || got[1] != "http://localhost:5173" {
		t.Fatalf("parseCSV() = %#v", got)
	}
}

func TestParseStorageBackend(t *testing.T) {
	cases := []struct {
		name            string
		in              string
		legacyDBEnabled bool
		want            string
	}{
		{name: "explicit-file", in: "file", legacyDBEnabled: true, want: "file"},
		{name: "explicit-db", in: "db", legacyDBEnabled: false, want: "db"},
		{name: "legacy-fallback-db", in: "", legacyDBEnabled: true, want: "db"},
		{name: "legacy-fallback-file", in: "", legacyDBEnabled: false, want: "file"},
		{name: "invalid-fallback-file", in: "oracle", legacyDBEnabled: true, want: "file"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := parseStorageBackend(tc.in, tc.legacyDBEnabled)
			if got != tc.want {
				t.Fatalf("parseStorageBackend(%q, %v)=%q want=%q", tc.in, tc.legacyDBEnabled, got, tc.want)
			}
		})
	}
}

func TestParseDBDriver(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: "sqlite"},
		{in: "sqlite", want: "sqlite"},
		{in: "mysql", want: "mysql"},
		{in: "oracle", want: "sqlite"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in+"->"+tc.want, func(t *testing.T) {
			got := parseDBDriver(tc.in)
			if got != tc.want {
				t.Fatalf("parseDBDriver(%q)=%q want=%q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseDBSyncIntervalSec(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{in: "", want: 0},
		{in: "-1", want: 0},
		{in: "0", want: 0},
		{in: "10", want: 10},
		{in: "999999", want: 3600},
		{in: "abc", want: 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			if got := parseDBSyncIntervalSec(tc.in); got != tc.want {
				t.Fatalf("parseDBSyncIntervalSec(%q)=%d want=%d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseProxyRollbackHistorySize(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{in: "", want: 8},
		{in: "-1", want: 1},
		{in: "0", want: 1},
		{in: "1", want: 1},
		{in: "8", want: 8},
		{in: "256", want: 64},
		{in: "abc", want: 8},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			if got := parseProxyRollbackHistorySize(tc.in); got != tc.want {
				t.Fatalf("parseProxyRollbackHistorySize(%q)=%d want=%d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseServerTimeoutSec(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		def       int
		allowZero bool
		want      int
	}{
		{name: "default", in: "", def: 30, allowZero: false, want: 30},
		{name: "negative-fallback", in: "-1", def: 30, allowZero: false, want: 30},
		{name: "zero-disallowed", in: "0", def: 30, allowZero: false, want: 30},
		{name: "zero-allowed", in: "0", def: 30, allowZero: true, want: 0},
		{name: "valid", in: "15", def: 30, allowZero: false, want: 15},
		{name: "cap", in: "999999", def: 30, allowZero: false, want: 3600},
		{name: "invalid-fallback", in: "abc", def: 30, allowZero: false, want: 30},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := parseServerTimeoutSec(tc.in, tc.def, tc.allowZero); got != tc.want {
				t.Fatalf("parseServerTimeoutSec(%q,%d,%v)=%d want=%d", tc.in, tc.def, tc.allowZero, got, tc.want)
			}
		})
	}
}

func TestParseServerMaxHeaderBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{in: "", want: 1 << 20},
		{in: "1", want: 1024},
		{in: "1024", want: 1024},
		{in: "2097152", want: 2097152},
		{in: "999999999", want: 16 << 20},
		{in: "abc", want: 1 << 20},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			if got := parseServerMaxHeaderBytes(tc.in); got != tc.want {
				t.Fatalf("parseServerMaxHeaderBytes(%q)=%d want=%d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseServerConcurrency(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{in: "", want: 0},
		{in: "-1", want: 0},
		{in: "0", want: 0},
		{in: "100", want: 100},
		{in: "9999999", want: 200000},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			if got := parseServerConcurrency(tc.in); got != tc.want {
				t.Fatalf("parseServerConcurrency(%q)=%d want=%d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRuntimeCaps(t *testing.T) {
	if got := parseRuntimeGOMAXPROCS("-1"); got != 0 {
		t.Fatalf("parseRuntimeGOMAXPROCS(-1)=%d want=0", got)
	}
	if got := parseRuntimeGOMAXPROCS("5000"); got != 4096 {
		t.Fatalf("parseRuntimeGOMAXPROCS(5000)=%d want=4096", got)
	}
	if got := parseRuntimeMemoryLimitMB("-1"); got != 0 {
		t.Fatalf("parseRuntimeMemoryLimitMB(-1)=%d want=0", got)
	}
	if got := parseRuntimeMemoryLimitMB("9999999"); got != 1024*1024 {
		t.Fatalf("parseRuntimeMemoryLimitMB(9999999)=%d want=%d", got, 1024*1024)
	}
}

func TestLoadAppConfigFile(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	raw := `{
		"server": {"listen_addr": ":18090"},
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
	cfg, err := loadAppConfigFile(cfgPath)
	if err != nil {
		t.Fatalf("loadAppConfigFile returned error: %v", err)
	}
	if cfg.Server.ListenAddr != ":18090" {
		t.Fatalf("unexpected listen_addr: %s", cfg.Server.ListenAddr)
	}
	if cfg.Paths.ProxyConfigFile != "conf/proxy.json" {
		t.Fatalf("unexpected proxy_config_file: %s", cfg.Paths.ProxyConfigFile)
	}
}

func TestLoadAppConfigFileRejectsInvalid(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	raw := `{
		"server": {"listen_addr": ":9090"},
		"admin": {"api_base_path": "/", "ui_base_path": "/mamotama-ui"},
		"paths": {"proxy_config_file": "conf/proxy.json", "rules_file": "rules/mamotama.conf"},
		"proxy": {"rollback_history_size": 8},
		"fp_tuner": {"mode": "mock", "timeout_sec": 15, "approval_ttl_sec": 600},
		"storage": {"backend": "file", "db_driver": "sqlite"}
	}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadAppConfigFile(cfgPath); err == nil {
		t.Fatal("expected validation error, got nil")
	}
}
