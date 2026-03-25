package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type proxyRulesPutBody struct {
	Raw string `json:"raw"`
}

type proxyRulesProbeBody struct {
	Raw       string `json:"raw"`
	TimeoutMS int    `json:"timeout_ms"`
}

func ProxyRulesAction(c *gin.Context) {
	action := strings.TrimPrefix(strings.TrimSpace(c.Param("action")), ":")
	switch action {
	case "validate":
		ValidateProxyRules(c)
	case "probe":
		ProbeProxyRules(c)
	case "rollback":
		RollbackProxyRulesHandler(c)
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown proxy-rules action"})
	}
}

func GetProxyRules(c *gin.Context) {
	raw, etag, cfg, health, rollbackDepth := ProxyRulesSnapshot()
	c.JSON(http.StatusOK, gin.H{
		"etag":           etag,
		"raw":            raw,
		"proxy":          cfg,
		"health":         health,
		"rollback_depth": rollbackDepth,
	})
}

func ValidateProxyRules(c *gin.Context) {
	var in proxyRulesPutBody
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cfg, err := ValidateProxyRulesRaw(in.Raw)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"ok":       false,
			"messages": []string{err.Error()},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"messages": []string{},
		"proxy":    cfg,
	})
}

func ProbeProxyRules(c *gin.Context) {
	var in proxyRulesProbeBody
	if err := c.ShouldBindJSON(&in); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if in.TimeoutMS < 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":       false,
			"messages": []string{"timeout_ms must be >= 0"},
		})
		return
	}
	timeout := 2 * time.Second
	if in.TimeoutMS > 0 {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}

	cfg, address, latencyMS, err := ProxyProbe(in.Raw, timeout)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "upstream probe failed",
			"proxy": cfg,
			"probe": gin.H{
				"address":    address,
				"timeout_ms": timeout.Milliseconds(),
			},
			"messages": []string{err.Error()},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"proxy": cfg,
		"probe": gin.H{
			"address":    address,
			"latency_ms": latencyMS,
			"timeout_ms": timeout.Milliseconds(),
		},
	})
}

func PutProxyRules(c *gin.Context) {
	var in proxyRulesPutBody
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ifMatch := strings.TrimSpace(c.GetHeader("If-Match"))
	if ifMatch == "" {
		c.JSON(http.StatusPreconditionRequired, gin.H{"error": "If-Match header is required"})
		return
	}

	etag, cfg, err := ApplyProxyRulesRaw(ifMatch, in.Raw)
	if err != nil {
		var conflict proxyRulesConflictError
		if asProxyRulesConflict(err, &conflict) {
			c.JSON(http.StatusConflict, gin.H{"error": "conflict", "currentETag": conflict.CurrentETag})
			return
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{"ok": false, "messages": []string{err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"etag":  etag,
		"proxy": cfg,
	})
}

func RollbackProxyRulesHandler(c *gin.Context) {
	etag, cfg, restored, err := RollbackProxyRules()
	if err != nil {
		if strings.Contains(err.Error(), "no rollback snapshot") {
			c.JSON(http.StatusConflict, gin.H{"error": "no rollback snapshot"})
			return
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{"ok": false, "messages": []string{err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":            true,
		"etag":          etag,
		"proxy":         cfg,
		"rollback":      true,
		"restored_from": restored,
	})
}
