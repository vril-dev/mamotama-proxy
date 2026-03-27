package handler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var proxyRouteHeaderNamePattern = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

var proxyRouteRestrictedHeaders = map[string]struct{}{
	"Connection":          {},
	"Host":                {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Proxy-Connection":    {},
	"TE":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
	"X-Forwarded-For":     {},
	"X-Forwarded-Host":    {},
	"X-Forwarded-Proto":   {},
}

var proxyRouteRestrictedResponseHeaders = map[string]struct{}{
	"Connection":        {},
	"Content-Length":    {},
	"Keep-Alive":        {},
	"Proxy-Connection":  {},
	"Set-Cookie":        {},
	"TE":                {},
	"Trailer":           {},
	"Transfer-Encoding": {},
	"Upgrade":           {},
}

type ProxyRoute struct {
	Name     string           `json:"name,omitempty"`
	Enabled  *bool            `json:"enabled,omitempty"`
	Priority int              `json:"priority"`
	Match    ProxyRouteMatch  `json:"match,omitempty"`
	Action   ProxyRouteAction `json:"action"`
}

type ProxyRouteMatch struct {
	Hosts []string             `json:"hosts,omitempty"`
	Path  *ProxyRoutePathMatch `json:"path,omitempty"`
}

type ProxyRoutePathMatch struct {
	Type     string `json:"type"`
	Value    string `json:"value"`
	compiled *regexp.Regexp
}

type ProxyRouteAction struct {
	Upstream        string                      `json:"upstream,omitempty"`
	PathRewrite     *ProxyRoutePathRewrite      `json:"path_rewrite,omitempty"`
	RequestHeaders  *ProxyRouteHeaderOperations `json:"request_headers,omitempty"`
	ResponseHeaders *ProxyRouteHeaderOperations `json:"response_headers,omitempty"`
}

type ProxyRoutePathRewrite struct {
	Prefix string `json:"prefix"`
}

type ProxyRouteHeaderOperations struct {
	Set    map[string]string `json:"set,omitempty"`
	Add    map[string]string `json:"add,omitempty"`
	Remove []string          `json:"remove,omitempty"`
}

type ProxyDefaultRoute struct {
	Name    string           `json:"name,omitempty"`
	Enabled *bool            `json:"enabled,omitempty"`
	Action  ProxyRouteAction `json:"action"`
}

type proxyRouteResolutionSource string

const (
	proxyRouteResolutionLegacy  proxyRouteResolutionSource = "legacy_upstream"
	proxyRouteResolutionRoute   proxyRouteResolutionSource = "route"
	proxyRouteResolutionDefault proxyRouteResolutionSource = "default_route"
)

type proxyRouteDecision struct {
	Source              proxyRouteResolutionSource
	RouteName           string
	OriginalHost        string
	OriginalPath        string
	RewrittenHost       string
	RewrittenPath       string
	RewrittenRawPath    string
	SelectedUpstream    string
	SelectedUpstreamURL string
	Target              *url.URL
	HealthKey           string
	RequestHeaderOps    ProxyRouteHeaderOperations
	ResponseHeaderOps   ProxyRouteHeaderOperations
	LogSelection        bool
}

type proxyRouteDryRunResult struct {
	Source              string `json:"source"`
	RouteName           string `json:"route_name,omitempty"`
	OriginalHost        string `json:"original_host,omitempty"`
	OriginalPath        string `json:"original_path,omitempty"`
	RewrittenHost       string `json:"rewritten_host,omitempty"`
	RewrittenPath       string `json:"rewritten_path,omitempty"`
	SelectedUpstream    string `json:"selected_upstream,omitempty"`
	SelectedUpstreamURL string `json:"selected_upstream_url,omitempty"`
	FinalURL            string `json:"final_url,omitempty"`
}

func normalizeProxyRoutes(in []ProxyRoute) []ProxyRoute {
	if len(in) == 0 {
		return nil
	}
	out := make([]ProxyRoute, 0, len(in))
	for i, route := range in {
		next := route
		next.Name = strings.TrimSpace(next.Name)
		if next.Name == "" {
			next.Name = fmt.Sprintf("route-%d", i+1)
		}
		next.Match.Hosts = normalizeProxyRouteHosts(next.Match.Hosts)
		next.Match.Path = normalizeProxyRoutePathMatch(next.Match.Path)
		next.Action = normalizeProxyRouteAction(next.Action)
		out = append(out, next)
	}
	return out
}

func normalizeProxyDefaultRoute(in *ProxyDefaultRoute) *ProxyDefaultRoute {
	if in == nil {
		return nil
	}
	out := *in
	out.Name = strings.TrimSpace(out.Name)
	if out.Name == "" {
		out.Name = "default"
	}
	out.Action = normalizeProxyRouteAction(out.Action)
	return &out
}

func normalizeProxyRouteAction(in ProxyRouteAction) ProxyRouteAction {
	out := in
	out.Upstream = strings.TrimSpace(out.Upstream)
	out.PathRewrite = normalizeProxyRoutePathRewrite(out.PathRewrite)
	out.RequestHeaders = normalizeProxyRouteHeaderOperations(out.RequestHeaders)
	out.ResponseHeaders = normalizeProxyRouteHeaderOperations(out.ResponseHeaders)
	return out
}

func normalizeProxyRoutePathMatch(in *ProxyRoutePathMatch) *ProxyRoutePathMatch {
	if in == nil {
		return nil
	}
	out := *in
	out.Type = strings.ToLower(strings.TrimSpace(out.Type))
	switch out.Type {
	case "prefix":
		out.Value = normalizeProxyRoutePrefix(out.Value)
	case "exact":
		out.Value = normalizeProxyRouteExactPath(out.Value)
	case "regex":
		out.Value = strings.TrimSpace(out.Value)
	default:
		out.Value = strings.TrimSpace(out.Value)
	}
	out.compiled = nil
	return &out
}

func normalizeProxyRoutePathRewrite(in *ProxyRoutePathRewrite) *ProxyRoutePathRewrite {
	if in == nil {
		return nil
	}
	out := *in
	out.Prefix = normalizeProxyRoutePrefix(out.Prefix)
	return &out
}

func normalizeProxyRouteHeaderOperations(in *ProxyRouteHeaderOperations) *ProxyRouteHeaderOperations {
	if in == nil {
		return nil
	}
	out := &ProxyRouteHeaderOperations{
		Set: make(map[string]string, len(in.Set)),
		Add: make(map[string]string, len(in.Add)),
	}
	for name, value := range in.Set {
		out.Set[canonicalProxyRouteHeaderName(name)] = value
	}
	for name, value := range in.Add {
		out.Add[canonicalProxyRouteHeaderName(name)] = value
	}
	if len(out.Set) == 0 {
		out.Set = nil
	}
	if len(out.Add) == 0 {
		out.Add = nil
	}
	if len(in.Remove) > 0 {
		out.Remove = make([]string, 0, len(in.Remove))
		seen := map[string]struct{}{}
		for _, name := range in.Remove {
			next := canonicalProxyRouteHeaderName(name)
			if next == "" {
				continue
			}
			if _, ok := seen[next]; ok {
				continue
			}
			seen[next] = struct{}{}
			out.Remove = append(out.Remove, next)
		}
		if len(out.Remove) == 0 {
			out.Remove = nil
		}
	}
	if out.Set == nil && out.Add == nil && len(out.Remove) == 0 {
		return nil
	}
	return out
}

func normalizeProxyRouteHosts(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, raw := range in {
		next := normalizeProxyHostPattern(raw)
		if next == "" {
			continue
		}
		if _, ok := seen[next]; ok {
			continue
		}
		seen[next] = struct{}{}
		out = append(out, next)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeProxyRouteExactPath(v string) string {
	out := strings.TrimSpace(v)
	if out == "" {
		return ""
	}
	if !strings.HasPrefix(out, "/") {
		out = "/" + out
	}
	return out
}

func normalizeProxyRoutePrefix(v string) string {
	out := normalizeProxyRouteExactPath(v)
	if out == "" || out == "/" {
		return "/"
	}
	return strings.TrimRight(out, "/")
}

func canonicalProxyRouteHeaderName(v string) string {
	return textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(v))
}

func normalizeProxyHostPattern(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if strings.HasPrefix(v, "*.") {
		return "*." + strings.TrimSuffix(strings.TrimPrefix(v, "*."), ".")
	}
	return strings.TrimSuffix(v, ".")
}

func proxyRouteEnabled(v *bool) bool {
	return v == nil || *v
}

func validateProxyRoutes(cfg ProxyRulesConfig) error {
	namedUpstreams := map[string]ProxyUpstream{}
	nameCounts := map[string]int{}
	for i, upstream := range cfg.Upstreams {
		if upstream.Weight <= 0 {
			return fmt.Errorf("upstreams[%d].weight must be > 0", i)
		}
		nameCounts[upstream.Name]++
		namedUpstreams[upstream.Name] = upstream
	}

	for i, route := range cfg.Routes {
		if err := validateProxyRouteMatch(route.Match, fmt.Sprintf("routes[%d].match", i)); err != nil {
			return err
		}
		if err := validateProxyRouteAction(route.Action, cfg, namedUpstreams, nameCounts, fmt.Sprintf("routes[%d].action", i)); err != nil {
			return err
		}
		if route.Action.PathRewrite != nil && route.Match.Path == nil {
			return fmt.Errorf("routes[%d].action.path_rewrite requires match.path", i)
		}
		if route.Action.PathRewrite != nil && route.Match.Path != nil && route.Match.Path.Type == "regex" {
			return fmt.Errorf("routes[%d].action.path_rewrite does not support regex path matches", i)
		}
	}
	if cfg.DefaultRoute != nil && proxyRouteEnabled(cfg.DefaultRoute.Enabled) {
		if err := validateProxyRouteAction(cfg.DefaultRoute.Action, cfg, namedUpstreams, nameCounts, "default_route.action"); err != nil {
			return err
		}
	}

	if !proxyRulesHasLegacyUpstream(cfg) && !proxyRoutesProvideFallback(cfg) {
		return fmt.Errorf("default_route or a catch-all route is required when upstream_url/upstreams are not set")
	}
	return nil
}

func validateProxyRouteMatch(match ProxyRouteMatch, field string) error {
	for i, host := range match.Hosts {
		if err := validateProxyRouteHostPattern(host); err != nil {
			return fmt.Errorf("%s.hosts[%d]: %w", field, i, err)
		}
	}
	if match.Path == nil {
		return nil
	}
	switch match.Path.Type {
	case "exact", "prefix", "regex":
	default:
		return fmt.Errorf("%s.path.type must be exact, prefix, or regex", field)
	}
	if strings.TrimSpace(match.Path.Value) == "" {
		return fmt.Errorf("%s.path.value is required", field)
	}
	if match.Path.Type != "regex" && !strings.HasPrefix(match.Path.Value, "/") {
		return fmt.Errorf("%s.path.value must start with '/'", field)
	}
	if match.Path.Type == "regex" {
		compiled, err := regexp.Compile(match.Path.Value)
		if err != nil {
			return fmt.Errorf("%s.path.value regex compile error: %w", field, err)
		}
		match.Path.compiled = compiled
	}
	return nil
}

func validateProxyRouteAction(action ProxyRouteAction, cfg ProxyRulesConfig, namedUpstreams map[string]ProxyUpstream, nameCounts map[string]int, field string) error {
	upstream := strings.TrimSpace(action.Upstream)
	if upstream == "" && !proxyRulesHasLegacyUpstream(cfg) {
		return fmt.Errorf("%s.upstream is required when upstream_url/upstreams are not set", field)
	}
	if upstream != "" {
		if _, err := parseProxyUpstreamURL(field+".upstream", upstream); err != nil {
			up, ok := namedUpstreams[upstream]
			if !ok {
				return fmt.Errorf("%s.upstream must be an absolute http(s) URL or a configured upstream name", field)
			}
			if nameCounts[upstream] > 1 {
				return fmt.Errorf("%s.upstream references duplicated upstream name %q", field, upstream)
			}
			if !up.Enabled {
				return fmt.Errorf("%s.upstream references disabled upstream %q", field, upstream)
			}
		}
	}
	if action.PathRewrite != nil {
		if strings.TrimSpace(action.PathRewrite.Prefix) == "" {
			return fmt.Errorf("%s.path_rewrite.prefix is required", field)
		}
		if !strings.HasPrefix(action.PathRewrite.Prefix, "/") {
			return fmt.Errorf("%s.path_rewrite.prefix must start with '/'", field)
		}
	}
	if action.RequestHeaders != nil {
		if err := validateProxyRouteHeaderOperations(*action.RequestHeaders, field+".request_headers", proxyRouteRestrictedHeaders, "route request_headers"); err != nil {
			return err
		}
	}
	if action.ResponseHeaders != nil {
		if err := validateProxyRouteHeaderOperations(*action.ResponseHeaders, field+".response_headers", proxyRouteRestrictedResponseHeaders, "route response_headers"); err != nil {
			return err
		}
	}
	return nil
}

func validateProxyRouteHeaderOperations(ops ProxyRouteHeaderOperations, field string, restricted map[string]struct{}, kind string) error {
	seen := map[string]string{}
	for name := range ops.Set {
		if err := validateProxyRouteHeaderName(name, restricted, kind); err != nil {
			return fmt.Errorf("%s.set.%s: %w", field, name, err)
		}
		seen[name] = "set"
	}
	for name := range ops.Add {
		if err := validateProxyRouteHeaderName(name, restricted, kind); err != nil {
			return fmt.Errorf("%s.add.%s: %w", field, name, err)
		}
		if prev, ok := seen[name]; ok {
			return fmt.Errorf("%s.add.%s conflicts with %s.%s", field, name, field, prev)
		}
		seen[name] = "add"
	}
	for _, name := range ops.Remove {
		if err := validateProxyRouteHeaderName(name, restricted, kind); err != nil {
			return fmt.Errorf("%s.remove.%s: %w", field, name, err)
		}
		if prev, ok := seen[name]; ok {
			return fmt.Errorf("%s.remove.%s conflicts with %s.%s", field, name, field, prev)
		}
		seen[name] = "remove"
	}
	return nil
}

func validateProxyRouteHeaderName(name string, restricted map[string]struct{}, kind string) error {
	if name == "" {
		return fmt.Errorf("header name is required")
	}
	if !proxyRouteHeaderNamePattern.MatchString(name) {
		return fmt.Errorf("invalid header name")
	}
	if _, ok := restricted[canonicalProxyRouteHeaderName(name)]; ok {
		return fmt.Errorf("header is not allowed in %s", kind)
	}
	return nil
}

func validateProxyRouteHostPattern(host string) error {
	if host == "" {
		return fmt.Errorf("host is required")
	}
	if strings.Contains(host, "/") {
		return fmt.Errorf("host must not contain '/'")
	}
	if strings.Contains(host, "*") {
		if !strings.HasPrefix(host, "*.") || strings.Count(host, "*") != 1 {
			return fmt.Errorf("wildcard host must use the form *.example.com")
		}
		if strings.TrimPrefix(host, "*.") == "" {
			return fmt.Errorf("wildcard host suffix is required")
		}
	}
	return nil
}

func proxyRulesHasLegacyUpstream(cfg ProxyRulesConfig) bool {
	return strings.TrimSpace(cfg.UpstreamURL) != "" || len(cfg.Upstreams) > 0
}

func proxyRouteFallbackTarget(cfg ProxyRulesConfig) (*url.URL, bool, error) {
	if cfg.DefaultRoute != nil && proxyRouteEnabled(cfg.DefaultRoute.Enabled) {
		if target, ok, err := proxyRouteConfiguredTarget(cfg, cfg.DefaultRoute.Action.Upstream); err != nil {
			return nil, false, err
		} else if ok {
			return target, true, nil
		}
	}
	for _, route := range cfg.Routes {
		if !proxyRouteEnabled(route.Enabled) {
			continue
		}
		if target, ok, err := proxyRouteConfiguredTarget(cfg, route.Action.Upstream); err != nil {
			return nil, false, err
		} else if ok {
			return target, true, nil
		}
	}
	return nil, false, nil
}

func proxyRouteConfiguredTarget(cfg ProxyRulesConfig, ref string) (*url.URL, bool, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, false, nil
	}
	if target, err := parseProxyUpstreamURL("action.upstream", ref); err == nil {
		return target, true, nil
	}
	for _, upstream := range cfg.Upstreams {
		if upstream.Name != ref {
			continue
		}
		target, err := parseProxyUpstreamURL("action.upstream", upstream.URL)
		if err != nil {
			return nil, false, err
		}
		return target, true, nil
	}
	return nil, false, nil
}

func proxyDirectRouteTarget(ref string) (*url.URL, bool) {
	target, err := parseProxyUpstreamURL("action.upstream", strings.TrimSpace(ref))
	if err != nil {
		return nil, false
	}
	return target, true
}

func proxyRoutesProvideFallback(cfg ProxyRulesConfig) bool {
	if cfg.DefaultRoute != nil && proxyRouteEnabled(cfg.DefaultRoute.Enabled) && strings.TrimSpace(cfg.DefaultRoute.Action.Upstream) != "" {
		return true
	}
	for _, route := range cfg.Routes {
		if !proxyRouteEnabled(route.Enabled) {
			continue
		}
		if len(route.Match.Hosts) == 0 && route.Match.Path == nil && strings.TrimSpace(route.Action.Upstream) != "" {
			return true
		}
	}
	return false
}

func withProxyRouteDecision(ctx context.Context, decision proxyRouteDecision) context.Context {
	return context.WithValue(ctx, ctxKeyRouteDecision, decision)
}

func proxyRouteDecisionFromContext(ctx context.Context) (proxyRouteDecision, bool) {
	if ctx == nil {
		return proxyRouteDecision{}, false
	}
	decision, ok := ctx.Value(ctxKeyRouteDecision).(proxyRouteDecision)
	return decision, ok
}

func appendProxyRouteLogFields(evt map[string]any, req *http.Request) {
	if evt == nil {
		return
	}
	if req != nil {
		evt["original_host"] = req.Host
		evt["original_path"] = requestPath(req)
	}
	decision, ok := proxyRouteDecisionFromContext(requestContext(req))
	if !ok {
		return
	}
	if decision.OriginalHost != "" {
		evt["original_host"] = decision.OriginalHost
	}
	if decision.OriginalPath != "" {
		evt["original_path"] = decision.OriginalPath
	}
	evt["route_source"] = string(decision.Source)
	if decision.RouteName != "" {
		evt["selected_route"] = decision.RouteName
	}
	if decision.SelectedUpstream != "" {
		evt["selected_upstream"] = decision.SelectedUpstream
	}
	if decision.SelectedUpstreamURL != "" {
		evt["selected_upstream_url"] = decision.SelectedUpstreamURL
	}
	if decision.RewrittenHost != "" {
		evt["rewritten_host"] = decision.RewrittenHost
	}
	if decision.RewrittenPath != "" {
		evt["rewritten_path"] = decision.RewrittenPath
	}
}

func requestContext(req *http.Request) context.Context {
	if req == nil {
		return nil
	}
	return req.Context()
}

func resolveProxyRouteDecision(req *http.Request, cfg ProxyRulesConfig, health *upstreamHealthMonitor) (proxyRouteDecision, error) {
	originalPath := requestPath(req)
	if originalPath == "" {
		originalPath = "/"
	}
	originalRawPath := proxyRouteRawPath(req)
	originalHost := strings.TrimSpace(req.Host)

	for _, idx := range sortedProxyRouteIndexes(cfg.Routes) {
		route := cfg.Routes[idx]
		if !proxyRouteEnabled(route.Enabled) {
			continue
		}
		if !proxyRouteMatches(route.Match, originalHost, originalPath) {
			continue
		}
		decision, err := buildProxyRouteDecision(originalHost, originalPath, originalRawPath, route.Name, proxyRouteResolutionRoute, route.Match.Path, route.Action, cfg, health)
		if err != nil {
			return proxyRouteDecision{}, err
		}
		decision.LogSelection = true
		return decision, nil
	}

	if cfg.DefaultRoute != nil && proxyRouteEnabled(cfg.DefaultRoute.Enabled) {
		decision, err := buildProxyRouteDecision(originalHost, originalPath, originalRawPath, cfg.DefaultRoute.Name, proxyRouteResolutionDefault, nil, cfg.DefaultRoute.Action, cfg, health)
		if err != nil {
			return proxyRouteDecision{}, err
		}
		decision.LogSelection = true
		return decision, nil
	}

	decision, err := buildProxyRouteDecision(originalHost, originalPath, originalRawPath, "legacy-upstream", proxyRouteResolutionLegacy, nil, ProxyRouteAction{}, cfg, health)
	if err != nil {
		return proxyRouteDecision{}, err
	}
	decision.LogSelection = len(cfg.Routes) > 0 || cfg.DefaultRoute != nil
	return decision, nil
}

func buildProxyRouteDecision(originalHost string, originalPath string, originalRawPath string, routeName string, source proxyRouteResolutionSource, match *ProxyRoutePathMatch, action ProxyRouteAction, cfg ProxyRulesConfig, health *upstreamHealthMonitor) (proxyRouteDecision, error) {
	target, selectedUpstream, selectedURL, healthKey, err := resolveProxyRouteTarget(cfg, action.Upstream, health)
	if err != nil {
		return proxyRouteDecision{}, err
	}
	rewrittenPath := originalPath
	rewrittenRawPath := originalRawPath
	if action.PathRewrite != nil {
		rewrittenPath, err = rewriteProxyRoutePath(originalPath, match, action.PathRewrite.Prefix)
		if err != nil {
			if health != nil && healthKey != "" {
				health.ReleaseTarget(healthKey)
			}
			return proxyRouteDecision{}, err
		}
		if originalRawPath != "" {
			rewrittenRawPath, err = rewriteProxyRoutePath(originalRawPath, match, action.PathRewrite.Prefix)
			if err != nil {
				if health != nil && healthKey != "" {
					health.ReleaseTarget(healthKey)
				}
				return proxyRouteDecision{}, err
			}
		}
	}
	return proxyRouteDecision{
		Source:              source,
		RouteName:           routeName,
		OriginalHost:        originalHost,
		OriginalPath:        originalPath,
		RewrittenHost:       target.Host,
		RewrittenPath:       rewrittenPath,
		RewrittenRawPath:    rewrittenRawPath,
		SelectedUpstream:    selectedUpstream,
		SelectedUpstreamURL: selectedURL,
		Target:              target,
		HealthKey:           healthKey,
		RequestHeaderOps:    valueOrZero(action.RequestHeaders),
		ResponseHeaderOps:   valueOrZero(action.ResponseHeaders),
		LogSelection:        source != proxyRouteResolutionLegacy,
	}, nil
}

func resolveProxyRouteTarget(cfg ProxyRulesConfig, ref string, health *upstreamHealthMonitor) (*url.URL, string, string, string, error) {
	ref = strings.TrimSpace(ref)
	if ref != "" {
		if target, err := parseProxyUpstreamURL("action.upstream", ref); err == nil {
			return target, ref, target.String(), "", nil
		}
		for _, upstream := range cfg.Upstreams {
			if upstream.Name != ref {
				continue
			}
			target, err := parseProxyUpstreamURL("action.upstream", upstream.URL)
			if err != nil {
				return nil, "", "", "", err
			}
			return target, upstream.Name, target.String(), "", nil
		}
		return nil, "", "", "", fmt.Errorf("unknown upstream reference %q", ref)
	}

	if health != nil {
		if selection, ok := health.SelectTarget(); ok && selection.Target != nil {
			name := strings.TrimSpace(selection.Name)
			if name == "" {
				name = strings.TrimSpace(selection.Key)
			}
			return selection.Target, name, selection.Target.String(), selection.Key, nil
		}
	}
	target, err := proxyPrimaryTarget(cfg)
	if err != nil {
		return nil, "", "", "", err
	}
	return target, proxyDefaultUpstreamName(cfg), target.String(), "", nil
}

func proxyDefaultUpstreamName(cfg ProxyRulesConfig) string {
	for _, upstream := range cfg.Upstreams {
		if upstream.Enabled {
			return upstream.Name
		}
	}
	if strings.TrimSpace(cfg.UpstreamURL) != "" {
		return "primary"
	}
	return ""
}

func sortedProxyRouteIndexes(routes []ProxyRoute) []int {
	idxs := make([]int, len(routes))
	for i := range routes {
		idxs[i] = i
	}
	sort.SliceStable(idxs, func(i, j int) bool {
		return routes[idxs[i]].Priority < routes[idxs[j]].Priority
	})
	return idxs
}

func proxyRouteMatches(match ProxyRouteMatch, host string, reqPath string) bool {
	if !proxyRouteHostsMatch(match.Hosts, host) {
		return false
	}
	if match.Path == nil {
		return true
	}
	ok, _ := proxyRoutePathMatchDetails(match.Path, reqPath)
	return ok
}

func proxyRouteHostsMatch(patterns []string, host string) bool {
	if len(patterns) == 0 {
		return true
	}
	reqHost := normalizeProxyRequestHost(host)
	for _, pattern := range patterns {
		if proxyRouteHostMatches(pattern, reqHost) {
			return true
		}
	}
	return false
}

func proxyRouteHostMatches(pattern string, reqHost string) bool {
	pattern = normalizeProxyHostPattern(pattern)
	if pattern == "" || reqHost == "" {
		return false
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := strings.TrimPrefix(pattern, "*.")
		return reqHost != suffix && strings.HasSuffix(reqHost, "."+suffix)
	}
	return reqHost == pattern
}

func normalizeProxyRequestHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if parsed, err := url.Parse("http://" + host); err == nil && parsed.Hostname() != "" {
		return normalizeProxyHostPattern(parsed.Hostname())
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return normalizeProxyHostPattern(strings.Trim(strings.ToLower(h), "[]"))
	}
	return normalizeProxyHostPattern(strings.Trim(host, "[]"))
}

func proxyRoutePathMatchDetails(match *ProxyRoutePathMatch, reqPath string) (bool, string) {
	if match == nil {
		return true, ""
	}
	if reqPath == "" {
		reqPath = "/"
	}
	switch match.Type {
	case "exact":
		return reqPath == match.Value, ""
	case "prefix":
		if match.Value == "/" {
			return true, reqPath
		}
		if reqPath == match.Value {
			return true, ""
		}
		prefixWithSlash := match.Value + "/"
		if strings.HasPrefix(reqPath, prefixWithSlash) {
			return true, strings.TrimPrefix(reqPath, match.Value)
		}
		return false, ""
	case "regex":
		compiled, err := proxyRouteCompiledRegexp(match)
		if err != nil {
			return false, ""
		}
		return compiled.MatchString(reqPath), ""
	default:
		return false, ""
	}
}

func rewriteProxyRoutePath(originalPath string, match *ProxyRoutePathMatch, rewritePrefix string) (string, error) {
	if match != nil && match.Type == "regex" {
		return "", fmt.Errorf("path rewrite does not support regex path matches")
	}
	ok, suffix := proxyRoutePathMatchDetails(match, originalPath)
	if !ok {
		return "", fmt.Errorf("path %q does not match route path rule", originalPath)
	}
	return joinProxyRoutePath(rewritePrefix, suffix), nil
}

func proxyRouteCompiledRegexp(match *ProxyRoutePathMatch) (*regexp.Regexp, error) {
	if match == nil || match.Type != "regex" {
		return nil, fmt.Errorf("regex path match is not configured")
	}
	if match.compiled != nil {
		return match.compiled, nil
	}
	compiled, err := regexp.Compile(match.Value)
	if err != nil {
		return nil, err
	}
	match.compiled = compiled
	return compiled, nil
}

func joinProxyRoutePath(prefix string, suffix string) string {
	prefix = normalizeProxyRoutePrefix(prefix)
	if prefix == "/" {
		if suffix == "" {
			return "/"
		}
		if strings.HasPrefix(suffix, "/") {
			return suffix
		}
		return "/" + suffix
	}
	if suffix == "" {
		return prefix
	}
	if strings.HasPrefix(suffix, "/") {
		return prefix + suffix
	}
	return prefix + "/" + suffix
}

func applyProxyRouteHeaders(header http.Header, ops ProxyRouteHeaderOperations) {
	if header == nil {
		return
	}
	for _, name := range ops.Remove {
		header.Del(name)
	}
	for name, value := range ops.Set {
		header.Set(name, value)
	}
	for name, value := range ops.Add {
		header.Add(name, value)
	}
}

func rewriteProxyOutgoingURL(out *http.Request, target *url.URL, rewrittenPath string, rewrittenRawPath string) {
	if out == nil || out.URL == nil {
		return
	}
	rewriteProxyTargetURL(out.URL, target, rewrittenPath, rewrittenRawPath, out.URL.RawQuery)
}

func rewriteProxyTargetURL(out *url.URL, target *url.URL, rewrittenPath string, rewrittenRawPath string, rawQuery string) {
	if out == nil || target == nil {
		return
	}
	reqURL := &url.URL{Path: rewrittenPath, RawPath: rewrittenRawPath, RawQuery: rawQuery}
	targetQuery := target.RawQuery
	out.Scheme = target.Scheme
	out.Host = target.Host
	out.Path, out.RawPath = joinProxyURLPath(target, reqURL)
	if targetQuery == "" || reqURL.RawQuery == "" {
		out.RawQuery = targetQuery + reqURL.RawQuery
	} else {
		out.RawQuery = targetQuery + "&" + reqURL.RawQuery
	}
}

func joinProxyURLPath(a, b *url.URL) (string, string) {
	if a == nil || b == nil {
		return "", ""
	}
	if a.RawPath == "" && b.RawPath == "" {
		return proxySingleJoiningSlash(a.Path, b.Path), ""
	}
	apath := a.EscapedPath()
	bpath := b.EscapedPath()
	aslash := strings.HasSuffix(apath, "/")
	bslash := strings.HasPrefix(bpath, "/")
	switch {
	case aslash && bslash:
		return a.Path + b.Path[1:], apath + bpath[1:]
	case !aslash && !bslash:
		return a.Path + "/" + b.Path, apath + "/" + bpath
	default:
		return a.Path + b.Path, apath + bpath
	}
}

func proxySingleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
}

func proxyRouteRawPath(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	return req.URL.RawPath
}

func proxyRouteDryRun(cfg ProxyRulesConfig, host string, path string) (proxyRouteDryRunResult, error) {
	return proxyRouteDryRunWithHealth(cfg, host, path, nil)
}

func proxyRouteDryRunWithHealth(cfg ProxyRulesConfig, host string, path string, health *upstreamHealthMonitor) (proxyRouteDryRunResult, error) {
	displayHost := strings.TrimSpace(host)
	if displayHost == "" {
		displayHost = "route.example.invalid"
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+host+path, nil)
	if err != nil {
		req, err = http.NewRequest(http.MethodGet, "http://"+displayHost+path, nil)
		if err != nil {
			return proxyRouteDryRunResult{}, err
		}
	}
	req.Host = displayHost
	decision, err := resolveProxyRouteDecision(req, cfg, health)
	if err != nil {
		return proxyRouteDryRunResult{}, err
	}
	return proxyRouteDryRunResult{
		Source:              string(decision.Source),
		RouteName:           decision.RouteName,
		OriginalHost:        decision.OriginalHost,
		OriginalPath:        decision.OriginalPath,
		RewrittenHost:       decision.RewrittenHost,
		RewrittenPath:       decision.RewrittenPath,
		SelectedUpstream:    decision.SelectedUpstream,
		SelectedUpstreamURL: decision.SelectedUpstreamURL,
		FinalURL:            finalProxyRouteURL(decision.Target, decision.RewrittenPath, decision.RewrittenRawPath),
	}, nil
}

func finalProxyRouteURL(target *url.URL, rewrittenPath string, rewrittenRawPath string) string {
	if target == nil {
		return ""
	}
	out := cloneURL(target)
	rewriteProxyTargetURL(out, target, rewrittenPath, rewrittenRawPath, "")
	return out.String()
}

func valueOrZero[T any](in *T) T {
	var zero T
	if in == nil {
		return zero
	}
	return *in
}
