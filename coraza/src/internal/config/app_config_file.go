package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type appConfigFile struct {
	Server  appServerConfig  `json:"server"`
	Runtime appRuntimeConfig `json:"runtime"`
	Admin   appAdminConfig   `json:"admin"`
	Paths   appPathsConfig   `json:"paths"`
	Proxy   appProxyConfig   `json:"proxy"`
	CRS     appCRSConfig     `json:"crs"`
	FPTuner appFPTunerConfig `json:"fp_tuner"`
	Storage appStorageConfig `json:"storage"`
}

type appServerConfig struct {
	ListenAddr                 string `json:"listen_addr"`
	ReadTimeoutSec             int    `json:"read_timeout_sec"`
	ReadHeaderTimeoutSec       int    `json:"read_header_timeout_sec"`
	WriteTimeoutSec            int    `json:"write_timeout_sec"`
	IdleTimeoutSec             int    `json:"idle_timeout_sec"`
	MaxHeaderBytes             int    `json:"max_header_bytes"`
	MaxConcurrentRequests      int    `json:"max_concurrent_requests"`
	MaxConcurrentProxyRequests int    `json:"max_concurrent_proxy_requests"`
}

type appRuntimeConfig struct {
	GOMAXPROCS    int `json:"gomaxprocs"`
	MemoryLimitMB int `json:"memory_limit_mb"`
}

type appAdminConfig struct {
	APIBasePath           string   `json:"api_base_path"`
	UIBasePath            string   `json:"ui_base_path"`
	APIKeyPrimary         string   `json:"api_key_primary"`
	APIKeySecondary       string   `json:"api_key_secondary"`
	APIAuthDisable        bool     `json:"api_auth_disable"`
	CORSAllowedOrigins    []string `json:"cors_allowed_origins"`
	StrictOverride        bool     `json:"strict_override"`
	AllowInsecureDefaults bool     `json:"allow_insecure_defaults"`
}

type appPathsConfig struct {
	ProxyConfigFile  string `json:"proxy_config_file"`
	RulesFile        string `json:"rules_file"`
	BypassFile       string `json:"bypass_file"`
	CountryBlockFile string `json:"country_block_file"`
	RateLimitFile    string `json:"rate_limit_file"`
	BotDefenseFile   string `json:"bot_defense_file"`
	SemanticFile     string `json:"semantic_file"`
	LogFile          string `json:"log_file"`
	CRSSetupFile     string `json:"crs_setup_file"`
	CRSRulesDir      string `json:"crs_rules_dir"`
	CRSDisabledFile  string `json:"crs_disabled_file"`
}

type appProxyConfig struct {
	RollbackHistorySize int `json:"rollback_history_size"`
}

type appCRSConfig struct {
	Enable bool `json:"enable"`
}

type appFPTunerConfig struct {
	Mode             string `json:"mode"`
	Endpoint         string `json:"endpoint"`
	APIKey           string `json:"api_key"`
	Model            string `json:"model"`
	TimeoutSec       int    `json:"timeout_sec"`
	MockResponseFile string `json:"mock_response_file"`
	RequireApproval  bool   `json:"require_approval"`
	ApprovalTTLSec   int    `json:"approval_ttl_sec"`
	AuditFile        string `json:"audit_file"`
}

type appStorageConfig struct {
	Backend           string `json:"backend"`
	DBDriver          string `json:"db_driver"`
	DBDSN             string `json:"db_dsn"`
	DBPath            string `json:"db_path"`
	DBRetentionDays   int    `json:"db_retention_days"`
	DBSyncIntervalSec int    `json:"db_sync_interval_sec"`
}

func defaultAppConfigFile() appConfigFile {
	return appConfigFile{
		Server: appServerConfig{
			ListenAddr:                 ":9090",
			ReadTimeoutSec:             30,
			ReadHeaderTimeoutSec:       5,
			WriteTimeoutSec:            0,
			IdleTimeoutSec:             120,
			MaxHeaderBytes:             1 << 20,
			MaxConcurrentRequests:      0,
			MaxConcurrentProxyRequests: 0,
		},
		Runtime: appRuntimeConfig{
			GOMAXPROCS:    0,
			MemoryLimitMB: 0,
		},
		Admin: appAdminConfig{
			APIBasePath:           "/mamotama-api",
			UIBasePath:            "/mamotama-ui",
			APIKeyPrimary:         "dev-only-change-this-key-please",
			APIKeySecondary:       "",
			APIAuthDisable:        false,
			CORSAllowedOrigins:    nil,
			StrictOverride:        false,
			AllowInsecureDefaults: false,
		},
		Paths: appPathsConfig{
			ProxyConfigFile:  "conf/proxy.json",
			RulesFile:        "rules/mamotama.conf",
			BypassFile:       "conf/waf.bypass",
			CountryBlockFile: "conf/country-block.conf",
			RateLimitFile:    "conf/rate-limit.conf",
			BotDefenseFile:   "conf/bot-defense.conf",
			SemanticFile:     "conf/semantic.conf",
			LogFile:          "",
			CRSSetupFile:     "rules/crs/crs-setup.conf",
			CRSRulesDir:      "rules/crs/rules",
			CRSDisabledFile:  "conf/crs-disabled.conf",
		},
		Proxy: appProxyConfig{
			RollbackHistorySize: 8,
		},
		CRS: appCRSConfig{
			Enable: true,
		},
		FPTuner: appFPTunerConfig{
			Mode:             "mock",
			Endpoint:         "",
			APIKey:           "",
			Model:            "",
			TimeoutSec:       15,
			MockResponseFile: "conf/fp-tuner-mock-response.json",
			RequireApproval:  true,
			ApprovalTTLSec:   600,
			AuditFile:        "logs/coraza/fp-tuner-audit.ndjson",
		},
		Storage: appStorageConfig{
			Backend:           "file",
			DBDriver:          "sqlite",
			DBDSN:             "",
			DBPath:            "logs/coraza/mamotama.db",
			DBRetentionDays:   30,
			DBSyncIntervalSec: 0,
		},
	}
}

func loadAppConfigFile(path string) (appConfigFile, error) {
	cfg := defaultAppConfigFile()
	f, err := os.Open(path)
	if err != nil {
		return appConfigFile{}, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return appConfigFile{}, fmt.Errorf("decode json: %w", err)
	}
	normalizeAppConfigFile(&cfg)
	if err := validateAppConfigFile(cfg); err != nil {
		return appConfigFile{}, err
	}
	return cfg, nil
}

func normalizeAppConfigFile(cfg *appConfigFile) {
	cfg.Server.ListenAddr = strings.TrimSpace(cfg.Server.ListenAddr)
	cfg.Admin.APIBasePath = strings.TrimSpace(cfg.Admin.APIBasePath)
	cfg.Admin.UIBasePath = strings.TrimSpace(cfg.Admin.UIBasePath)
	cfg.Admin.APIKeyPrimary = strings.TrimSpace(cfg.Admin.APIKeyPrimary)
	cfg.Admin.APIKeySecondary = strings.TrimSpace(cfg.Admin.APIKeySecondary)
	cfg.FPTuner.Mode = strings.ToLower(strings.TrimSpace(cfg.FPTuner.Mode))
	cfg.FPTuner.Endpoint = strings.TrimSpace(cfg.FPTuner.Endpoint)
	cfg.FPTuner.APIKey = strings.TrimSpace(cfg.FPTuner.APIKey)
	cfg.FPTuner.Model = strings.TrimSpace(cfg.FPTuner.Model)
	cfg.Paths.ProxyConfigFile = strings.TrimSpace(cfg.Paths.ProxyConfigFile)
	cfg.Paths.RulesFile = strings.TrimSpace(cfg.Paths.RulesFile)
	cfg.Paths.BypassFile = strings.TrimSpace(cfg.Paths.BypassFile)
	cfg.Paths.CountryBlockFile = strings.TrimSpace(cfg.Paths.CountryBlockFile)
	cfg.Paths.RateLimitFile = strings.TrimSpace(cfg.Paths.RateLimitFile)
	cfg.Paths.BotDefenseFile = strings.TrimSpace(cfg.Paths.BotDefenseFile)
	cfg.Paths.SemanticFile = strings.TrimSpace(cfg.Paths.SemanticFile)
	cfg.Paths.LogFile = strings.TrimSpace(cfg.Paths.LogFile)
	cfg.Paths.CRSSetupFile = strings.TrimSpace(cfg.Paths.CRSSetupFile)
	cfg.Paths.CRSRulesDir = strings.TrimSpace(cfg.Paths.CRSRulesDir)
	cfg.Paths.CRSDisabledFile = strings.TrimSpace(cfg.Paths.CRSDisabledFile)
	cfg.Storage.Backend = strings.ToLower(strings.TrimSpace(cfg.Storage.Backend))
	cfg.Storage.DBDriver = strings.ToLower(strings.TrimSpace(cfg.Storage.DBDriver))
	cfg.Storage.DBDSN = strings.TrimSpace(cfg.Storage.DBDSN)
	cfg.Storage.DBPath = strings.TrimSpace(cfg.Storage.DBPath)
}

func validateAppConfigFile(cfg appConfigFile) error {
	if cfg.Server.ListenAddr == "" {
		return fmt.Errorf("server.listen_addr is required")
	}
	if cfg.Admin.APIBasePath == "" {
		return fmt.Errorf("admin.api_base_path is required")
	}
	if !strings.HasPrefix(cfg.Admin.APIBasePath, "/") {
		return fmt.Errorf("admin.api_base_path must start with '/'")
	}
	if cfg.Admin.APIBasePath == "/" {
		return fmt.Errorf("admin.api_base_path cannot be '/'")
	}
	if cfg.Admin.UIBasePath == "" {
		return fmt.Errorf("admin.ui_base_path is required")
	}
	if !strings.HasPrefix(cfg.Admin.UIBasePath, "/") {
		return fmt.Errorf("admin.ui_base_path must start with '/'")
	}
	if cfg.Admin.UIBasePath == "/" {
		return fmt.Errorf("admin.ui_base_path cannot be '/'")
	}
	if cfg.Admin.APIBasePath == cfg.Admin.UIBasePath {
		return fmt.Errorf("admin.api_base_path and admin.ui_base_path must be different")
	}
	if cfg.Paths.ProxyConfigFile == "" {
		return fmt.Errorf("paths.proxy_config_file is required")
	}
	if cfg.Paths.RulesFile == "" {
		return fmt.Errorf("paths.rules_file is required")
	}
	if cfg.Proxy.RollbackHistorySize < 1 || cfg.Proxy.RollbackHistorySize > 64 {
		return fmt.Errorf("proxy.rollback_history_size must be between 1 and 64")
	}
	if cfg.Server.ReadTimeoutSec < 0 || cfg.Server.ReadHeaderTimeoutSec < 0 || cfg.Server.WriteTimeoutSec < 0 || cfg.Server.IdleTimeoutSec < 0 {
		return fmt.Errorf("server timeout values must be >= 0")
	}
	if cfg.Server.MaxHeaderBytes < 0 || cfg.Server.MaxConcurrentRequests < 0 || cfg.Server.MaxConcurrentProxyRequests < 0 {
		return fmt.Errorf("server resource limits must be >= 0")
	}
	if cfg.Runtime.GOMAXPROCS < 0 || cfg.Runtime.MemoryLimitMB < 0 {
		return fmt.Errorf("runtime resource limits must be >= 0")
	}
	if cfg.Storage.Backend != "file" && cfg.Storage.Backend != "db" {
		return fmt.Errorf("storage.backend must be one of: file, db")
	}
	if cfg.Storage.DBDriver != "sqlite" && cfg.Storage.DBDriver != "mysql" {
		return fmt.Errorf("storage.db_driver must be one of: sqlite, mysql")
	}
	if cfg.Storage.DBRetentionDays < 0 {
		return fmt.Errorf("storage.db_retention_days must be >= 0")
	}
	if cfg.Storage.DBSyncIntervalSec < 0 {
		return fmt.Errorf("storage.db_sync_interval_sec must be >= 0")
	}
	if cfg.FPTuner.Mode != "mock" && cfg.FPTuner.Mode != "http" {
		return fmt.Errorf("fp_tuner.mode must be one of: mock, http")
	}
	if cfg.FPTuner.TimeoutSec < 1 || cfg.FPTuner.TimeoutSec > 300 {
		return fmt.Errorf("fp_tuner.timeout_sec must be between 1 and 300")
	}
	if cfg.FPTuner.ApprovalTTLSec < 10 || cfg.FPTuner.ApprovalTTLSec > 86400 {
		return fmt.Errorf("fp_tuner.approval_ttl_sec must be between 10 and 86400")
	}
	return nil
}
