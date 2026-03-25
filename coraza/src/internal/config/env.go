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
	ProxyConfigFile          string
	UIBasePath               string
	ProxyRollbackMax         int
	ListenAddr               string
	ServerReadTimeout        time.Duration
	ServerReadHeaderTimeout  time.Duration
	ServerWriteTimeout       time.Duration
	ServerIdleTimeout        time.Duration
	ServerMaxHeaderBytes     int
	ServerMaxConcurrentReqs  int
	ServerMaxConcurrentProxy int
	RuntimeGOMAXPROCS        int
	RuntimeMemoryLimitMB     int
	RulesFile                string
	BypassFile               string
	CountryBlockFile         string
	RateLimitFile            string
	BotDefenseFile           string
	SemanticFile             string
	LogFile                  string
	StrictOverride           bool
	APIBasePath              string
	APIKeyPrimary            string
	APIKeySecondary          string
	APIAuthDisable           bool
	APICORSOrigins           []string
	CRSEnable                bool
	CRSSetupFile             string
	CRSRulesDir              string
	CRSDisabledFile          string

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
)

func LoadEnv() {
	_ = godotenv.Load()

	ProxyConfigFile = strings.TrimSpace(os.Getenv("WAF_PROXY_CONFIG_FILE"))
	if ProxyConfigFile == "" {
		ProxyConfigFile = "conf/proxy.json"
	}
	UIBasePath = "/mamotama-ui"
	ProxyRollbackMax = parseProxyRollbackHistorySize(os.Getenv("WAF_PROXY_ROLLBACK_HISTORY_SIZE"))
	ListenAddr = parseListenAddr(os.Getenv("WAF_LISTEN_ADDR"))
	ServerReadTimeout = time.Duration(parseServerTimeoutSec(os.Getenv("WAF_SERVER_READ_TIMEOUT_SEC"), 30, false)) * time.Second
	ServerReadHeaderTimeout = time.Duration(parseServerTimeoutSec(os.Getenv("WAF_SERVER_READ_HEADER_TIMEOUT_SEC"), 5, false)) * time.Second
	ServerWriteTimeout = time.Duration(parseServerTimeoutSec(os.Getenv("WAF_SERVER_WRITE_TIMEOUT_SEC"), 0, true)) * time.Second
	ServerIdleTimeout = time.Duration(parseServerTimeoutSec(os.Getenv("WAF_SERVER_IDLE_TIMEOUT_SEC"), 120, false)) * time.Second
	ServerMaxHeaderBytes = parseServerMaxHeaderBytes(os.Getenv("WAF_SERVER_MAX_HEADER_BYTES"))
	ServerMaxConcurrentReqs = parseServerConcurrency(os.Getenv("WAF_SERVER_MAX_CONCURRENT_REQUESTS"))
	ServerMaxConcurrentProxy = parseServerConcurrency(os.Getenv("WAF_SERVER_MAX_CONCURRENT_PROXY_REQUESTS"))
	RuntimeGOMAXPROCS = parseRuntimeGOMAXPROCS(os.Getenv("WAF_RUNTIME_GOMAXPROCS"))
	RuntimeMemoryLimitMB = parseRuntimeMemoryLimitMB(os.Getenv("WAF_RUNTIME_MEMORY_LIMIT_MB"))
	RulesFile = os.Getenv("WAF_RULES_FILE")
	BypassFile = os.Getenv("WAF_BYPASS_FILE")
	CountryBlockFile = strings.TrimSpace(os.Getenv("WAF_COUNTRY_BLOCK_FILE"))
	if CountryBlockFile == "" {
		CountryBlockFile = "conf/country-block.conf"
	}
	RateLimitFile = strings.TrimSpace(os.Getenv("WAF_RATE_LIMIT_FILE"))
	if RateLimitFile == "" {
		RateLimitFile = "conf/rate-limit.conf"
	}
	BotDefenseFile = strings.TrimSpace(os.Getenv("WAF_BOT_DEFENSE_FILE"))
	if BotDefenseFile == "" {
		BotDefenseFile = "conf/bot-defense.conf"
	}
	SemanticFile = strings.TrimSpace(os.Getenv("WAF_SEMANTIC_FILE"))
	if SemanticFile == "" {
		SemanticFile = "conf/semantic.conf"
	}
	LogFile = os.Getenv("WAF_LOG_FILE")
	StrictOverride = os.Getenv("WAF_STRICT_OVERRIDE") == "true"

	APIBasePath = os.Getenv("WAF_API_BASEPATH")
	if APIBasePath == "" {
		APIBasePath = "/mamotama-api"
	}
	if !strings.HasPrefix(APIBasePath, "/") {
		APIBasePath = "/" + APIBasePath
	}
	if APIBasePath == "/" {
		log.Fatal("WAF_API_BASEPATH cannot be root path '/'")
	}

	APIKeyPrimary = strings.TrimSpace(os.Getenv("WAF_API_KEY_PRIMARY"))
	APIKeySecondary = strings.TrimSpace(os.Getenv("WAF_API_KEY_SECONDARY"))
	APIAuthDisable = isTruthy(os.Getenv("WAF_API_AUTH_DISABLE"))
	APICORSOrigins = parseCSV(os.Getenv("WAF_API_CORS_ALLOWED_ORIGINS"))

	CRSEnable = !isFalsy(os.Getenv("WAF_CRS_ENABLE"))
	CRSSetupFile = strings.TrimSpace(os.Getenv("WAF_CRS_SETUP_FILE"))
	if CRSSetupFile == "" {
		CRSSetupFile = "rules/crs/crs-setup.conf"
	}
	CRSRulesDir = strings.TrimSpace(os.Getenv("WAF_CRS_RULES_DIR"))
	if CRSRulesDir == "" {
		CRSRulesDir = "rules/crs/rules"
	}
	CRSDisabledFile = strings.TrimSpace(os.Getenv("WAF_CRS_DISABLED_FILE"))
	if CRSDisabledFile == "" {
		CRSDisabledFile = "conf/crs-disabled.conf"
	}

	FPTunerMode = strings.ToLower(strings.TrimSpace(os.Getenv("WAF_FP_TUNER_MODE")))
	if FPTunerMode == "" {
		FPTunerMode = "mock"
	}
	FPTunerEndpoint = strings.TrimSpace(os.Getenv("WAF_FP_TUNER_ENDPOINT"))
	FPTunerAPIKey = strings.TrimSpace(os.Getenv("WAF_FP_TUNER_API_KEY"))
	FPTunerModel = strings.TrimSpace(os.Getenv("WAF_FP_TUNER_MODEL"))
	FPTunerMockResponseFile = strings.TrimSpace(os.Getenv("WAF_FP_TUNER_MOCK_RESPONSE_FILE"))
	if FPTunerMockResponseFile == "" {
		FPTunerMockResponseFile = "conf/fp-tuner-mock-response.json"
	}
	timeoutSec := parseIntDefault(os.Getenv("WAF_FP_TUNER_TIMEOUT_SEC"), 15)
	if timeoutSec < 1 || timeoutSec > 300 {
		timeoutSec = 15
	}
	FPTunerTimeout = time.Duration(timeoutSec) * time.Second
	FPTunerRequireApproval = !isFalsy(os.Getenv("WAF_FP_TUNER_REQUIRE_APPROVAL"))
	approvalTTLSec := parseIntDefault(os.Getenv("WAF_FP_TUNER_APPROVAL_TTL_SEC"), 600)
	if approvalTTLSec < 10 || approvalTTLSec > 86400 {
		approvalTTLSec = 600
	}
	FPTunerApprovalTTL = time.Duration(approvalTTLSec) * time.Second
	FPTunerAuditFile = strings.TrimSpace(os.Getenv("WAF_FP_TUNER_AUDIT_FILE"))
	if FPTunerAuditFile == "" {
		FPTunerAuditFile = "logs/coraza/fp-tuner-audit.ndjson"
	}
	legacyDBEnabled := isTruthy(os.Getenv("WAF_DB_ENABLED"))
	StorageBackend = parseStorageBackend(os.Getenv("WAF_STORAGE_BACKEND"), legacyDBEnabled)
	DBEnabled = StorageBackend == "db"
	DBDriver = parseDBDriver(os.Getenv("WAF_DB_DRIVER"))
	DBDSN = strings.TrimSpace(os.Getenv("WAF_DB_DSN"))
	DBPath = strings.TrimSpace(os.Getenv("WAF_DB_PATH"))
	if DBPath == "" {
		DBPath = "logs/coraza/mamotama.db"
	}
	DBRetentionDays = parseIntDefault(os.Getenv("WAF_DB_RETENTION_DAYS"), 30)
	if DBRetentionDays < 0 {
		DBRetentionDays = 0
	}
	if DBRetentionDays > 3650 {
		DBRetentionDays = 3650
	}
	dbSyncSec := parseDBSyncIntervalSec(os.Getenv("WAF_DB_SYNC_INTERVAL_SEC"))
	DBSyncInterval = time.Duration(dbSyncSec) * time.Second

	AllowInsecureDefaults = isTruthy(os.Getenv("WAF_ALLOW_INSECURE_DEFAULTS"))
	enforceSecureDefaults()
}

func enforceSecureDefaults() {
	if AllowInsecureDefaults {
		log.Println("[SECURITY][WARN] WAF_ALLOW_INSECURE_DEFAULTS enabled; weak bootstrap settings are allowed")
		return
	}

	if APIAuthDisable {
		log.Fatal("[SECURITY] WAF_API_AUTH_DISABLE is enabled; set WAF_ALLOW_INSECURE_DEFAULTS=1 only for local testing")
	}
	if isWeakAPIKey(APIKeyPrimary) {
		log.Fatal("[SECURITY] WAF_API_KEY_PRIMARY is weak; set a random key with 16+ chars")
	}
	if APIKeySecondary != "" && isWeakAPIKey(APIKeySecondary) {
		log.Fatal("[SECURITY] WAF_API_KEY_SECONDARY is weak; set a random key with 16+ chars or leave it empty")
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
		log.Printf("[CONFIG][WARN] unsupported WAF_STORAGE_BACKEND=%q, fallback=file", s)
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
		log.Printf("[CONFIG][WARN] unsupported WAF_DB_DRIVER=%q, fallback=sqlite", s)
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
