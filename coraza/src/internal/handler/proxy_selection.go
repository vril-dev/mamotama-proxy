package handler

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

var proxySelectionCursor uint64

type proxyRetryPolicy struct {
	Attempts      int
	Backoff       time.Duration
	PerTryTimeout time.Duration
	StatusCodes   map[int]struct{}
	Methods       map[string]struct{}
}

type proxyRouteTargetCandidate struct {
	Key     string
	Name    string
	Target  *url.URL
	Weight  int
	Managed bool
}

type proxyRouteTargetSelectionOptions struct {
	HashPolicy   string
	HashKey      string
	UseLeastConn bool
}

type proxyCandidateAvailability struct {
	Selectable bool
	InFlight   int
}

func normalizeProxyHashPolicy(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "none":
		return ""
	case "client_ip":
		return "client_ip"
	case "header":
		return "header"
	case "cookie":
		return "cookie"
	case "jwt_sub":
		return "jwt_sub"
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func validateProxyHashPolicy(policy string, key string, field string) error {
	switch normalizeProxyHashPolicy(policy) {
	case "":
		return nil
	case "client_ip", "jwt_sub":
		return nil
	case "header", "cookie":
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s requires hash_key", field)
		}
		return nil
	default:
		return fmt.Errorf("%s must be one of none|client_ip|header|cookie|jwt_sub", field)
	}
}

func normalizeProxyMethodList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, raw := range in {
		next := strings.ToUpper(strings.TrimSpace(raw))
		if next == "" {
			continue
		}
		if _, ok := seen[next]; ok {
			continue
		}
		seen[next] = struct{}{}
		out = append(out, next)
	}
	return out
}

func validateProxyRetryMethods(in []string, field string) error {
	for _, method := range in {
		if strings.TrimSpace(method) == "" {
			return fmt.Errorf("%s must not contain empty entries", field)
		}
	}
	return nil
}

func normalizeProxyStatusCodeList(in []int) []int {
	out := make([]int, 0, len(in))
	seen := map[int]struct{}{}
	for _, code := range in {
		if code < 100 || code > 599 {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	sort.Ints(out)
	return out
}

func validateProxyRetryStatusCodes(in []int, field string) error {
	for _, code := range in {
		if code < 100 || code > 599 {
			return fmt.Errorf("%s entries must be valid HTTP status codes", field)
		}
	}
	return nil
}

func validateProxyRetryConfiguration(cfg ProxyRulesConfig) error {
	if cfg.RetryAttempts == 0 {
		return nil
	}
	if cfg.RetryAttempts < 0 || cfg.RetryAttempts > 8 {
		return fmt.Errorf("retry_attempts must be between 0 and 8")
	}
	if cfg.RetryBackoffMS > 10000 {
		return fmt.Errorf("retry_backoff_ms must be <= 10000")
	}
	if cfg.RetryPerTryTimeoutMS > 0 && cfg.RetryPerTryTimeoutMS < 50 {
		return fmt.Errorf("retry_per_try_timeout_ms must be >= 50 when set")
	}
	return nil
}

func validateProxyPassiveCircuitConfiguration(cfg ProxyRulesConfig) error {
	if cfg.PassiveHealthEnabled {
		if cfg.PassiveFailureThreshold > 32 {
			return fmt.Errorf("passive_failure_threshold must be <= 32")
		}
		if err := validateProxyRetryStatusCodes(cfg.PassiveUnhealthyStatusCodes, "passive_unhealthy_status_codes"); err != nil {
			return err
		}
	}
	if cfg.CircuitBreakerEnabled {
		if cfg.CircuitBreakerOpenSec > 3600 {
			return fmt.Errorf("circuit_breaker_open_sec must be <= 3600")
		}
		if cfg.CircuitBreakerHalfOpenRequests > 16 {
			return fmt.Errorf("circuit_breaker_half_open_requests must be <= 16")
		}
	}
	return nil
}

func proxyBuildRetryPolicy(cfg ProxyRulesConfig) proxyRetryPolicy {
	policy := proxyRetryPolicy{
		Attempts: cfg.RetryAttempts,
		Backoff:  time.Duration(cfg.RetryBackoffMS) * time.Millisecond,
	}
	if cfg.RetryPerTryTimeoutMS > 0 {
		policy.PerTryTimeout = time.Duration(cfg.RetryPerTryTimeoutMS) * time.Millisecond
	}
	if len(cfg.RetryStatusCodes) == 0 {
		policy.StatusCodes = map[int]struct{}{
			http.StatusBadGateway:         {},
			http.StatusServiceUnavailable: {},
			http.StatusGatewayTimeout:     {},
		}
	} else {
		policy.StatusCodes = make(map[int]struct{}, len(cfg.RetryStatusCodes))
		for _, code := range cfg.RetryStatusCodes {
			policy.StatusCodes[code] = struct{}{}
		}
	}
	methods := cfg.RetryMethods
	if len(methods) == 0 {
		methods = []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	}
	policy.Methods = make(map[string]struct{}, len(methods))
	for _, method := range methods {
		policy.Methods[strings.ToUpper(strings.TrimSpace(method))] = struct{}{}
	}
	return policy
}

func (p proxyRetryPolicy) Enabled() bool {
	return p.Attempts > 0
}

func (p proxyRetryPolicy) AllowsMethod(method string) bool {
	if !p.Enabled() {
		return false
	}
	_, ok := p.Methods[strings.ToUpper(strings.TrimSpace(method))]
	return ok
}

func (p proxyRetryPolicy) RetryableStatus(code int) bool {
	_, ok := p.StatusCodes[code]
	return ok
}

func resolveProxyRouteTargets(req *http.Request, cfg ProxyRulesConfig, action ProxyRouteAction, health *upstreamHealthMonitor) ([]proxyRouteTargetCandidate, error) {
	candidates, options, err := buildProxyRouteTargetCandidates(cfg, action)
	if err != nil {
		return nil, err
	}
	return orderProxyRouteCandidates(req, candidates, options, health), nil
}

func buildProxyRouteTargetCandidates(cfg ProxyRulesConfig, action ProxyRouteAction) ([]proxyRouteTargetCandidate, proxyRouteTargetSelectionOptions, error) {
	options := proxyRouteTargetSelectionOptions{
		HashPolicy:   cfg.HashPolicy,
		HashKey:      cfg.HashKey,
		UseLeastConn: cfg.LoadBalancingStrategy == "least_conn",
	}
	if action.HashPolicy != "" {
		options.HashPolicy = action.HashPolicy
		options.HashKey = action.HashKey
	}
	if strings.TrimSpace(action.CanaryUpstream) != "" {
		primary, err := proxyRouteTargetCandidateFromRef(cfg, action.Upstream, 100-action.CanaryWeightPct)
		if err != nil {
			return nil, proxyRouteTargetSelectionOptions{}, err
		}
		canary, err := proxyRouteTargetCandidateFromRef(cfg, action.CanaryUpstream, action.CanaryWeightPct)
		if err != nil {
			return nil, proxyRouteTargetSelectionOptions{}, err
		}
		options.UseLeastConn = false
		return []proxyRouteTargetCandidate{primary, canary}, options, nil
	}
	if strings.TrimSpace(action.Upstream) != "" {
		candidate, err := proxyRouteTargetCandidateFromRef(cfg, action.Upstream, 1)
		if err != nil {
			return nil, proxyRouteTargetSelectionOptions{}, err
		}
		return []proxyRouteTargetCandidate{candidate}, options, nil
	}
	defs := proxyConfiguredUpstreams(cfg)
	out := make([]proxyRouteTargetCandidate, 0, len(defs))
	for _, upstream := range defs {
		target, err := parseProxyUpstreamURL("upstream_url", upstream.URL)
		if err != nil {
			return nil, proxyRouteTargetSelectionOptions{}, err
		}
		out = append(out, proxyRouteTargetCandidate{
			Key:     proxyBackendLookupKey(upstream.Name, target.String()),
			Name:    upstream.Name,
			Target:  target,
			Weight:  proxyPositiveWeight(upstream.Weight),
			Managed: true,
		})
	}
	return out, options, nil
}

func proxyRouteTargetCandidateFromRef(cfg ProxyRulesConfig, ref string, weight int) (proxyRouteTargetCandidate, error) {
	ref = strings.TrimSpace(ref)
	if target, err := parseProxyUpstreamURL("action.upstream", ref); err == nil {
		for _, upstream := range cfg.Upstreams {
			if strings.TrimSpace(upstream.URL) != target.String() {
				continue
			}
			return proxyRouteTargetCandidate{
				Key:     proxyBackendLookupKey(upstream.Name, target.String()),
				Name:    upstream.Name,
				Target:  target,
				Weight:  proxyPositiveWeight(weight),
				Managed: true,
			}, nil
		}
		return proxyRouteTargetCandidate{
			Name:    ref,
			Target:  target,
			Weight:  proxyPositiveWeight(weight),
			Managed: false,
		}, nil
	}
	for _, upstream := range cfg.Upstreams {
		if upstream.Name != ref {
			continue
		}
		target, err := parseProxyUpstreamURL("action.upstream", upstream.URL)
		if err != nil {
			return proxyRouteTargetCandidate{}, err
		}
		return proxyRouteTargetCandidate{
			Key:     proxyBackendLookupKey(upstream.Name, target.String()),
			Name:    upstream.Name,
			Target:  target,
			Weight:  proxyPositiveWeight(weight),
			Managed: true,
		}, nil
	}
	return proxyRouteTargetCandidate{}, fmt.Errorf("unknown upstream reference %q", ref)
}

func orderProxyRouteCandidates(req *http.Request, candidates []proxyRouteTargetCandidate, options proxyRouteTargetSelectionOptions, health *upstreamHealthMonitor) []proxyRouteTargetCandidate {
	if len(candidates) <= 1 {
		return append([]proxyRouteTargetCandidate(nil), candidates...)
	}
	availability := proxyCandidateAvailabilities(health, candidates)
	eligible := make([]int, 0, len(candidates))
	fallback := make([]int, 0, len(candidates))
	for i, candidate := range candidates {
		avail := availability[candidate.Key]
		if !candidate.Managed || avail.Selectable {
			eligible = append(eligible, i)
			continue
		}
		fallback = append(fallback, i)
	}
	if len(eligible) == 0 {
		eligible = append(eligible, fallback...)
		fallback = nil
	}
	if len(eligible) == 0 {
		return append([]proxyRouteTargetCandidate(nil), candidates...)
	}

	order := make([]int, 0, len(candidates))
	switch {
	case options.UseLeastConn && len(eligible) > 1:
		sort.SliceStable(eligible, func(i, j int) bool {
			leftCandidate := candidates[eligible[i]]
			rightCandidate := candidates[eligible[j]]
			left := int64(availability[leftCandidate.Key].InFlight) * int64(proxyPositiveWeight(rightCandidate.Weight))
			right := int64(availability[rightCandidate.Key].InFlight) * int64(proxyPositiveWeight(leftCandidate.Weight))
			if left == right {
				return leftCandidate.Name < rightCandidate.Name
			}
			return left < right
		})
		order = append(order, eligible...)
	case options.HashPolicy != "":
		selected := proxyWeightedHashIndex(candidates, eligible, proxyRouteSelectionValue(req, options.HashPolicy, options.HashKey))
		order = append(order, eligible[selected])
		for i, idx := range eligible {
			if i == selected {
				continue
			}
			order = append(order, idx)
		}
	default:
		selected := proxyWeightedCursorIndex(candidates, eligible)
		order = append(order, eligible[selected])
		for offset := 1; offset < len(eligible); offset++ {
			order = append(order, eligible[(selected+offset)%len(eligible)])
		}
	}
	order = append(order, fallback...)
	out := make([]proxyRouteTargetCandidate, 0, len(order))
	for _, idx := range order {
		out = append(out, candidates[idx])
	}
	return out
}

func proxyCandidateAvailabilities(health *upstreamHealthMonitor, candidates []proxyRouteTargetCandidate) map[string]proxyCandidateAvailability {
	out := make(map[string]proxyCandidateAvailability, len(candidates))
	if health == nil {
		return out
	}
	now := time.Now().UTC()
	health.mu.RLock()
	defer health.mu.RUnlock()
	backends := make(map[string]*proxyBackendState, len(health.backends))
	for _, backend := range health.backends {
		if backend == nil {
			continue
		}
		backends[backend.Key] = backend
	}
	for _, candidate := range candidates {
		if !candidate.Managed {
			out[candidate.Key] = proxyCandidateAvailability{Selectable: true}
			continue
		}
		backend, ok := backends[candidate.Key]
		if !ok {
			out[candidate.Key] = proxyCandidateAvailability{Selectable: true}
			continue
		}
		out[candidate.Key] = proxyCandidateAvailability{
			Selectable: proxyBackendSelectableLocked(health.cfg, backend, now),
			InFlight:   backend.InFlight,
		}
	}
	return out
}

func proxyRouteSelectionValue(req *http.Request, policy string, key string) string {
	switch normalizeProxyHashPolicy(policy) {
	case "client_ip":
		if req == nil {
			return ""
		}
		if forwarded := strings.TrimSpace(req.Header.Get("X-Forwarded-For")); forwarded != "" {
			return strings.TrimSpace(strings.Split(forwarded, ",")[0])
		}
		return requestRemoteIP(req)
	case "header":
		if req == nil {
			return ""
		}
		return strings.TrimSpace(req.Header.Get(key))
	case "cookie":
		if req == nil {
			return ""
		}
		c, err := req.Cookie(key)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(c.Value)
	case "jwt_sub":
		if req == nil {
			return ""
		}
		return extractRateLimitJWTSub(req, defaultRateLimitJWTHeaderNames, defaultRateLimitJWTCookieNames)
	default:
		return ""
	}
}

func proxyWeightedHashIndex(candidates []proxyRouteTargetCandidate, eligible []int, value string) int {
	if strings.TrimSpace(value) == "" {
		return proxyWeightedCursorIndex(candidates, eligible)
	}
	totalWeight := 0
	for _, idx := range eligible {
		totalWeight += proxyPositiveWeight(candidates[idx].Weight)
	}
	if totalWeight <= 0 {
		return 0
	}
	sum := sha256.Sum256([]byte(value))
	bucket := int(binary.BigEndian.Uint64(sum[:8]) % uint64(totalWeight))
	acc := 0
	for pos, idx := range eligible {
		acc += proxyPositiveWeight(candidates[idx].Weight)
		if bucket < acc {
			return pos
		}
	}
	return 0
}

func proxyWeightedCursorIndex(candidates []proxyRouteTargetCandidate, eligible []int) int {
	totalWeight := 0
	for _, idx := range eligible {
		totalWeight += proxyPositiveWeight(candidates[idx].Weight)
	}
	if totalWeight <= 0 {
		return 0
	}
	cursor := int((atomic.AddUint64(&proxySelectionCursor, 1) - 1) % uint64(totalWeight))
	acc := 0
	for pos, idx := range eligible {
		acc += proxyPositiveWeight(candidates[idx].Weight)
		if cursor < acc {
			return pos
		}
	}
	return 0
}

func proxyPositiveWeight(v int) int {
	if v <= 0 {
		return 1
	}
	return v
}

func proxyBackendLookupKey(name string, rawURL string) string {
	return fmt.Sprintf("%s|%s", strings.TrimSpace(name), strings.TrimSpace(rawURL))
}
