package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/corazawaf/coraza/v3"
	"github.com/gin-gonic/gin"

	"mamotama/internal/bypassconf"
	"mamotama/internal/cacheconf"
	"mamotama/internal/waf"
)

type ctxKey string

const (
	ctxKeyReqID   ctxKey = "req_id"
	ctxKeyWafHit  ctxKey = "waf_hit"
	ctxKeyWafRule ctxKey = "waf_rules"
	ctxKeyIP      ctxKey = "client_ip"
	ctxKeyCountry ctxKey = "country"
)

func onProxyResponse(res *http.Response) error {
	if err := maybeBufferProxyResponseBody(res); err != nil {
		return err
	}
	annotateWAFHit(res)
	applyCacheHeaders(res)
	return nil
}

func annotateWAFHit(res *http.Response) {
	if res == nil || res.Request == nil {
		return
	}

	ctx := res.Request.Context()
	if hit, _ := ctx.Value(ctxKeyWafHit).(bool); !hit {
		return
	}
	if res.Header != nil {
		res.Header.Set("X-WAF-Hit", "1")
		if rid, _ := ctx.Value(ctxKeyWafRule).(string); rid != "" {
			res.Header.Set("X-WAF-RuleIDs", rid)
		}
	}

	reqID, _ := ctx.Value(ctxKeyReqID).(string)
	ip, _ := ctx.Value(ctxKeyIP).(string)
	country, _ := ctx.Value(ctxKeyCountry).(string)
	path := res.Request.URL.Path
	status := res.StatusCode
	emitJSONLog(map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"service": "coraza",
		"level":   "INFO",
		"event":   "waf_hit_allow",
		"req_id":  reqID,
		"ip":      ip,
		"country": country,
		"path":    path,
		"rules":   res.Header.Get("X-WAF-RuleIDs"),
		"status":  status,
	})
}

func applyCacheHeaders(res *http.Response) {
	rs := cacheconf.Get()
	if rs == nil || res == nil || res.Request == nil {
		return
	}

	method := res.Request.Method
	if method != http.MethodGet && method != http.MethodHead {
		return
	}

	path := res.Request.URL.Path
	if rule, allow := rs.Match(method, path); allow {
		ttl := rule.TTL
		if ttl <= 0 {
			ttl = 600
		}

		h := res.Header
		h.Set("X-Mamotama-Cacheable", "1")
		h.Set("X-Accel-Expires", strconv.Itoa(ttl))
		if len(rule.Vary) > 0 {
			h.Set("Vary", strings.Join(rule.Vary, ", "))
		}
	}
}

func ensureRequestID(c *gin.Context) string {
	reqID := c.Request.Header.Get("X-Request-ID")
	if reqID == "" {
		reqID = genReqID()
		c.Request.Header.Set("X-Request-ID", reqID)
	}
	c.Writer.Header().Set("X-Request-ID", reqID)

	return reqID
}

func selectWAFEngine(reqPath string) coraza.WAF {
	wafEngine := waf.GetBaseWAF()
	switch mr := bypassconf.Match(reqPath); mr.Action {
	case bypassconf.ACTION_BYPASS:
		return nil
	case bypassconf.ACTION_RULE:
		log.Printf("[BYPASS][RULE] %s extra=%s", reqPath, mr.ExtraRule)
		ruleWAF, err := waf.GetWAFForExtraRule(mr.ExtraRule)
		if err != nil {
			log.Printf("[BYPASS][RULE][WARN] %v (fallback=default-rules)", err)
			return wafEngine
		}

		return ruleWAF
	default:
		return wafEngine
	}
}

func setWAFContext(c *gin.Context, reqID, clientIP, country string, wafHit bool, ruleIDs string) {
	ctx := context.WithValue(c.Request.Context(), ctxKeyReqID, reqID)
	ctx = context.WithValue(ctx, ctxKeyIP, clientIP)
	ctx = context.WithValue(ctx, ctxKeyCountry, country)
	ctx = context.WithValue(ctx, ctxKeyWafHit, wafHit)
	ctx = context.WithValue(ctx, ctxKeyWafRule, ruleIDs)
	c.Request = c.Request.WithContext(ctx)
}

func ProxyHandler(c *gin.Context) {
	reqID := ensureRequestID(c)
	clientIP := requestClientIP(c)
	country := normalizeCountryCode(c.GetHeader("X-Country-Code"))

	if IsCountryBlocked(country) {
		evt := map[string]any{
			"ts":      time.Now().UTC().Format(time.RFC3339Nano),
			"service": "coraza",
			"level":   "WARN",
			"event":   "country_block",
			"req_id":  reqID,
			"ip":      clientIP,
			"country": country,
			"path":    c.Request.URL.Path,
			"status":  http.StatusForbidden,
		}
		emitJSONLog(evt)
		_ = appendEventToFile(evt)
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	botDecision := EvaluateBotDefense(c.Request, clientIP, time.Now().UTC())
	if !botDecision.Allowed {
		evt := map[string]any{
			"ts":      time.Now().UTC().Format(time.RFC3339Nano),
			"service": "coraza",
			"level":   "WARN",
			"event":   "bot_challenge",
			"req_id":  reqID,
			"ip":      clientIP,
			"country": country,
			"path":    c.Request.URL.Path,
			"status":  botDecision.Status,
			"mode":    botDecision.Mode,
		}
		emitJSONLog(evt)
		_ = appendEventToFile(evt)

		WriteBotDefenseChallenge(c.Writer, c.Request, botDecision)
		c.Abort()
		return
	}

	semanticEval := EvaluateSemantic(c.Request)
	if semanticEval.Score > 0 {
		c.Header("X-Mamotama-Semantic-Score", strconv.Itoa(semanticEval.Score))
	}
	if semanticEval.Action != semanticActionNone {
		evt := map[string]any{
			"ts":      time.Now().UTC().Format(time.RFC3339Nano),
			"service": "coraza",
			"level":   "WARN",
			"event":   "semantic_anomaly",
			"req_id":  reqID,
			"ip":      clientIP,
			"country": country,
			"path":    c.Request.URL.Path,
			"action":  semanticEval.Action,
			"score":   semanticEval.Score,
			"reasons": strings.Join(semanticEval.Reasons, ","),
		}
		emitJSONLog(evt)
		_ = appendEventToFile(evt)

		switch semanticEval.Action {
		case semanticActionChallenge:
			if !HasValidSemanticChallengeCookie(c.Request, clientIP, time.Now().UTC()) {
				WriteSemanticChallenge(c.Writer, c.Request, clientIP)
				c.Abort()
				return
			}
		case semanticActionBlock:
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
	}

	rateDecision := EvaluateRateLimit(c.Request.Method, c.Request.URL.Path, clientIP, country, time.Now().UTC())
	if !rateDecision.Allowed {
		evt := map[string]any{
			"ts":          time.Now().UTC().Format(time.RFC3339Nano),
			"service":     "coraza",
			"level":       "WARN",
			"event":       "rate_limited",
			"req_id":      reqID,
			"ip":          clientIP,
			"country":     country,
			"path":        c.Request.URL.Path,
			"status":      rateDecision.Status,
			"policy_id":   rateDecision.PolicyID,
			"limit":       rateDecision.Limit,
			"window_sec":  rateDecision.WindowSeconds,
			"rl_key_hash": rateDecision.Key,
		}
		emitJSONLog(evt)
		_ = appendEventToFile(evt)
		c.Header("Retry-After", strconv.Itoa(rateDecision.RetryAfterSeconds))
		c.AbortWithStatus(rateDecision.Status)
		return
	}

	reqPath := c.Request.URL.Path
	wafEngine := selectWAFEngine(reqPath)
	if wafEngine == nil {
		log.Printf("[BYPASS][HIT] %s -> skip WAF", reqPath)
		if err := maybeBufferProxyRequestBody(c.Request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		ServeProxy(c.Writer, c.Request)
		return
	}

	tx := wafEngine.NewTransaction()
	defer func() {
		tx.ProcessLogging()
		tx.Close()
	}()

	tx.ProcessURI(c.Request.URL.String(), c.Request.Method, c.Request.Proto)
	tx.AddRequestHeader("Host", c.Request.Host)
	if err := tx.ProcessRequestHeaders(); err != nil {
		log.Println("Header error:", err)
	}
	if _, err := tx.ProcessRequestBody(); err != nil {
		log.Println("Body error:", err)
	}

	wafHit := false
	ruleIDs := make([]string, 0, 4)
	for _, matched := range tx.MatchedRules() {
		wafHit = true
		if matched.Rule() != nil {
			ruleIDs = append(ruleIDs, strconv.Itoa(matched.Rule().ID()))
		}
	}

	setWAFContext(c, reqID, clientIP, country, wafHit, strings.Join(unique(ruleIDs), ","))

	if it := tx.Interruption(); it != nil {
		evt := map[string]any{
			"ts":      time.Now().UTC().Format(time.RFC3339Nano),
			"service": "coraza",
			"level":   "WARN",
			"event":   "waf_block",
			"req_id":  reqID,
			"ip":      clientIP,
			"country": country,
			"path":    c.Request.URL.Path,
			"rule_id": it.RuleID,
			"status":  it.Status,
		}
		emitJSONLog(evt)
		_ = appendEventToFile(evt)
		c.AbortWithStatus(it.Status)
		return
	}

	if err := maybeBufferProxyRequestBody(c.Request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ServeProxy(c.Writer, c.Request)
}

func genReqID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func unique(in []string) []string {
	m := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := m[s]; !ok && s != "" {
			m[s] = struct{}{}
			out = append(out, s)
		}
	}

	return out
}

func emitJSONLog(obj map[string]any) {
	if b, err := json.Marshal(obj); err == nil {
		log.Println(string(b))
	}
}

func appendEventToFile(obj map[string]any) error {
	path := os.Getenv("WAF_EVENTS_FILE")
	if path == "" {
		path = "/app/logs/coraza/waf-events.ndjson"
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))

	return err
}
