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

	"mamotama/internal/bypassconf"
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
	UpstreamURL           string `json:"upstream_url"`
	DialTimeout           int    `json:"dial_timeout"`
	ResponseHeaderTimeout int    `json:"response_header_timeout"`
	IdleConnTimeout       int    `json:"idle_conn_timeout"`
	MaxIdleConns          int    `json:"max_idle_conns"`
	MaxIdleConnsPerHost   int    `json:"max_idle_conns_per_host"`
	MaxConnsPerHost       int    `json:"max_conns_per_host"`
	ForceHTTP2            bool   `json:"force_http2"`
	DisableCompression    bool   `json:"disable_compression"`
	ExpectContinueTimeout int    `json:"expect_continue_timeout"`
	TLSInsecureSkipVerify bool   `json:"tls_insecure_skip_verify"`
	TLSClientCert         string `json:"tls_client_cert"`
	TLSClientKey          string `json:"tls_client_key"`

	BufferRequestBody      bool  `json:"buffer_request_body"`
	MaxResponseBufferBytes int64 `json:"max_response_buffer_bytes"`
	FlushIntervalMS        int   `json:"flush_interval_ms"`

	HealthCheckPath     string `json:"health_check_path"`
	HealthCheckInterval int    `json:"health_check_interval_sec"`
	HealthCheckTimeout  int    `json:"health_check_timeout_sec"`
}

type proxyRulesPreparedUpdate struct {
	cfg    ProxyRulesConfig
	target *url.URL
	raw    string
	etag   string
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

	transport, err := newDynamicProxyTransport(prepared.cfg)
	if err != nil {
		return fmt.Errorf("build proxy transport: %w", err)
	}
	cfgForRewrite := prepared.cfg
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			target := currentProxyTarget()
			if target == nil {
				target = mustURL(cfgForRewrite.UpstreamURL)
			}
			pr.SetURL(target)
			pr.SetXForwarded()
			pr.Out.Host = pr.In.Host
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
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
		rollbackMax:   clampProxyRollbackMax(rollbackMax),
		rollbackStack: make([]proxyRollbackEntry, 0, clampProxyRollbackMax(rollbackMax)),
	}
	rt.health = newUpstreamHealthMonitor(prepared.cfg)

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

	address, latencyMS, err := probeProxyUpstream(cfg.UpstreamURL, timeout)
	return cfg, address, latencyMS, err
}

func prepareProxyRulesRaw(raw string) (proxyRulesPreparedUpdate, error) {
	cfg, target, err := parseProxyRulesRaw(raw)
	if err != nil {
		return proxyRulesPreparedUpdate{}, err
	}
	normalizedRaw := mustJSON(cfg)
	return proxyRulesPreparedUpdate{
		cfg:    cfg,
		target: target,
		raw:    normalizedRaw,
		etag:   bypassconf.ComputeETag([]byte(normalizedRaw)),
	}, nil
}

func parseProxyRulesRaw(raw string) (ProxyRulesConfig, *url.URL, error) {
	var in ProxyRulesConfig
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return ProxyRulesConfig{}, nil, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return ProxyRulesConfig{}, nil, fmt.Errorf("invalid json")
	}
	return normalizeAndValidateProxyRules(in)
}

func normalizeAndValidateProxyRules(in ProxyRulesConfig) (ProxyRulesConfig, *url.URL, error) {
	cfg := normalizeProxyRulesConfig(in)
	cfg.UpstreamURL = strings.TrimSpace(cfg.UpstreamURL)
	if cfg.UpstreamURL == "" {
		return ProxyRulesConfig{}, nil, fmt.Errorf("upstream_url is required")
	}
	target, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return ProxyRulesConfig{}, nil, fmt.Errorf("upstream_url parse error: %w", err)
	}
	if target.Scheme == "" || target.Host == "" {
		return ProxyRulesConfig{}, nil, fmt.Errorf("upstream_url must include scheme and host")
	}
	scheme := strings.ToLower(strings.TrimSpace(target.Scheme))
	if scheme != "http" && scheme != "https" {
		return ProxyRulesConfig{}, nil, fmt.Errorf("upstream_url scheme must be http or https")
	}

	if cfg.DialTimeout <= 0 {
		return ProxyRulesConfig{}, nil, fmt.Errorf("dial_timeout must be > 0")
	}
	if cfg.ResponseHeaderTimeout <= 0 {
		return ProxyRulesConfig{}, nil, fmt.Errorf("response_header_timeout must be > 0")
	}
	if cfg.IdleConnTimeout <= 0 {
		return ProxyRulesConfig{}, nil, fmt.Errorf("idle_conn_timeout must be > 0")
	}
	if cfg.MaxIdleConns <= 0 {
		return ProxyRulesConfig{}, nil, fmt.Errorf("max_idle_conns must be > 0")
	}
	if cfg.MaxIdleConnsPerHost <= 0 {
		return ProxyRulesConfig{}, nil, fmt.Errorf("max_idle_conns_per_host must be > 0")
	}
	if cfg.MaxConnsPerHost <= 0 {
		return ProxyRulesConfig{}, nil, fmt.Errorf("max_conns_per_host must be > 0")
	}
	if cfg.ExpectContinueTimeout <= 0 {
		return ProxyRulesConfig{}, nil, fmt.Errorf("expect_continue_timeout must be > 0")
	}
	if cfg.MaxResponseBufferBytes < 0 {
		return ProxyRulesConfig{}, nil, fmt.Errorf("max_response_buffer_bytes must be >= 0")
	}
	if cfg.FlushIntervalMS < 0 {
		return ProxyRulesConfig{}, nil, fmt.Errorf("flush_interval_ms must be >= 0")
	}
	if cfg.HealthCheckInterval <= 0 {
		return ProxyRulesConfig{}, nil, fmt.Errorf("health_check_interval_sec must be > 0")
	}
	if cfg.HealthCheckTimeout <= 0 {
		return ProxyRulesConfig{}, nil, fmt.Errorf("health_check_timeout_sec must be > 0")
	}
	if cfg.HealthCheckPath != "" && !strings.HasPrefix(cfg.HealthCheckPath, "/") {
		return ProxyRulesConfig{}, nil, fmt.Errorf("health_check_path must start with '/'")
	}
	if (cfg.TLSClientCert != "" || cfg.TLSClientKey != "") && scheme != "https" {
		return ProxyRulesConfig{}, nil, fmt.Errorf("tls_client_cert and tls_client_key require https upstream_url")
	}
	if _, err := buildProxyTLSClientConfig(cfg); err != nil {
		return ProxyRulesConfig{}, nil, err
	}
	cfg.UpstreamURL = target.String()
	return cfg, target, nil
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

func emitProxyConfigApplied(msg string, cfg ProxyRulesConfig) {
	log.Printf("[PROXY][INFO] %s upstream=%s force_http2=%t disable_compression=%t expect_continue_timeout=%ds buffer_request_body=%t max_response_buffer_bytes=%d flush_interval_ms=%d health_check_path=%s health_check_interval_sec=%d health_check_timeout_sec=%d tls_insecure_skip_verify=%t mtls=%t", msg, cfg.UpstreamURL, cfg.ForceHTTP2, cfg.DisableCompression, cfg.ExpectContinueTimeout, cfg.BufferRequestBody, cfg.MaxResponseBufferBytes, cfg.FlushIntervalMS, cfg.HealthCheckPath, cfg.HealthCheckInterval, cfg.HealthCheckTimeout, cfg.TLSInsecureSkipVerify, cfg.TLSClientCert != "")
}

func emitProxyTLSInsecureWarning(cfg ProxyRulesConfig) {
	if !cfg.TLSInsecureSkipVerify {
		return
	}
	log.Printf("[PROXY][WARN] tls_insecure_skip_verify=true: backend TLS certificate verification is disabled")
}

type dynamicProxyTransport struct {
	mu sync.RWMutex
	rt http.RoundTripper
}

func newDynamicProxyTransport(cfg ProxyRulesConfig) (*dynamicProxyTransport, error) {
	if _, err := buildProxyTLSClientConfig(cfg); err != nil {
		return nil, err
	}
	return &dynamicProxyTransport{rt: buildProxyTransport(cfg)}, nil
}

func (d *dynamicProxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	d.mu.RLock()
	rt := d.rt
	d.mu.RUnlock()
	return rt.RoundTrip(req)
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

func probeProxyUpstream(rawURL string, timeout time.Duration) (string, int64, error) {
	cfg, target, err := normalizeAndValidateProxyRules(ProxyRulesConfig{UpstreamURL: rawURL})
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
	Enabled             bool   `json:"enabled"`
	Status              string `json:"status"`
	Endpoint            string `json:"endpoint"`
	HealthCheckPath     string `json:"health_check_path"`
	HealthCheckInterval int    `json:"health_check_interval_sec"`
	HealthCheckTimeout  int    `json:"health_check_timeout_sec"`
	CheckedAt           string `json:"checked_at,omitempty"`
	LastSuccessAt       string `json:"last_success_at,omitempty"`
	LastFailureAt       string `json:"last_failure_at,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastError           string `json:"last_error,omitempty"`
	LastStatusCode      int    `json:"last_status_code,omitempty"`
	LastLatencyMS       int64  `json:"last_latency_ms,omitempty"`
}

type upstreamHealthMonitor struct {
	mu      sync.RWMutex
	cfg     ProxyRulesConfig
	status  upstreamHealthStatus
	wakeCh  chan struct{}
	running bool
}

func newUpstreamHealthMonitor(initial ProxyRulesConfig) *upstreamHealthMonitor {
	cfg := normalizeProxyRulesConfig(initial)
	m := &upstreamHealthMonitor{
		cfg:    cfg,
		wakeCh: make(chan struct{}, 1),
		status: upstreamHealthStatus{Status: "disabled"},
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
	return m.status
}

func (m *upstreamHealthMonitor) Update(next ProxyRulesConfig) {
	if m == nil {
		return
	}
	next = normalizeProxyRulesConfig(next)
	m.mu.Lock()
	m.cfg = next
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

func (m *upstreamHealthMonitor) run() {
	for {
		cfg := m.currentConfig()
		if !proxyHealthCheckEnabled(cfg) {
			m.awaitWake()
			continue
		}
		checkedAt := time.Now().UTC()
		statusCode, latencyMS, err := checkProxyUpstreamHealth(cfg)
		m.recordResult(checkedAt, statusCode, latencyMS, err)
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
	endpoint, _ := proxyHealthEndpoint(cfg)

	m.status.Enabled = enabled
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
		return
	}
	if m.status.Status == "" || m.status.Status == "disabled" {
		m.status.Status = "unknown"
	}
}

func (m *upstreamHealthMonitor) recordResult(checkedAt time.Time, statusCode int, latencyMS int64, err error) {
	m.mu.Lock()
	m.status.CheckedAt = checkedAt.Format(time.RFC3339Nano)
	m.status.LastStatusCode = statusCode
	m.status.LastLatencyMS = latencyMS
	if err == nil {
		m.status.Status = "healthy"
		m.status.LastSuccessAt = m.status.CheckedAt
		m.status.ConsecutiveFailures = 0
		m.status.LastError = ""
	} else {
		m.status.Status = "unhealthy"
		m.status.LastFailureAt = m.status.CheckedAt
		m.status.ConsecutiveFailures++
		m.status.LastError = err.Error()
	}
	m.mu.Unlock()
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

func proxyHealthEndpoint(cfg ProxyRulesConfig) (string, error) {
	target, err := url.Parse(strings.TrimSpace(cfg.UpstreamURL))
	if err != nil {
		return "", err
	}
	if target.Scheme == "" || target.Host == "" {
		return "", fmt.Errorf("upstream_url must include scheme and host")
	}
	endpoint := *target
	endpoint.Path = cfg.HealthCheckPath
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String(), nil
}

func checkProxyUpstreamHealth(cfg ProxyRulesConfig) (statusCode int, latencyMS int64, err error) {
	endpoint, err := proxyHealthEndpoint(cfg)
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
