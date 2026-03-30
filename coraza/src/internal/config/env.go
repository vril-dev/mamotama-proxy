package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

var (
	ConfigFile                string
	ProxyConfigFile           string
	CacheStoreFile            string
	UIBasePath                string
	ProxyRollbackMax          int
	ListenAddr                string
	ServerReadTimeout         time.Duration
	ServerReadHeaderTimeout   time.Duration
	ServerWriteTimeout        time.Duration
	ServerIdleTimeout         time.Duration
	ServerMaxHeaderBytes      int
	ServerMaxConcurrentReqs   int
	ServerMaxConcurrentProxy  int
	ServerTLSEnabled          bool
	ServerTLSCertFile         string
	ServerTLSKeyFile          string
	ServerTLSMinVersion       string
	ServerTLSRedirectHTTP     bool
	ServerTLSHTTPRedirectAddr string
	ServerTLSACMEEnabled      bool
	ServerTLSACMEEmail        string
	ServerTLSACMEDomains      []string
	ServerTLSACMECacheDir     string
	ServerTLSACMEStaging      bool
	RuntimeGOMAXPROCS         int
	RuntimeMemoryLimitMB      int
	HostNetworkCfg            HostNetworkConfig
	RulesFile                 string
	BypassFile                string
	CountryBlockFile          string
	RateLimitFile             string
	BotDefenseFile            string
	SemanticFile              string
	NotificationFile          string
	IPReputationFile          string
	LogFile                   string
	StrictOverride            bool
	APIBasePath               string
	AdminExternalMode         string
	AdminTrustedCIDRs         []string
	AdminTrustForwardedFor    bool
	APIKeyPrimary             string
	APIKeySecondary           string
	APIAuthDisable            bool
	APICORSOrigins            []string
	AdminRateLimitEnabled     bool
	AdminRateLimitRPS         int
	AdminRateLimitBurst       int
	AdminRateLimitStatusCode  int
	AdminRateLimitRetryAfter  int
	CRSEnable                 bool
	CRSSetupFile              string
	CRSRulesDir               string
	CRSDisabledFile           string

	AllowInsecureDefaults bool

	FPTunerMode             string
	FPTunerEndpoint         string
	FPTunerAPIKey           string
	FPTunerModel            string
	FPTunerTimeout          time.Duration
	FPTunerMockResponseFile string
	FPTunerRequireApproval  bool
	FPTunerApprovalTTL      time.Duration
	FPTunerAuditFile        string

	StorageBackend  string
	DBEnabled       bool
	DBDriver        string
	DBDSN           string
	DBPath          string
	DBRetentionDays int
	DBSyncInterval  time.Duration
	FileRotateBytes int64
	FileMaxBytes    int64
	FileRetention   time.Duration

	TracingEnabled      bool
	TracingServiceName  string
	TracingOTLPEndpoint string
	TracingInsecure     bool
	TracingSampleRatio  float64
)

func LoadEnv() {
	_ = godotenv.Load()
	ConfigFile = strings.TrimSpace(os.Getenv("WAF_CONFIG_FILE"))
	if ConfigFile == "" {
		ConfigFile = "conf/config.json"
	}
	cfg, err := loadAppConfigFile(ConfigFile)
	if err != nil {
		log.Fatalf("[CONFIG][FATAL] load %s: %v", ConfigFile, err)
	}
	applyAppConfig(cfg)
	enforceSecureDefaults()
}

func applyAppConfig(cfg appConfigFile) {
	ProxyConfigFile = strings.TrimSpace(cfg.Paths.ProxyConfigFile)
	if ProxyConfigFile == "" {
		ProxyConfigFile = "conf/proxy.json"
	}
	CacheStoreFile = strings.TrimSpace(cfg.Paths.CacheStoreFile)
	if CacheStoreFile == "" {
		CacheStoreFile = "conf/cache-store.json"
	}
	UIBasePath = strings.TrimSpace(cfg.Admin.UIBasePath)
	if UIBasePath == "" {
		UIBasePath = "/mamotama-ui"
	}

	ProxyRollbackMax = parseProxyRollbackHistorySize(strconv.Itoa(cfg.Proxy.RollbackHistorySize))
	ListenAddr = parseListenAddr(cfg.Server.ListenAddr)
	ServerReadTimeout = time.Duration(parseServerTimeoutSec(strconv.Itoa(cfg.Server.ReadTimeoutSec), 30, false)) * time.Second
	ServerReadHeaderTimeout = time.Duration(parseServerTimeoutSec(strconv.Itoa(cfg.Server.ReadHeaderTimeoutSec), 5, false)) * time.Second
	ServerWriteTimeout = time.Duration(parseServerTimeoutSec(strconv.Itoa(cfg.Server.WriteTimeoutSec), 0, true)) * time.Second
	ServerIdleTimeout = time.Duration(parseServerTimeoutSec(strconv.Itoa(cfg.Server.IdleTimeoutSec), 120, false)) * time.Second
	ServerMaxHeaderBytes = parseServerMaxHeaderBytes(strconv.Itoa(cfg.Server.MaxHeaderBytes))
	ServerMaxConcurrentReqs = parseServerConcurrency(strconv.Itoa(cfg.Server.MaxConcurrentRequests))
	ServerMaxConcurrentProxy = parseServerConcurrency(strconv.Itoa(cfg.Server.MaxConcurrentProxyRequests))
	ServerTLSEnabled = cfg.Server.TLS.Enabled
	ServerTLSCertFile = strings.TrimSpace(cfg.Server.TLS.CertFile)
	ServerTLSKeyFile = strings.TrimSpace(cfg.Server.TLS.KeyFile)
	ServerTLSMinVersion = normalizeServerTLSMinVersion(cfg.Server.TLS.MinVersion)
	ServerTLSRedirectHTTP = cfg.Server.TLS.RedirectHTTP
	ServerTLSHTTPRedirectAddr = strings.TrimSpace(cfg.Server.TLS.HTTPRedirectAddr)
	if ServerTLSHTTPRedirectAddr != "" {
		ServerTLSHTTPRedirectAddr = parseListenAddr(ServerTLSHTTPRedirectAddr)
	}
	ServerTLSACMEEnabled = cfg.Server.TLS.ACME.Enabled
	ServerTLSACMEEmail = strings.TrimSpace(cfg.Server.TLS.ACME.Email)
	ServerTLSACMEDomains = append([]string(nil), cfg.Server.TLS.ACME.Domains...)
	ServerTLSACMECacheDir = strings.TrimSpace(cfg.Server.TLS.ACME.CacheDir)
	ServerTLSACMEStaging = cfg.Server.TLS.ACME.Staging
	RuntimeGOMAXPROCS = parseRuntimeGOMAXPROCS(strconv.Itoa(cfg.Runtime.GOMAXPROCS))
	RuntimeMemoryLimitMB = parseRuntimeMemoryLimitMB(strconv.Itoa(cfg.Runtime.MemoryLimitMB))
	HostNetworkCfg = NormalizeHostNetworkConfig(cfg.HostNetwork)

	RulesFile = strings.TrimSpace(cfg.Paths.RulesFile)
	if RulesFile == "" {
		RulesFile = "rules/mamotama.conf"
	}
	BypassFile = strings.TrimSpace(cfg.Paths.BypassFile)
	if BypassFile == "" {
		BypassFile = "conf/waf.bypass"
	}
	CountryBlockFile = strings.TrimSpace(cfg.Paths.CountryBlockFile)
	if CountryBlockFile == "" {
		CountryBlockFile = "conf/country-block.conf"
	}
	RateLimitFile = strings.TrimSpace(cfg.Paths.RateLimitFile)
	if RateLimitFile == "" {
		RateLimitFile = "conf/rate-limit.conf"
	}
	BotDefenseFile = strings.TrimSpace(cfg.Paths.BotDefenseFile)
	if BotDefenseFile == "" {
		BotDefenseFile = "conf/bot-defense.conf"
	}
	SemanticFile = strings.TrimSpace(cfg.Paths.SemanticFile)
	if SemanticFile == "" {
		SemanticFile = "conf/semantic.conf"
	}
	NotificationFile = strings.TrimSpace(cfg.Paths.NotificationFile)
	if NotificationFile == "" {
		NotificationFile = "conf/notifications.conf"
	}
	IPReputationFile = strings.TrimSpace(cfg.Paths.IPReputationFile)
	if IPReputationFile == "" {
		IPReputationFile = "conf/ip-reputation.conf"
	}
	LogFile = strings.TrimSpace(cfg.Paths.LogFile)

	StrictOverride = cfg.Admin.StrictOverride
	APIBasePath = strings.TrimSpace(cfg.Admin.APIBasePath)
	if APIBasePath == "" {
		APIBasePath = "/mamotama-api"
	}
	if !strings.HasPrefix(APIBasePath, "/") {
		APIBasePath = "/" + APIBasePath
	}
	if APIBasePath == "/" {
		log.Fatal("api_base_path cannot be root path '/'")
	}
	AdminExternalMode = strings.TrimSpace(cfg.Admin.ExternalMode)
	AdminTrustedCIDRs = append([]string(nil), cfg.Admin.TrustedCIDRs...)
	AdminTrustForwardedFor = cfg.Admin.TrustForwardedFor
	APIKeyPrimary = strings.TrimSpace(cfg.Admin.APIKeyPrimary)
	APIKeySecondary = strings.TrimSpace(cfg.Admin.APIKeySecondary)
	APIAuthDisable = cfg.Admin.APIAuthDisable
	AdminRateLimitEnabled = cfg.Admin.RateLimit.Enabled
	AdminRateLimitRPS = cfg.Admin.RateLimit.RPS
	AdminRateLimitBurst = cfg.Admin.RateLimit.Burst
	AdminRateLimitStatusCode = cfg.Admin.RateLimit.StatusCode
	AdminRateLimitRetryAfter = cfg.Admin.RateLimit.RetryAfterSeconds
	APICORSOrigins = make([]string, 0, len(cfg.Admin.CORSAllowedOrigins))
	for _, origin := range cfg.Admin.CORSAllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			APICORSOrigins = append(APICORSOrigins, origin)
		}
	}

	CRSEnable = cfg.CRS.Enable
	CRSSetupFile = strings.TrimSpace(cfg.Paths.CRSSetupFile)
	if CRSSetupFile == "" {
		CRSSetupFile = "rules/crs/crs-setup.conf"
	}
	CRSRulesDir = strings.TrimSpace(cfg.Paths.CRSRulesDir)
	if CRSRulesDir == "" {
		CRSRulesDir = "rules/crs/rules"
	}
	CRSDisabledFile = strings.TrimSpace(cfg.Paths.CRSDisabledFile)
	if CRSDisabledFile == "" {
		CRSDisabledFile = "conf/crs-disabled.conf"
	}

	FPTunerMode = strings.ToLower(strings.TrimSpace(cfg.FPTuner.Mode))
	if FPTunerMode == "" {
		FPTunerMode = "mock"
	}
	FPTunerEndpoint = strings.TrimSpace(cfg.FPTuner.Endpoint)
	FPTunerAPIKey = strings.TrimSpace(cfg.FPTuner.APIKey)
	FPTunerModel = strings.TrimSpace(cfg.FPTuner.Model)
	FPTunerMockResponseFile = strings.TrimSpace(cfg.FPTuner.MockResponseFile)
	if FPTunerMockResponseFile == "" {
		FPTunerMockResponseFile = "conf/fp-tuner-mock-response.json"
	}
	timeoutSec := cfg.FPTuner.TimeoutSec
	if timeoutSec < 1 || timeoutSec > 300 {
		timeoutSec = 15
	}
	FPTunerTimeout = time.Duration(timeoutSec) * time.Second
	FPTunerRequireApproval = cfg.FPTuner.RequireApproval
	approvalTTLSec := cfg.FPTuner.ApprovalTTLSec
	if approvalTTLSec < 10 || approvalTTLSec > 86400 {
		approvalTTLSec = 600
	}
	FPTunerApprovalTTL = time.Duration(approvalTTLSec) * time.Second
	FPTunerAuditFile = strings.TrimSpace(cfg.FPTuner.AuditFile)
	if FPTunerAuditFile == "" {
		FPTunerAuditFile = "logs/coraza/fp-tuner-audit.ndjson"
	}

	StorageBackend = parseStorageBackend(cfg.Storage.Backend, false)
	DBEnabled = StorageBackend == "db"
	DBDriver = parseDBDriver(cfg.Storage.DBDriver)
	DBDSN = strings.TrimSpace(cfg.Storage.DBDSN)
	DBPath = strings.TrimSpace(cfg.Storage.DBPath)
	if DBPath == "" {
		DBPath = "logs/coraza/mamotama.db"
	}
	DBRetentionDays = cfg.Storage.DBRetentionDays
	if DBRetentionDays < 0 {
		DBRetentionDays = 0
	}
	if DBRetentionDays > 3650 {
		DBRetentionDays = 3650
	}
	dbSyncSec := parseDBSyncIntervalSec(strconv.Itoa(cfg.Storage.DBSyncIntervalSec))
	DBSyncInterval = time.Duration(dbSyncSec) * time.Second
	FileRotateBytes = cfg.Storage.FileRotateBytes
	FileMaxBytes = cfg.Storage.FileMaxBytes
	FileRetention = time.Duration(cfg.Storage.FileRetentionDays) * 24 * time.Hour

	AllowInsecureDefaults = cfg.Admin.AllowInsecureDefaults

	TracingEnabled = cfg.Observability.Tracing.Enabled
	TracingServiceName = strings.TrimSpace(cfg.Observability.Tracing.ServiceName)
	TracingOTLPEndpoint = strings.TrimSpace(cfg.Observability.Tracing.OTLPEndpoint)
	TracingInsecure = cfg.Observability.Tracing.Insecure
	TracingSampleRatio = cfg.Observability.Tracing.SampleRatio
}

func enforceSecureDefaults() {
	if AllowInsecureDefaults {
		log.Println("[SECURITY][WARN] admin.allow_insecure_defaults enabled; weak bootstrap settings are allowed")
		return
	}

	if APIAuthDisable {
		log.Fatal("[SECURITY] admin.api_auth_disable is enabled; set admin.allow_insecure_defaults=true only for local testing")
	}
	if isWeakAPIKey(APIKeyPrimary) {
		log.Fatal("[SECURITY] admin.api_key_primary is weak; set a random key with 16+ chars")
	}
	if APIKeySecondary != "" && isWeakAPIKey(APIKeySecondary) {
		log.Fatal("[SECURITY] admin.api_key_secondary is weak; set a random key with 16+ chars or leave it empty")
	}
}

func isWeakAPIKey(v string) bool {
	trimmed := strings.TrimSpace(v)
	s := strings.ToLower(trimmed)
	if s == "" || len(trimmed) < 16 {
		return true
	}

	weak := map[string]struct{}{
		"change-me":                        {},
		"changeme":                         {},
		"replace-with-long-random-api-key": {},
		"replace-me":                       {},
		"example":                          {},
		"test":                             {},
	}
	_, ok := weak[s]
	return ok
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func isFalsy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}

func parseCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		out = append(out, s)
	}

	return out
}

func parseIntDefault(v string, d int) int {
	s := strings.TrimSpace(v)
	if s == "" {
		return d
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return n
}

func parseStorageBackend(v string, legacyDBEnabled bool) string {
	s := strings.ToLower(strings.TrimSpace(v))
	switch s {
	case "file", "db":
		return s
	case "":
		if legacyDBEnabled {
			return "db"
		}
		return "file"
	default:
		log.Printf("[CONFIG][WARN] unsupported storage.backend=%q, fallback=file", s)
		return "file"
	}
}

func parseDBDriver(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	switch s {
	case "":
		return "sqlite"
	case "sqlite", "mysql":
		return s
	default:
		log.Printf("[CONFIG][WARN] unsupported storage.db_driver=%q, fallback=sqlite", s)
		return "sqlite"
	}
}

func parseDBSyncIntervalSec(v string) int {
	n := parseIntDefault(v, 0)
	if n < 0 {
		return 0
	}
	if n > 3600 {
		return 3600
	}
	return n
}

func parseListenAddr(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return ":9090"
	}
	if strings.HasPrefix(s, ":") {
		return s
	}
	if _, err := strconv.Atoi(s); err == nil {
		return ":" + s
	}
	return s
}

func parseProxyRollbackHistorySize(v string) int {
	n := parseIntDefault(v, 8)
	if n < 1 {
		return 1
	}
	if n > 64 {
		return 64
	}
	return n
}

func parseServerTimeoutSec(v string, def int, allowZero bool) int {
	n := parseIntDefault(v, def)
	if n < 0 {
		return def
	}
	if n == 0 && !allowZero {
		return def
	}
	if n > 3600 {
		return 3600
	}
	return n
}

func parseServerMaxHeaderBytes(v string) int {
	n := parseIntDefault(v, 1<<20)
	if n < 1024 {
		return 1024
	}
	if n > 16<<20 {
		return 16 << 20
	}
	return n
}

func parseServerConcurrency(v string) int {
	n := parseIntDefault(v, 0)
	if n < 0 {
		return 0
	}
	if n > 200000 {
		return 200000
	}
	return n
}

func parseRuntimeGOMAXPROCS(v string) int {
	n := parseIntDefault(v, 0)
	if n < 0 {
		return 0
	}
	if n > 4096 {
		return 4096
	}
	return n
}

func parseRuntimeMemoryLimitMB(v string) int {
	n := parseIntDefault(v, 0)
	if n < 0 {
		return 0
	}
	if n > 1024*1024 {
		return 1024 * 1024
	}
	return n
}
