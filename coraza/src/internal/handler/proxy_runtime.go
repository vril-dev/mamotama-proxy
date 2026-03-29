package handler

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"

	"mamotama/internal/bypassconf"
	"mamotama/internal/observability"
)

const (
	defaultProxyDialTimeoutSec           = 5
	defaultProxyResponseHeaderTimeoutSec = 10
	defaultProxyIdleConnTimeoutSec       = 90
	defaultProxyExpectContinueSec        = 1
	defaultProxyMaxIdleConns             = 100
	defaultProxyMaxIdleConnsPerHost      = 100
	defaultProxyMaxConnsPerHost          = 200
	defaultProxyHealthCheckIntervalSec   = 15
	defaultProxyHealthCheckTimeoutSec    = 2

	proxyConfigBlobKey = "proxy_rules"
)

type ProxyRulesConfig struct {
	UpstreamURL                    string             `json:"upstream_url"`
	Upstreams                      []ProxyUpstream    `json:"upstreams,omitempty"`
	LoadBalancingStrategy          string             `json:"load_balancing_strategy,omitempty"`
	HashPolicy                     string             `json:"hash_policy,omitempty"`
	HashKey                        string             `json:"hash_key,omitempty"`
	Routes                         []ProxyRoute       `json:"routes,omitempty"`
	DefaultRoute                   *ProxyDefaultRoute `json:"default_route,omitempty"`
	DialTimeout                    int                `json:"dial_timeout"`
	ResponseHeaderTimeout          int                `json:"response_header_timeout"`
	IdleConnTimeout                int                `json:"idle_conn_timeout"`
	MaxIdleConns                   int                `json:"max_idle_conns"`
	MaxIdleConnsPerHost            int                `json:"max_idle_conns_per_host"`
	MaxConnsPerHost                int                `json:"max_conns_per_host"`
	ForceHTTP2                     bool               `json:"force_http2"`
	DisableCompression             bool               `json:"disable_compression"`
	ExpectContinueTimeout          int                `json:"expect_continue_timeout"`
	TLSInsecureSkipVerify          bool               `json:"tls_insecure_skip_verify"`
	TLSClientCert                  string             `json:"tls_client_cert"`
	TLSClientKey                   string             `json:"tls_client_key"`
	RetryAttempts                  int                `json:"retry_attempts,omitempty"`
	RetryBackoffMS                 int                `json:"retry_backoff_ms,omitempty"`
	RetryPerTryTimeoutMS           int                `json:"retry_per_try_timeout_ms,omitempty"`
	RetryStatusCodes               []int              `json:"retry_status_codes,omitempty"`
	RetryMethods                   []string           `json:"retry_methods,omitempty"`
	PassiveHealthEnabled           bool               `json:"passive_health_enabled,omitempty"`
	PassiveFailureThreshold        int                `json:"passive_failure_threshold,omitempty"`
	PassiveUnhealthyStatusCodes    []int              `json:"passive_unhealthy_status_codes,omitempty"`
	CircuitBreakerEnabled          bool               `json:"circuit_breaker_enabled,omitempty"`
	CircuitBreakerOpenSec          int                `json:"circuit_breaker_open_sec,omitempty"`
	CircuitBreakerHalfOpenRequests int                `json:"circuit_breaker_half_open_requests,omitempty"`

	BufferRequestBody      bool  `json:"buffer_request_body"`
	MaxResponseBufferBytes int64 `json:"max_response_buffer_bytes"`
	FlushIntervalMS        int   `json:"flush_interval_ms"`

	HealthCheckPath     string `json:"health_check_path"`
	HealthCheckInterval int    `json:"health_check_interval_sec"`
	HealthCheckTimeout  int    `json:"health_check_timeout_sec"`
	ErrorHTMLFile       string `json:"error_html_file"`
	ErrorRedirectURL    string `json:"error_redirect_url"`
}

type ProxyUpstream struct {
	Name    string `json:"name,omitempty"`
	URL     string `json:"url"`
	Weight  int    `json:"weight,omitempty"`
	Enabled bool   `json:"enabled"`
}

type proxyRulesPreparedUpdate struct {
	cfg    ProxyRulesConfig
	target *url.URL
	raw    string
	etag   string
	errRes proxyErrorResponse
}

type proxyRulesConflictError struct {
	CurrentETag string
}

func (e proxyRulesConflictError) Error() string {
	return "conflict"
}

type proxyRollbackEntry struct {
	Raw       string `json:"raw"`
	Timestamp string `json:"timestamp"`
}

type proxyRuntime struct {
	mu            sync.RWMutex
	configPath    string
	raw           string
	etag          string
	cfg           ProxyRulesConfig
	target        *url.URL
	reverseProxy  *httputil.ReverseProxy
	transport     *dynamicProxyTransport
	health        *upstreamHealthMonitor
	errRes        proxyErrorResponse
	rollbackMax   int
	rollbackStack []proxyRollbackEntry
}

var (
	proxyRuntimeMu sync.RWMutex
	proxyRt        *proxyRuntime
)

func InitProxyRuntime(configPath string, rollbackMax int) error {
	path := strings.TrimSpace(configPath)
	if path == "" {
		return fmt.Errorf("proxy config path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read proxy config (%s): %w", path, err)
	}
	prepared, err := prepareProxyRulesRaw(string(raw))
	if err != nil {
		return fmt.Errorf("invalid proxy config (%s): %w", path, err)
	}

	health := newUpstreamHealthMonitor(prepared.cfg)
	transport, err := newDynamicProxyTransport(prepared.cfg, health)
	if err != nil {
		return fmt.Errorf("build proxy transport: %w", err)
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			target := currentProxyTarget()
			decision, ok := proxyRouteDecisionFromContext(pr.In.Context())
			if ok && decision.Target != nil {
				target = decision.Target
			}
			if target == nil {
				target, _ = proxyPrimaryTarget(currentProxyConfig())
			}
			rewrittenPath := pr.In.URL.Path
			rewrittenRawPath := pr.In.URL.RawPath
			if ok {
				rewrittenPath = decision.RewrittenPath
				rewrittenRawPath = decision.RewrittenRawPath
			}
			rewriteProxyOutgoingURL(pr.Out, target, rewrittenPath, rewrittenRawPath)
			pr.SetXForwarded()
			outboundHost := pr.In.Host
			if ok && strings.TrimSpace(decision.RewrittenHost) != "" {
				outboundHost = decision.RewrittenHost
			}
			pr.Out.Host = outboundHost
			if ok {
				applyProxyRouteHeaders(pr.Out.Header, decision.RequestHeaderOps)
				if decision.HealthKey != "" {
					pr.Out = pr.Out.WithContext(context.WithValue(pr.Out.Context(), ctxKeySelectedUpstream, decision.HealthKey))
				}
			}
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			evt := map[string]any{
				"ts":       time.Now().UTC().Format(time.RFC3339Nano),
				"service":  "coraza",
				"level":    "ERROR",
				"event":    "proxy_error",
				"path":     requestPath(r),
				"trace_id": observability.TraceIDFromContext(r.Context()),
				"ip":       requestRemoteIP(r),
				"status":   http.StatusBadGateway,
				"error":    err.Error(),
			}
			appendProxyRouteLogFields(evt, r)
			emitJSONLog(evt)
			currentProxyErrorResponse().Write(w, r)
			log.Printf("[PROXY][ERROR] upstream unavailable method=%s path=%s err=%v", r.Method, r.URL.Path, err)
		},
	}
	rp.ModifyResponse = onProxyResponse
	rp.FlushInterval = time.Duration(prepared.cfg.FlushIntervalMS) * time.Millisecond

	rt := &proxyRuntime{
		configPath:    path,
		raw:           prepared.raw,
		etag:          prepared.etag,
		cfg:           prepared.cfg,
		target:        prepared.target,
		reverseProxy:  rp,
		transport:     transport,
		errRes:        prepared.errRes,
		rollbackMax:   clampProxyRollbackMax(rollbackMax),
		rollbackStack: make([]proxyRollbackEntry, 0, clampProxyRollbackMax(rollbackMax)),
	}
	rt.health = health

	proxyRuntimeMu.Lock()
	proxyRt = rt
	proxyRuntimeMu.Unlock()

	emitProxyConfigApplied("proxy transport initialized", prepared.cfg)
	emitProxyTLSInsecureWarning(prepared.cfg)
	return nil
}

func proxyRuntimeInstance() *proxyRuntime {
	proxyRuntimeMu.RLock()
	defer proxyRuntimeMu.RUnlock()
	return proxyRt
}

func currentProxyTarget() *url.URL {
	rt := proxyRuntimeInstance()
	if rt == nil {
		return nil
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.target == nil {
		return nil
	}
	out := *rt.target
	return &out
}

func currentProxyConfig() ProxyRulesConfig {
	rt := proxyRuntimeInstance()
	if rt == nil {
		return normalizeProxyRulesConfig(ProxyRulesConfig{})
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.cfg
}

func currentProxyErrorResponse() proxyErrorResponse {
	rt := proxyRuntimeInstance()
	if rt == nil {
		resp, _ := newProxyErrorResponse(ProxyRulesConfig{})
		return resp
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.errRes
}

func ServeProxy(w http.ResponseWriter, r *http.Request) {
	rt := proxyRuntimeInstance()
	if rt == nil || rt.reverseProxy == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"proxy runtime is not initialized"}`))
		return
	}
	rt.reverseProxy.ServeHTTP(w, r)
}

func ProxyRulesSnapshot() (raw string, etag string, cfg ProxyRulesConfig, health upstreamHealthStatus, rollbackDepth int) {
	rt := proxyRuntimeInstance()
	if rt == nil {
		return "", "", normalizeProxyRulesConfig(ProxyRulesConfig{}), upstreamHealthStatus{Status: "disabled"}, 0
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	health = upstreamHealthStatus{Status: "disabled"}
	if rt.health != nil {
		health = rt.health.Snapshot()
	}
	return rt.raw, rt.etag, rt.cfg, health, len(rt.rollbackStack)
}

func ValidateProxyRulesRaw(raw string) (ProxyRulesConfig, error) {
	prepared, err := prepareProxyRulesRaw(raw)
	if err != nil {
		return ProxyRulesConfig{}, err
	}
	return prepared.cfg, nil
}

func ApplyProxyRulesRaw(ifMatch string, raw string) (string, ProxyRulesConfig, error) {
	rt := proxyRuntimeInstance()
	if rt == nil {
		return "", ProxyRulesConfig{}, fmt.Errorf("proxy runtime is not initialized")
	}
	prepared, err := prepareProxyRulesRaw(raw)
	if err != nil {
		return "", ProxyRulesConfig{}, err
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	if ifMatch = strings.TrimSpace(ifMatch); ifMatch != "" && ifMatch != rt.etag {
		return "", ProxyRulesConfig{}, proxyRulesConflictError{CurrentETag: rt.etag}
	}

	prevRaw := rt.raw
	prevCfg := rt.cfg
	prevETag := rt.etag
	prevTarget := rt.target

	if err := persistProxyConfigRaw(rt.configPath, prepared.raw); err != nil {
		return "", ProxyRulesConfig{}, err
	}
	if err := upsertProxyConfigBlob([]byte(prepared.raw), prepared.etag); err != nil {
		_ = persistProxyConfigRaw(rt.configPath, prevRaw)
		return "", ProxyRulesConfig{}, err
	}
	if err := rt.transport.Update(prepared.cfg); err != nil {
		_ = persistProxyConfigRaw(rt.configPath, prevRaw)
		_ = upsertProxyConfigBlob([]byte(prevRaw), prevETag)
		return "", ProxyRulesConfig{}, err
	}

	rt.raw = prepared.raw
	rt.etag = prepared.etag
	rt.cfg = prepared.cfg
	rt.target = prepared.target
	rt.errRes = prepared.errRes
	rt.reverseProxy.FlushInterval = time.Duration(prepared.cfg.FlushIntervalMS) * time.Millisecond
	if rt.health != nil {
		rt.health.Update(prepared.cfg)
	}
	rt.pushRollbackLocked(proxyRollbackEntry{Raw: prevRaw, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)})

	emitProxyConfigApplied("proxy rules updated", prepared.cfg)
	emitProxyTLSInsecureWarning(prepared.cfg)
	if !proxyURLSame(prevTarget, prepared.target) {
		log.Printf("[PROXY][INFO] upstream changed from=%s to=%s", safeProxyURL(prevCfg.UpstreamURL), safeProxyURL(prepared.cfg.UpstreamURL))
	}

	return rt.etag, rt.cfg, nil
}

func RollbackProxyRules() (string, ProxyRulesConfig, proxyRollbackEntry, error) {
	rt := proxyRuntimeInstance()
	if rt == nil {
		return "", ProxyRulesConfig{}, proxyRollbackEntry{}, fmt.Errorf("proxy runtime is not initialized")
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	if len(rt.rollbackStack) == 0 {
		return "", ProxyRulesConfig{}, proxyRollbackEntry{}, fmt.Errorf("no rollback snapshot")
	}
	entry := rt.rollbackStack[len(rt.rollbackStack)-1]
	rt.rollbackStack = rt.rollbackStack[:len(rt.rollbackStack)-1]

	prepared, err := prepareProxyRulesRaw(entry.Raw)
	if err != nil {
		rt.pushRollbackLocked(entry)
		return "", ProxyRulesConfig{}, proxyRollbackEntry{}, err
	}

	prevRaw := rt.raw
	prevCfg := rt.cfg
	prevETag := rt.etag
	prevTarget := rt.target

	if err := persistProxyConfigRaw(rt.configPath, prepared.raw); err != nil {
		rt.pushRollbackLocked(entry)
		return "", ProxyRulesConfig{}, proxyRollbackEntry{}, err
	}
	if err := upsertProxyConfigBlob([]byte(prepared.raw), prepared.etag); err != nil {
		_ = persistProxyConfigRaw(rt.configPath, prevRaw)
		rt.pushRollbackLocked(entry)
		return "", ProxyRulesConfig{}, proxyRollbackEntry{}, err
	}
	if err := rt.transport.Update(prepared.cfg); err != nil {
		_ = persistProxyConfigRaw(rt.configPath, prevRaw)
		_ = upsertProxyConfigBlob([]byte(prevRaw), prevETag)
		rt.pushRollbackLocked(entry)
		return "", ProxyRulesConfig{}, proxyRollbackEntry{}, err
	}

	rt.raw = prepared.raw
	rt.etag = prepared.etag
	rt.cfg = prepared.cfg
	rt.target = prepared.target
	rt.errRes = prepared.errRes
	rt.reverseProxy.FlushInterval = time.Duration(prepared.cfg.FlushIntervalMS) * time.Millisecond
	if rt.health != nil {
		rt.health.Update(prepared.cfg)
	}

	emitProxyConfigApplied("proxy rules rollback applied", prepared.cfg)
	emitProxyTLSInsecureWarning(prepared.cfg)
	if !proxyURLSame(prevTarget, prepared.target) {
		log.Printf("[PROXY][INFO] upstream changed by rollback from=%s to=%s", safeProxyURL(prevCfg.UpstreamURL), safeProxyURL(prepared.cfg.UpstreamURL))
	}
	return rt.etag, rt.cfg, entry, nil
}

func SyncProxyStorage() error {
	rt := proxyRuntimeInstance()
	if rt == nil {
		return nil
	}

	rawFile, _, err := readFileMaybe(rt.configPath)
	if err != nil {
		return err
	}
	store := getLogsStatsStore()
	if store == nil {
		return nil
	}
	blobRaw, blobETag, found, err := store.GetConfigBlob(proxyConfigBlobKey)
	if err != nil {
		return err
	}

	if found {
		prepared, err := prepareProxyRulesRaw(string(blobRaw))
		if err != nil {
			return err
		}
		rt.mu.Lock()
		defer rt.mu.Unlock()

		curRaw := rt.raw
		curETag := rt.etag
		if !bytes.Equal(rawFile, blobRaw) {
			if err := persistProxyConfigRaw(rt.configPath, prepared.raw); err != nil {
				return err
			}
		}
		if strings.TrimSpace(blobETag) == "" {
			blobETag = prepared.etag
			if err := store.UpsertConfigBlob(proxyConfigBlobKey, []byte(prepared.raw), blobETag, time.Now().UTC()); err != nil {
				return err
			}
		}
		if err := rt.transport.Update(prepared.cfg); err != nil {
			_ = persistProxyConfigRaw(rt.configPath, curRaw)
			_ = upsertProxyConfigBlob([]byte(curRaw), curETag)
			return err
		}
		rt.raw = prepared.raw
		rt.etag = prepared.etag
		rt.cfg = prepared.cfg
		rt.target = prepared.target
		rt.reverseProxy.FlushInterval = time.Duration(prepared.cfg.FlushIntervalMS) * time.Millisecond
		if rt.health != nil {
			rt.health.Update(prepared.cfg)
		}
		return nil
	}

	if len(rawFile) == 0 {
		return fmt.Errorf("proxy config file is empty: %s", rt.configPath)
	}
	prepared, err := prepareProxyRulesRaw(string(rawFile))
	if err != nil {
		return err
	}
	return store.UpsertConfigBlob(proxyConfigBlobKey, []byte(prepared.raw), prepared.etag, time.Now().UTC())
}

func ProxyProbe(raw string, timeout time.Duration) (ProxyRulesConfig, string, int64, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if timeout < 100*time.Millisecond {
		timeout = 100 * time.Millisecond
	}
	if timeout > 10*time.Second {
		timeout = 10 * time.Second
	}

	var cfg ProxyRulesConfig
	if strings.TrimSpace(raw) == "" {
		_, _, cfg, _, _ = ProxyRulesSnapshot()
	} else {
		var err error
		cfg, err = ValidateProxyRulesRaw(raw)
		if err != nil {
			return ProxyRulesConfig{}, "", 0, err
		}
	}

	address, latencyMS, err := probeProxyUpstream(cfg, timeout)
	return cfg, address, latencyMS, err
}

func prepareProxyRulesRaw(raw string) (proxyRulesPreparedUpdate, error) {
	cfg, target, errRes, err := parseProxyRulesRaw(raw)
	if err != nil {
		return proxyRulesPreparedUpdate{}, err
	}
	normalizedRaw := mustJSON(cfg)
	return proxyRulesPreparedUpdate{
		cfg:    cfg,
		target: target,
		raw:    normalizedRaw,
		etag:   bypassconf.ComputeETag([]byte(normalizedRaw)),
		errRes: errRes,
	}, nil
}

func parseProxyRulesRaw(raw string) (ProxyRulesConfig, *url.URL, proxyErrorResponse, error) {
	var in ProxyRulesConfig
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("invalid json")
	}
	return normalizeAndValidateProxyRules(in)
}

func normalizeAndValidateProxyRules(in ProxyRulesConfig) (ProxyRulesConfig, *url.URL, proxyErrorResponse, error) {
	cfg := normalizeProxyRulesConfig(in)

	if cfg.DialTimeout <= 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("dial_timeout must be > 0")
	}
	if cfg.ResponseHeaderTimeout <= 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("response_header_timeout must be > 0")
	}
	if cfg.IdleConnTimeout <= 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("idle_conn_timeout must be > 0")
	}
	if cfg.MaxIdleConns <= 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("max_idle_conns must be > 0")
	}
	if cfg.MaxIdleConnsPerHost <= 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("max_idle_conns_per_host must be > 0")
	}
	if cfg.MaxConnsPerHost <= 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("max_conns_per_host must be > 0")
	}
	if cfg.ExpectContinueTimeout <= 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("expect_continue_timeout must be > 0")
	}
	if cfg.MaxResponseBufferBytes < 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("max_response_buffer_bytes must be >= 0")
	}
	if cfg.FlushIntervalMS < 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("flush_interval_ms must be >= 0")
	}
	if cfg.RetryAttempts < 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("retry_attempts must be >= 0")
	}
	if cfg.RetryBackoffMS < 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("retry_backoff_ms must be >= 0")
	}
	if cfg.RetryPerTryTimeoutMS < 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("retry_per_try_timeout_ms must be >= 0")
	}
	if cfg.PassiveFailureThreshold < 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("passive_failure_threshold must be >= 0")
	}
	if cfg.CircuitBreakerOpenSec < 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("circuit_breaker_open_sec must be >= 0")
	}
	if cfg.CircuitBreakerHalfOpenRequests < 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("circuit_breaker_half_open_requests must be >= 0")
	}
	if cfg.HealthCheckInterval <= 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("health_check_interval_sec must be > 0")
	}
	if cfg.HealthCheckTimeout <= 0 {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("health_check_timeout_sec must be > 0")
	}
	if cfg.HealthCheckPath != "" && !strings.HasPrefix(cfg.HealthCheckPath, "/") {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("health_check_path must start with '/'")
	}
	if cfg.ErrorHTMLFile != "" && cfg.ErrorRedirectURL != "" {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("error_html_file and error_redirect_url are mutually exclusive")
	}
	if err := validateProxyHashPolicy(cfg.HashPolicy, cfg.HashKey, "hash_policy"); err != nil {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, err
	}
	if err := validateProxyRetryStatusCodes(cfg.RetryStatusCodes, "retry_status_codes"); err != nil {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, err
	}
	if err := validateProxyRetryMethods(cfg.RetryMethods, "retry_methods"); err != nil {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, err
	}
	if err := validateProxyRetryConfiguration(cfg); err != nil {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, err
	}
	if err := validateProxyPassiveCircuitConfiguration(cfg); err != nil {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, err
	}
	if (cfg.TLSClientCert != "" || cfg.TLSClientKey != "") && !proxyRulesHasHTTPSUpstream(cfg) {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, fmt.Errorf("tls_client_cert and tls_client_key require at least one https upstream")
	}
	if err := validateProxyRoutes(cfg); err != nil {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, err
	}
	target, err := proxyPrimaryTarget(cfg)
	if err != nil {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, err
	}
	if _, err := buildProxyTLSClientConfig(cfg); err != nil {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, err
	}
	errRes, err := newProxyErrorResponse(cfg)
	if err != nil {
		return ProxyRulesConfig{}, nil, proxyErrorResponse{}, err
	}
	if len(cfg.Upstreams) == 0 {
		cfg.UpstreamURL = target.String()
	}
	return cfg, target, errRes, nil
}

func normalizeProxyRulesConfig(in ProxyRulesConfig) ProxyRulesConfig {
	out := in
	if out.DialTimeout == 0 {
		out.DialTimeout = defaultProxyDialTimeoutSec
	}
	if out.ResponseHeaderTimeout == 0 {
		out.ResponseHeaderTimeout = defaultProxyResponseHeaderTimeoutSec
	}
	if out.IdleConnTimeout == 0 {
		out.IdleConnTimeout = defaultProxyIdleConnTimeoutSec
	}
	if out.MaxIdleConns == 0 {
		out.MaxIdleConns = defaultProxyMaxIdleConns
	}
	if out.MaxIdleConnsPerHost == 0 {
		out.MaxIdleConnsPerHost = defaultProxyMaxIdleConnsPerHost
	}
	if out.MaxConnsPerHost == 0 {
		out.MaxConnsPerHost = defaultProxyMaxConnsPerHost
	}
	if out.ExpectContinueTimeout == 0 {
		out.ExpectContinueTimeout = defaultProxyExpectContinueSec
	}
	out.TLSClientCert = strings.TrimSpace(out.TLSClientCert)
	out.TLSClientKey = strings.TrimSpace(out.TLSClientKey)
	out.ErrorHTMLFile = strings.TrimSpace(out.ErrorHTMLFile)
	out.ErrorRedirectURL = strings.TrimSpace(out.ErrorRedirectURL)
	out.LoadBalancingStrategy = normalizeProxyLoadBalancingStrategy(out.LoadBalancingStrategy)
	out.HashPolicy = normalizeProxyHashPolicy(out.HashPolicy)
	out.HashKey = strings.TrimSpace(out.HashKey)
	out.RetryStatusCodes = normalizeProxyStatusCodeList(out.RetryStatusCodes)
	out.RetryMethods = normalizeProxyMethodList(out.RetryMethods)
	out.PassiveUnhealthyStatusCodes = normalizeProxyStatusCodeList(out.PassiveUnhealthyStatusCodes)
	out.UpstreamURL = strings.TrimSpace(out.UpstreamURL)
	out.Upstreams = normalizeProxyUpstreams(out.Upstreams)
	out.Routes = normalizeProxyRoutes(out.Routes)
	out.DefaultRoute = normalizeProxyDefaultRoute(out.DefaultRoute)
	out.HealthCheckPath = normalizeProxyHealthCheckPath(out.HealthCheckPath)
	if out.HealthCheckInterval == 0 {
		out.HealthCheckInterval = defaultProxyHealthCheckIntervalSec
	}
	if out.HealthCheckTimeout == 0 {
		out.HealthCheckTimeout = defaultProxyHealthCheckTimeoutSec
	}
	return out
}

func normalizeProxyHealthCheckPath(v string) string {
	x := strings.TrimSpace(v)
	if x == "" {
		return ""
	}
	if !strings.HasPrefix(x, "/") {
		x = "/" + x
	}
	return x
}

func normalizeProxyLoadBalancingStrategy(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "round_robin":
		return "round_robin"
	case "least_conn":
		return "least_conn"
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func normalizeProxyUpstreams(in []ProxyUpstream) []ProxyUpstream {
	if len(in) == 0 {
		return nil
	}
	out := make([]ProxyUpstream, 0, len(in))
	enabledCount := 0
	for i, upstream := range in {
		next := upstream
		next.Name = strings.TrimSpace(next.Name)
		next.URL = strings.TrimSpace(next.URL)
		if next.Weight <= 0 {
			next.Weight = 1
		}
		if next.Name == "" {
			next.Name = fmt.Sprintf("upstream-%d", i+1)
		}
		if next.Enabled {
			enabledCount++
		}
		out = append(out, next)
	}
	if enabledCount == 0 {
		for i := range out {
			out[i].Enabled = true
		}
	}
	return out
}

func parseProxyUpstreamURL(field, raw string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%s parse error: %w", field, err)
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("%s must include scheme and host", field)
	}
	scheme := strings.ToLower(strings.TrimSpace(target.Scheme))
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("%s scheme must be http or https", field)
	}
	return target, nil
}

func proxyPrimaryTarget(cfg ProxyRulesConfig) (*url.URL, error) {
	if len(cfg.Upstreams) == 0 {
		cfg.UpstreamURL = strings.TrimSpace(cfg.UpstreamURL)
		if cfg.UpstreamURL == "" {
			if target, ok, err := proxyRouteFallbackTarget(cfg); err != nil {
				return nil, err
			} else if ok {
				return target, nil
			}
			return nil, fmt.Errorf("upstream_url is required")
		}
		target, err := parseProxyUpstreamURL("upstream_url", cfg.UpstreamURL)
		if err != nil {
			return nil, err
		}
		return target, nil
	}
	var firstEnabled *url.URL
	for i, upstream := range cfg.Upstreams {
		target, err := parseProxyUpstreamURL(fmt.Sprintf("upstreams[%d].url", i), upstream.URL)
		if err != nil {
			return nil, err
		}
		if upstream.Weight <= 0 {
			return nil, fmt.Errorf("upstreams[%d].weight must be > 0", i)
		}
		if upstream.Enabled && firstEnabled == nil {
			firstEnabled = target
		}
	}
	if firstEnabled != nil {
		return firstEnabled, nil
	}
	if target, ok, err := proxyRouteFallbackTarget(cfg); err != nil {
		return nil, err
	} else if ok {
		return target, nil
	}
	return nil, fmt.Errorf("at least one upstream must be enabled")
}

func proxyRulesHasHTTPSUpstream(cfg ProxyRulesConfig) bool {
	if len(cfg.Upstreams) == 0 {
		target, err := url.Parse(strings.TrimSpace(cfg.UpstreamURL))
		if err == nil && strings.EqualFold(target.Scheme, "https") {
			return true
		}
	}
	for _, upstream := range cfg.Upstreams {
		if !upstream.Enabled {
			continue
		}
		target, err := url.Parse(strings.TrimSpace(upstream.URL))
		if err == nil && strings.EqualFold(target.Scheme, "https") {
			return true
		}
	}
	if cfg.DefaultRoute != nil {
		if target, ok := proxyDirectRouteTarget(cfg.DefaultRoute.Action.Upstream); ok && strings.EqualFold(target.Scheme, "https") {
			return true
		}
	}
	for _, route := range cfg.Routes {
		if target, ok := proxyDirectRouteTarget(route.Action.Upstream); ok && strings.EqualFold(target.Scheme, "https") {
			return true
		}
		if target, ok := proxyDirectRouteTarget(route.Action.CanaryUpstream); ok && strings.EqualFold(target.Scheme, "https") {
			return true
		}
	}
	if cfg.DefaultRoute != nil {
		if target, ok := proxyDirectRouteTarget(cfg.DefaultRoute.Action.CanaryUpstream); ok && strings.EqualFold(target.Scheme, "https") {
			return true
		}
	}
	return false
}

func persistProxyConfigRaw(path string, raw string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("proxy config path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return bypassconf.AtomicWriteWithBackup(path, []byte(raw))
}

func upsertProxyConfigBlob(raw []byte, etag string) error {
	store := getLogsStatsStore()
	if store == nil {
		return nil
	}
	if strings.TrimSpace(etag) == "" {
		etag = bypassconf.ComputeETag(raw)
	}
	return store.UpsertConfigBlob(proxyConfigBlobKey, raw, etag, time.Now().UTC())
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b) + "\n"
}

func clampProxyRollbackMax(v int) int {
	if v <= 0 {
		return 8
	}
	if v > 64 {
		return 64
	}
	return v
}

func (rt *proxyRuntime) pushRollbackLocked(entry proxyRollbackEntry) {
	if strings.TrimSpace(entry.Raw) == "" {
		return
	}
	if rt.rollbackMax <= 0 {
		return
	}
	rt.rollbackStack = append(rt.rollbackStack, entry)
	if len(rt.rollbackStack) > rt.rollbackMax {
		trim := len(rt.rollbackStack) - rt.rollbackMax
		rt.rollbackStack = append([]proxyRollbackEntry(nil), rt.rollbackStack[trim:]...)
	}
}

func proxyURLSame(a, b *url.URL) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.String() == b.String()
}

func safeProxyURL(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "-"
	}
	return v
}

func safeProxyValue(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "-"
	}
	return v
}

func emitProxyConfigApplied(msg string, cfg ProxyRulesConfig) {
	log.Printf("[PROXY][INFO] %s upstream=%s upstream_count=%d strategy=%s force_http2=%t disable_compression=%t expect_continue_timeout=%ds buffer_request_body=%t max_response_buffer_bytes=%d flush_interval_ms=%d health_check_path=%s health_check_interval_sec=%d health_check_timeout_sec=%d error_html_file=%s error_redirect_url=%s tls_insecure_skip_verify=%t mtls=%t", msg, proxyDisplayUpstream(cfg), len(proxyConfiguredUpstreams(cfg)), cfg.LoadBalancingStrategy, cfg.ForceHTTP2, cfg.DisableCompression, cfg.ExpectContinueTimeout, cfg.BufferRequestBody, cfg.MaxResponseBufferBytes, cfg.FlushIntervalMS, cfg.HealthCheckPath, cfg.HealthCheckInterval, cfg.HealthCheckTimeout, safeProxyValue(cfg.ErrorHTMLFile), safeProxyValue(cfg.ErrorRedirectURL), cfg.TLSInsecureSkipVerify, cfg.TLSClientCert != "")
}

func emitProxyTLSInsecureWarning(cfg ProxyRulesConfig) {
	if !cfg.TLSInsecureSkipVerify {
		return
	}
	log.Printf("[PROXY][WARN] tls_insecure_skip_verify=true: backend TLS certificate verification is disabled")
}

type dynamicProxyTransport struct {
	mu      sync.RWMutex
	rt      http.RoundTripper
	tracker *upstreamHealthMonitor
}

func newDynamicProxyTransport(cfg ProxyRulesConfig, tracker *upstreamHealthMonitor) (*dynamicProxyTransport, error) {
	if _, err := buildProxyTLSClientConfig(cfg); err != nil {
		return nil, err
	}
	return &dynamicProxyTransport{rt: buildProxyTransport(cfg), tracker: tracker}, nil
}

func (d *dynamicProxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	d.mu.RLock()
	rt := d.rt
	tracker := d.tracker
	d.mu.RUnlock()
	tracer := otel.Tracer("mamotama/upstream")
	ctx, span := tracer.Start(
		req.Context(),
		"proxy.upstream",
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(
			attribute.String("http.request.method", req.Method),
			attribute.String("server.address", req.URL.Host),
			attribute.String("url.full", req.URL.String()),
		),
	)
	req = req.Clone(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	decision, _ := proxyRouteDecisionFromContext(req.Context())
	targets := decision.OrderedTargets
	if len(targets) == 0 && decision.Target != nil {
		targets = []proxyRouteTargetCandidate{{
			Key:     decision.HealthKey,
			Name:    decision.SelectedUpstream,
			Target:  cloneURL(decision.Target),
			Weight:  1,
			Managed: decision.HealthKey != "",
		}}
	}
	retryPolicy := decision.RetryPolicy
	maxAttempts := 1
	if retryPolicy.Enabled() && retryPolicy.AllowsMethod(req.Method) {
		maxAttempts += retryPolicy.Attempts
	}
	if len(targets) > 0 && len(targets) < maxAttempts {
		maxAttempts = len(targets)
	}
	if len(targets) == 0 {
		resp, err := rt.RoundTrip(req)
		endProxyTransportSpan(span, resp, err)
		return resp, err
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		candidate := targets[attempt]
		attemptReq, cancel, err := cloneProxyRetryRequest(req, decision, candidate, attempt, retryPolicy)
		if err != nil {
			if cancel != nil {
				cancel()
			}
			lastErr = err
			break
		}
		if tracker != nil && candidate.Key != "" && !tracker.AcquireTarget(candidate.Key) {
			if cancel != nil {
				cancel()
			}
			lastErr = fmt.Errorf("backend unavailable for retry target %q", candidate.Name)
			continue
		}

		resp, err := rt.RoundTrip(attemptReq)
		if err != nil {
			if tracker != nil && candidate.Key != "" {
				tracker.RecordPassiveFailure(candidate.Key, 0, err)
				tracker.ReleaseTarget(candidate.Key)
			}
			if cancel != nil {
				cancel()
			}
			lastErr = err
			if attempt+1 < maxAttempts && retryPolicy.Enabled() {
				if retryPolicy.Backoff > 0 {
					time.Sleep(retryPolicy.Backoff)
				}
				continue
			}
			endProxyTransportSpan(span, nil, err)
			return nil, err
		}

		shouldRetryStatus := retryPolicy.Enabled() && attempt+1 < maxAttempts && retryPolicy.RetryableStatus(resp.StatusCode)
		if shouldRetryStatus {
			if tracker != nil && candidate.Key != "" {
				tracker.RecordPassiveFailure(candidate.Key, resp.StatusCode, nil)
				tracker.ReleaseTarget(candidate.Key)
			}
			if resp.Body != nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
				_ = resp.Body.Close()
			}
			if cancel != nil {
				cancel()
			}
			if retryPolicy.Backoff > 0 {
				time.Sleep(retryPolicy.Backoff)
			}
			lastErr = fmt.Errorf("retryable status code: %d", resp.StatusCode)
			continue
		}

		if tracker != nil && candidate.Key != "" {
			if retryPolicy.RetryableStatus(resp.StatusCode) {
				tracker.RecordPassiveFailure(candidate.Key, resp.StatusCode, nil)
			} else {
				tracker.RecordPassiveSuccess(candidate.Key, resp.StatusCode)
			}
		}
		if resp == nil || resp.Body == nil {
			if tracker != nil && candidate.Key != "" {
				tracker.ReleaseTarget(candidate.Key)
			}
			if cancel != nil {
				cancel()
			}
			endProxyTransportSpan(span, resp, nil)
			return resp, nil
		}
		resp.Body = &proxyTrackedReadCloser{
			ReadCloser: resp.Body,
			release: func() {
				if tracker != nil && candidate.Key != "" {
					tracker.ReleaseTarget(candidate.Key)
				}
				if cancel != nil {
					cancel()
				}
			},
			span: span,
		}
		return resp, nil
	}
	endProxyTransportSpan(span, nil, lastErr)
	return nil, lastErr
}

func (d *dynamicProxyTransport) Update(cfg ProxyRulesConfig) error {
	if d == nil {
		return nil
	}
	if _, err := buildProxyTLSClientConfig(cfg); err != nil {
		return err
	}
	d.mu.Lock()
	old := d.rt
	d.rt = buildProxyTransport(cfg)
	d.mu.Unlock()
	if tr, ok := old.(*http.Transport); ok && tr != nil {
		tr.CloseIdleConnections()
	}
	return nil
}

type proxyTrackedReadCloser struct {
	io.ReadCloser
	once    sync.Once
	release func()
	span    oteltrace.Span
}

func cloneProxyRetryRequest(req *http.Request, decision proxyRouteDecision, candidate proxyRouteTargetCandidate, attempt int, retryPolicy proxyRetryPolicy) (*http.Request, context.CancelFunc, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("request is required")
	}
	out := req.Clone(req.Context())
	if attempt > 0 && req.Body != nil {
		if req.GetBody == nil {
			return nil, nil, fmt.Errorf("request body is not rewindable for retry")
		}
		body, err := req.GetBody()
		if err != nil {
			return nil, nil, err
		}
		out.Body = body
	}
	if out.URL == nil {
		out.URL = &url.URL{}
	}
	rewriteProxyOutgoingURL(out, candidate.Target, decision.RewrittenPath, decision.RewrittenRawPath)
	out.Host = decision.RewrittenHost
	var cancel context.CancelFunc
	if retryPolicy.PerTryTimeout > 0 {
		ctx, nextCancel := context.WithTimeout(out.Context(), retryPolicy.PerTryTimeout)
		out = out.WithContext(ctx)
		cancel = nextCancel
	}
	return out, cancel, nil
}

func (r *proxyTrackedReadCloser) Close() error {
	if r == nil || r.ReadCloser == nil {
		return nil
	}
	err := r.ReadCloser.Close()
	r.once.Do(func() {
		if r.release != nil {
			r.release()
		}
		if r.span != nil {
			r.span.End()
		}
	})
	return err
}

func (r *proxyTrackedReadCloser) Write(p []byte) (int, error) {
	if rw, ok := r.ReadCloser.(io.Writer); ok {
		return rw.Write(p)
	}
	return 0, fmt.Errorf("response body does not support write")
}

func endProxyTransportSpan(span oteltrace.Span, resp *http.Response, err error) {
	if span == nil {
		return
	}
	if resp != nil {
		span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}

func buildProxyTransport(cfg ProxyRulesConfig) *http.Transport {
	tlsCfg, err := buildProxyTLSClientConfig(cfg)
	if err != nil {
		tlsCfg = nil
	}
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   time.Duration(cfg.DialTimeout) * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.MaxConnsPerHost,
		IdleConnTimeout:       time.Duration(cfg.IdleConnTimeout) * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: time.Duration(cfg.ExpectContinueTimeout) * time.Second,
		ResponseHeaderTimeout: time.Duration(cfg.ResponseHeaderTimeout) * time.Second,
		ForceAttemptHTTP2:     cfg.ForceHTTP2,
		DisableCompression:    cfg.DisableCompression,
		TLSClientConfig:       tlsCfg,
	}
}

func buildProxyTLSClientConfig(cfg ProxyRulesConfig) (*tls.Config, error) {
	certPath := strings.TrimSpace(cfg.TLSClientCert)
	keyPath := strings.TrimSpace(cfg.TLSClientKey)
	if (certPath == "") != (keyPath == "") {
		return nil, fmt.Errorf("tls_client_cert and tls_client_key must be set together")
	}
	if certPath == "" && !cfg.TLSInsecureSkipVerify {
		return nil, nil
	}

	tlsCfg := &tls.Config{InsecureSkipVerify: cfg.TLSInsecureSkipVerify}
	if certPath == "" {
		return tlsCfg, nil
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load proxy tls client certificate: %w", err)
	}
	tlsCfg.Certificates = []tls.Certificate{cert}
	return tlsCfg, nil
}

func maybeBufferProxyRequestBody(req *http.Request) error {
	cfg := currentProxyConfig()
	if !cfg.BufferRequestBody || req == nil || req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	req.ContentLength = int64(len(body))
	return nil
}

func maybeBufferProxyResponseBody(res *http.Response) error {
	cfg := currentProxyConfig()
	if cfg.MaxResponseBufferBytes <= 0 || res == nil || res.Body == nil {
		return nil
	}
	if res.ContentLength > cfg.MaxResponseBufferBytes && res.ContentLength > 0 {
		return fmt.Errorf("upstream response exceeds max_response_buffer_bytes")
	}
	lr := io.LimitReader(res.Body, cfg.MaxResponseBufferBytes+1)
	body, err := io.ReadAll(lr)
	if err != nil {
		return err
	}
	if int64(len(body)) > cfg.MaxResponseBufferBytes {
		return fmt.Errorf("upstream response exceeds max_response_buffer_bytes")
	}
	_ = res.Body.Close()
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))
	if res.Header != nil {
		res.Header.Set("Content-Length", strconv.FormatInt(res.ContentLength, 10))
	}
	return nil
}

func probeProxyUpstream(in ProxyRulesConfig, timeout time.Duration) (string, int64, error) {
	cfg, target, _, err := normalizeAndValidateProxyRules(in)
	if err != nil {
		return "", 0, err
	}
	_ = cfg
	address, err := proxyDialAddress(target)
	if err != nil {
		return "", 0, err
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return address, 0, err
	}
	_ = conn.Close()
	return address, time.Since(start).Milliseconds(), nil
}

func proxyConfiguredUpstreams(cfg ProxyRulesConfig) []ProxyUpstream {
	if len(cfg.Upstreams) > 0 {
		return cfg.Upstreams
	}
	if strings.TrimSpace(cfg.UpstreamURL) == "" {
		return nil
	}
	return []ProxyUpstream{{
		Name:    "primary",
		URL:     cfg.UpstreamURL,
		Weight:  1,
		Enabled: true,
	}}
}

func proxyDisplayUpstream(cfg ProxyRulesConfig) string {
	if len(cfg.Upstreams) == 0 {
		return safeProxyURL(cfg.UpstreamURL)
	}
	names := make([]string, 0, len(cfg.Upstreams))
	for _, upstream := range cfg.Upstreams {
		if !upstream.Enabled {
			continue
		}
		names = append(names, upstream.URL)
	}
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, ",")
}

func proxyDialAddress(target *url.URL) (string, error) {
	if target == nil {
		return "", fmt.Errorf("upstream target is required")
	}
	host := strings.TrimSpace(target.Host)
	if host == "" {
		return "", fmt.Errorf("upstream host is required")
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host, nil
	}
	host = target.Hostname()
	if host == "" {
		return "", fmt.Errorf("upstream host is required")
	}
	port := target.Port()
	if port == "" {
		switch strings.ToLower(strings.TrimSpace(target.Scheme)) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return "", fmt.Errorf("unsupported upstream scheme: %s", target.Scheme)
		}
	}
	return net.JoinHostPort(host, port), nil
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return &url.URL{}
	}
	return u
}

type upstreamHealthStatus struct {
	Enabled             bool                    `json:"enabled"`
	Status              string                  `json:"status"`
	Strategy            string                  `json:"strategy,omitempty"`
	Endpoint            string                  `json:"endpoint,omitempty"`
	HealthCheckPath     string                  `json:"health_check_path"`
	HealthCheckInterval int                     `json:"health_check_interval_sec"`
	HealthCheckTimeout  int                     `json:"health_check_timeout_sec"`
	CheckedAt           string                  `json:"checked_at,omitempty"`
	LastSuccessAt       string                  `json:"last_success_at,omitempty"`
	LastFailureAt       string                  `json:"last_failure_at,omitempty"`
	ConsecutiveFailures int                     `json:"consecutive_failures"`
	LastError           string                  `json:"last_error,omitempty"`
	LastStatusCode      int                     `json:"last_status_code,omitempty"`
	LastLatencyMS       int64                   `json:"last_latency_ms,omitempty"`
	ActiveBackends      int                     `json:"active_backends"`
	HealthyBackends     int                     `json:"healthy_backends"`
	Backends            []upstreamBackendStatus `json:"backends,omitempty"`
}

type upstreamBackendStatus struct {
	Key                 string `json:"key"`
	Name                string `json:"name"`
	URL                 string `json:"url"`
	Enabled             bool   `json:"enabled"`
	Healthy             bool   `json:"healthy"`
	InFlight            int    `json:"inflight"`
	PassiveFailures     int    `json:"passive_failures,omitempty"`
	CircuitState        string `json:"circuit_state,omitempty"`
	CircuitOpenedAt     string `json:"circuit_opened_at,omitempty"`
	CircuitReopenAt     string `json:"circuit_reopen_at,omitempty"`
	Endpoint            string `json:"endpoint,omitempty"`
	CheckedAt           string `json:"checked_at,omitempty"`
	LastSuccessAt       string `json:"last_success_at,omitempty"`
	LastFailureAt       string `json:"last_failure_at,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastError           string `json:"last_error,omitempty"`
	LastStatusCode      int    `json:"last_status_code,omitempty"`
	LastLatencyMS       int64  `json:"last_latency_ms,omitempty"`
}

type proxyTargetSelection struct {
	Key    string
	Name   string
	Target *url.URL
}

type proxyBackendState struct {
	Key                 string
	Name                string
	URL                 string
	Target              *url.URL
	Weight              int
	Enabled             bool
	Healthy             bool
	InFlight            int
	PassiveFailures     int
	CircuitState        string
	CircuitOpenedAt     time.Time
	CircuitReopenAt     time.Time
	HalfOpenRequests    int
	Endpoint            string
	CheckedAt           string
	LastSuccessAt       string
	LastFailureAt       string
	ConsecutiveFailures int
	LastError           string
	LastStatusCode      int
	LastLatencyMS       int64
}

type upstreamHealthMonitor struct {
	mu       sync.RWMutex
	cfg      ProxyRulesConfig
	status   upstreamHealthStatus
	backends []*proxyBackendState
	wakeCh   chan struct{}
	running  bool
	rrCursor uint64
}

func newUpstreamHealthMonitor(initial ProxyRulesConfig) *upstreamHealthMonitor {
	cfg := normalizeProxyRulesConfig(initial)
	m := &upstreamHealthMonitor{
		cfg:      cfg,
		wakeCh:   make(chan struct{}, 1),
		status:   upstreamHealthStatus{Status: "disabled"},
		backends: buildProxyBackendStates(cfg, nil),
	}
	m.applyConfigLocked(cfg)
	if m.status.Enabled {
		m.running = true
		go m.run()
	}
	return m
}

func (m *upstreamHealthMonitor) Snapshot() upstreamHealthStatus {
	if m == nil {
		return upstreamHealthStatus{Status: "disabled"}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	snapshot := m.status
	if len(snapshot.Backends) > 0 {
		cp := make([]upstreamBackendStatus, len(snapshot.Backends))
		copy(cp, snapshot.Backends)
		snapshot.Backends = cp
	}
	return snapshot
}

func (m *upstreamHealthMonitor) Update(next ProxyRulesConfig) {
	if m == nil {
		return
	}
	next = normalizeProxyRulesConfig(next)
	m.mu.Lock()
	m.cfg = next
	m.backends = buildProxyBackendStates(next, m.backends)
	m.applyConfigLocked(next)
	shouldStart := !m.running && m.status.Enabled
	if shouldStart {
		m.running = true
	}
	m.mu.Unlock()
	if shouldStart {
		go m.run()
	}
	m.triggerWake()
}

func (m *upstreamHealthMonitor) SelectTarget() (proxyTargetSelection, bool) {
	if m == nil {
		return proxyTargetSelection{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	idx, ok := m.selectBackendIndexLocked()
	if !ok {
		return proxyTargetSelection{}, false
	}
	backend := m.backends[idx]
	backend.InFlight++
	m.refreshStatusLocked()
	return proxyTargetSelection{
		Key:    backend.Key,
		Name:   backend.Name,
		Target: cloneURL(backend.Target),
	}, true
}

func (m *upstreamHealthMonitor) ReleaseTarget(key string) {
	if m == nil || strings.TrimSpace(key) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, backend := range m.backends {
		if backend.Key != key {
			continue
		}
		if backend.InFlight > 0 {
			backend.InFlight--
		}
		if backend.HalfOpenRequests > 0 {
			backend.HalfOpenRequests--
		}
		break
	}
	m.refreshStatusLocked()
}

func (m *upstreamHealthMonitor) AcquireTarget(key string) bool {
	if m == nil || strings.TrimSpace(key) == "" {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for _, backend := range m.backends {
		if backend == nil || backend.Key != key {
			continue
		}
		if !proxyBackendSelectableLocked(m.cfg, backend, now) {
			return false
		}
		if m.cfg.CircuitBreakerEnabled && backend.CircuitState == "open" && !backend.CircuitReopenAt.IsZero() && !now.Before(backend.CircuitReopenAt) {
			backend.CircuitState = "half_open"
		}
		backend.InFlight++
		if backend.CircuitState == "half_open" {
			backend.HalfOpenRequests++
		}
		m.refreshStatusLocked()
		return true
	}
	return true
}

func (m *upstreamHealthMonitor) RecordPassiveFailure(key string, statusCode int, err error) {
	if m == nil || strings.TrimSpace(key) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for _, backend := range m.backends {
		if backend == nil || backend.Key != key {
			continue
		}
		backend.LastFailureAt = now.Format(time.RFC3339Nano)
		backend.LastStatusCode = statusCode
		if err != nil {
			backend.LastError = err.Error()
		} else if statusCode > 0 {
			backend.LastError = fmt.Sprintf("unexpected status code: %d", statusCode)
		}
		backend.PassiveFailures++
		if m.cfg.PassiveHealthEnabled && backend.PassiveFailures >= proxyPassiveFailureThreshold(m.cfg) {
			backend.Healthy = false
		}
		if m.cfg.CircuitBreakerEnabled && backend.PassiveFailures >= proxyPassiveFailureThreshold(m.cfg) {
			backend.CircuitState = "open"
			backend.CircuitOpenedAt = now
			backend.CircuitReopenAt = now.Add(proxyCircuitOpenDuration(m.cfg))
		}
		m.refreshStatusLocked()
		return
	}
}

func (m *upstreamHealthMonitor) RecordPassiveSuccess(key string, statusCode int) {
	if m == nil || strings.TrimSpace(key) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for _, backend := range m.backends {
		if backend == nil || backend.Key != key {
			continue
		}
		backend.LastSuccessAt = now.Format(time.RFC3339Nano)
		backend.LastStatusCode = statusCode
		backend.LastError = ""
		backend.PassiveFailures = 0
		backend.Healthy = true
		backend.CircuitState = ""
		backend.CircuitOpenedAt = time.Time{}
		backend.CircuitReopenAt = time.Time{}
		backend.HalfOpenRequests = 0
		m.refreshStatusLocked()
		return
	}
}

func (m *upstreamHealthMonitor) run() {
	for {
		cfg := m.currentConfig()
		if !proxyHealthCheckEnabled(cfg) {
			m.awaitWake()
			continue
		}
		backends := m.backendsSnapshot()
		for _, backend := range backends {
			if backend == nil || !backend.Enabled {
				continue
			}
			checkedAt := time.Now().UTC()
			statusCode, latencyMS, err := checkProxyBackendHealth(cfg, backend.Target)
			m.recordResult(backend.Key, checkedAt, statusCode, latencyMS, err)
		}
		m.waitOrWake(proxyHealthCheckInterval(cfg))
	}
}

func (m *upstreamHealthMonitor) currentConfig() ProxyRulesConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *upstreamHealthMonitor) applyConfigLocked(cfg ProxyRulesConfig) {
	enabled := proxyHealthCheckEnabled(cfg)
	endpoint := ""
	if len(m.backends) > 0 {
		endpoint = m.backends[0].Endpoint
	}

	m.status.Enabled = enabled
	m.status.Strategy = cfg.LoadBalancingStrategy
	m.status.HealthCheckPath = cfg.HealthCheckPath
	m.status.HealthCheckInterval = cfg.HealthCheckInterval
	m.status.HealthCheckTimeout = cfg.HealthCheckTimeout
	m.status.Endpoint = endpoint
	if !enabled {
		m.status.Status = "disabled"
		m.status.ConsecutiveFailures = 0
		m.status.LastError = ""
		m.status.LastStatusCode = 0
		m.status.LastLatencyMS = 0
		m.refreshStatusLocked()
		return
	}
	m.refreshStatusLocked()
}

func (m *upstreamHealthMonitor) recordResult(key string, checkedAt time.Time, statusCode int, latencyMS int64, err error) {
	m.mu.Lock()
	for _, backend := range m.backends {
		if backend.Key != key {
			continue
		}
		backend.CheckedAt = checkedAt.Format(time.RFC3339Nano)
		backend.LastStatusCode = statusCode
		backend.LastLatencyMS = latencyMS
		if err == nil {
			backend.Healthy = true
			backend.LastSuccessAt = backend.CheckedAt
			backend.ConsecutiveFailures = 0
			backend.LastError = ""
			backend.PassiveFailures = 0
			backend.CircuitState = ""
			backend.CircuitOpenedAt = time.Time{}
			backend.CircuitReopenAt = time.Time{}
			backend.HalfOpenRequests = 0
		} else {
			backend.Healthy = false
			backend.LastFailureAt = backend.CheckedAt
			backend.ConsecutiveFailures++
			backend.LastError = err.Error()
		}
		break
	}
	m.refreshStatusLocked()
	m.mu.Unlock()
}

func (m *upstreamHealthMonitor) backendsSnapshot() []*proxyBackendState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*proxyBackendState, 0, len(m.backends))
	for _, backend := range m.backends {
		if backend == nil {
			continue
		}
		cp := *backend
		cp.Target = cloneURL(backend.Target)
		out = append(out, &cp)
	}
	return out
}

func (m *upstreamHealthMonitor) selectBackendIndexLocked() (int, bool) {
	if len(m.backends) == 0 {
		return -1, false
	}
	candidates := make([]int, 0, len(m.backends))
	healthyCandidates := make([]int, 0, len(m.backends))
	for i, backend := range m.backends {
		if backend == nil || !backend.Enabled {
			continue
		}
		candidates = append(candidates, i)
		if !m.status.Enabled || backend.Healthy {
			healthyCandidates = append(healthyCandidates, i)
		}
	}
	if len(candidates) == 0 {
		return -1, false
	}
	if len(healthyCandidates) > 0 {
		candidates = healthyCandidates
	}
	switch m.cfg.LoadBalancingStrategy {
	case "least_conn":
		best := candidates[0]
		for _, idx := range candidates[1:] {
			if proxyBackendLessLoaded(m.backends[idx], m.backends[best]) {
				best = idx
			}
		}
		return best, true
	default:
		totalWeight := 0
		for _, idx := range candidates {
			totalWeight += m.backends[idx].Weight
		}
		if totalWeight <= 0 {
			return candidates[0], true
		}
		selected := int(m.rrCursor % uint64(totalWeight))
		m.rrCursor++
		acc := 0
		for _, idx := range candidates {
			acc += m.backends[idx].Weight
			if selected < acc {
				return idx, true
			}
		}
		return candidates[0], true
	}
}

func (m *upstreamHealthMonitor) refreshStatusLocked() {
	backends := make([]upstreamBackendStatus, 0, len(m.backends))
	activeCount := 0
	healthyCount := 0
	var aggregate upstreamBackendStatus
	var aggregateSet bool

	for _, backend := range m.backends {
		if backend == nil {
			continue
		}
		if backend.Enabled {
			activeCount++
		}
		if backend.Enabled && backend.Healthy {
			healthyCount++
		}
		entry := upstreamBackendStatus{
			Key:                 backend.Key,
			Name:                backend.Name,
			URL:                 backend.URL,
			Enabled:             backend.Enabled,
			Healthy:             backend.Healthy,
			InFlight:            backend.InFlight,
			PassiveFailures:     backend.PassiveFailures,
			CircuitState:        backend.CircuitState,
			CircuitOpenedAt:     formatProxyTime(backend.CircuitOpenedAt),
			CircuitReopenAt:     formatProxyTime(backend.CircuitReopenAt),
			Endpoint:            backend.Endpoint,
			CheckedAt:           backend.CheckedAt,
			LastSuccessAt:       backend.LastSuccessAt,
			LastFailureAt:       backend.LastFailureAt,
			ConsecutiveFailures: backend.ConsecutiveFailures,
			LastError:           backend.LastError,
			LastStatusCode:      backend.LastStatusCode,
			LastLatencyMS:       backend.LastLatencyMS,
		}
		backends = append(backends, entry)
		if !aggregateSet && backend.Enabled {
			aggregate = entry
			aggregateSet = true
		}
	}

	m.status.Backends = backends
	m.status.ActiveBackends = activeCount
	m.status.HealthyBackends = healthyCount
	m.status.Endpoint = aggregate.Endpoint
	m.status.CheckedAt = aggregate.CheckedAt
	m.status.LastSuccessAt = aggregate.LastSuccessAt
	m.status.LastFailureAt = aggregate.LastFailureAt
	m.status.ConsecutiveFailures = aggregate.ConsecutiveFailures
	m.status.LastError = aggregate.LastError
	m.status.LastStatusCode = aggregate.LastStatusCode
	m.status.LastLatencyMS = aggregate.LastLatencyMS

	switch {
	case !m.status.Enabled:
		m.status.Status = "disabled"
	case healthyCount > 0 && healthyCount == activeCount:
		m.status.Status = "healthy"
	case healthyCount > 0:
		m.status.Status = "degraded"
	case activeCount > 0:
		checked := false
		for _, backend := range backends {
			if backend.CheckedAt != "" {
				checked = true
				break
			}
		}
		if checked {
			m.status.Status = "unhealthy"
		} else {
			m.status.Status = "unknown"
		}
	default:
		m.status.Status = "unknown"
	}
}

func (m *upstreamHealthMonitor) triggerWake() {
	if m == nil {
		return
	}
	select {
	case m.wakeCh <- struct{}{}:
	default:
	}
}

func (m *upstreamHealthMonitor) awaitWake() {
	if m == nil {
		return
	}
	<-m.wakeCh
}

func (m *upstreamHealthMonitor) waitOrWake(wait time.Duration) {
	if wait <= 0 {
		wait = time.Duration(defaultProxyHealthCheckIntervalSec) * time.Second
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-m.wakeCh:
	}
}

func proxyHealthCheckEnabled(cfg ProxyRulesConfig) bool {
	return strings.TrimSpace(cfg.HealthCheckPath) != ""
}

func proxyHealthCheckInterval(cfg ProxyRulesConfig) time.Duration {
	sec := cfg.HealthCheckInterval
	if sec <= 0 {
		sec = defaultProxyHealthCheckIntervalSec
	}
	return time.Duration(sec) * time.Second
}

func proxyHealthCheckTimeout(cfg ProxyRulesConfig) time.Duration {
	sec := cfg.HealthCheckTimeout
	if sec <= 0 {
		sec = defaultProxyHealthCheckTimeoutSec
	}
	return time.Duration(sec) * time.Second
}

func proxyPassiveFailureThreshold(cfg ProxyRulesConfig) int {
	if cfg.PassiveFailureThreshold > 0 {
		return cfg.PassiveFailureThreshold
	}
	return 3
}

func proxyCircuitOpenDuration(cfg ProxyRulesConfig) time.Duration {
	if cfg.CircuitBreakerOpenSec > 0 {
		return time.Duration(cfg.CircuitBreakerOpenSec) * time.Second
	}
	return 30 * time.Second
}

func proxyCircuitHalfOpenRequests(cfg ProxyRulesConfig) int {
	if cfg.CircuitBreakerHalfOpenRequests > 0 {
		return cfg.CircuitBreakerHalfOpenRequests
	}
	return 1
}

func proxyBackendSelectableLocked(cfg ProxyRulesConfig, backend *proxyBackendState, now time.Time) bool {
	if backend == nil || !backend.Enabled {
		return false
	}
	if cfg.CircuitBreakerEnabled {
		switch backend.CircuitState {
		case "open":
			if !backend.CircuitReopenAt.IsZero() && now.Before(backend.CircuitReopenAt) {
				return false
			}
		case "half_open":
			if backend.HalfOpenRequests >= proxyCircuitHalfOpenRequests(cfg) {
				return false
			}
		}
	}
	if proxyHealthCheckEnabled(cfg) || cfg.PassiveHealthEnabled {
		return backend.Healthy
	}
	return true
}

func formatProxyTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339Nano)
}

func proxyHealthEndpoint(cfg ProxyRulesConfig, target *url.URL) (string, error) {
	if target == nil {
		return "", fmt.Errorf("upstream target is required")
	}
	endpoint := *target
	endpoint.Path = cfg.HealthCheckPath
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String(), nil
}

func checkProxyBackendHealth(cfg ProxyRulesConfig, target *url.URL) (statusCode int, latencyMS int64, err error) {
	endpoint, err := proxyHealthEndpoint(cfg, target)
	if err != nil {
		return 0, 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), proxyHealthCheckTimeout(cfg))
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, 0, err
	}

	transport := buildProxyTransport(cfg)
	defer transport.CloseIdleConnections()

	client := &http.Client{Transport: transport}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	latency := time.Since(start).Milliseconds()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return resp.StatusCode, latency, nil
	}
	return resp.StatusCode, latency, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
}

func buildProxyBackendStates(cfg ProxyRulesConfig, prev []*proxyBackendState) []*proxyBackendState {
	prevMap := make(map[string]*proxyBackendState, len(prev))
	for _, backend := range prev {
		if backend == nil {
			continue
		}
		prevMap[backend.Key] = backend
	}

	defs := cfg.Upstreams
	if len(defs) == 0 {
		defs = []ProxyUpstream{{Name: "primary", URL: cfg.UpstreamURL, Weight: 1, Enabled: true}}
	}
	out := make([]*proxyBackendState, 0, len(defs))
	for i, upstream := range defs {
		target, err := parseProxyUpstreamURL(fmt.Sprintf("upstreams[%d].url", i), upstream.URL)
		if err != nil {
			continue
		}
		key := proxyBackendLookupKey(upstream.Name, target.String())
		state := &proxyBackendState{
			Key:     key,
			Name:    upstream.Name,
			URL:     target.String(),
			Target:  target,
			Weight:  upstream.Weight,
			Enabled: upstream.Enabled,
			Healthy: true,
		}
		if state.Weight <= 0 {
			state.Weight = 1
		}
		if prevState, ok := prevMap[key]; ok {
			state.Healthy = prevState.Healthy
			state.InFlight = prevState.InFlight
			state.CheckedAt = prevState.CheckedAt
			state.LastSuccessAt = prevState.LastSuccessAt
			state.LastFailureAt = prevState.LastFailureAt
			state.ConsecutiveFailures = prevState.ConsecutiveFailures
			state.LastError = prevState.LastError
			state.LastStatusCode = prevState.LastStatusCode
			state.LastLatencyMS = prevState.LastLatencyMS
			state.PassiveFailures = prevState.PassiveFailures
			state.CircuitState = prevState.CircuitState
			state.CircuitOpenedAt = prevState.CircuitOpenedAt
			state.CircuitReopenAt = prevState.CircuitReopenAt
			state.HalfOpenRequests = prevState.HalfOpenRequests
		}
		if endpoint, err := proxyHealthEndpoint(cfg, target); err == nil {
			state.Endpoint = endpoint
		}
		out = append(out, state)
	}
	return out
}

func proxyBackendLessLoaded(a, b *proxyBackendState) bool {
	if a == nil {
		return false
	}
	if b == nil {
		return true
	}
	left := int64(a.InFlight) * int64(b.Weight)
	right := int64(b.InFlight) * int64(a.Weight)
	if left == right {
		return a.Name < b.Name
	}
	return left < right
}

func cloneURL(in *url.URL) *url.URL {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func asProxyRulesConflict(err error, target *proxyRulesConflictError) bool {
	if err == nil || target == nil {
		return false
	}
	var c proxyRulesConflictError
	if !errors.As(err, &c) {
		return false
	}
	*target = c
	return true
}
