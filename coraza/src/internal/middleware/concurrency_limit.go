package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type ConcurrencyGuard struct {
	name string
	sem  chan struct{}
}

func NewConcurrencyGuard(max int, name string) *ConcurrencyGuard {
	if max <= 0 {
		return nil
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		trimmed = "global"
	}
	return &ConcurrencyGuard{
		name: trimmed,
		sem:  make(chan struct{}, max),
	}
}

func (g *ConcurrencyGuard) Name() string {
	if g == nil {
		return "global"
	}
	return g.name
}

func (g *ConcurrencyGuard) Acquire() bool {
	if g == nil {
		return true
	}
	select {
	case g.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *ConcurrencyGuard) Release() {
	if g == nil {
		return
	}
	select {
	case <-g.sem:
	default:
	}
}

func (g *ConcurrencyGuard) Reject(c *gin.Context) {
	scope := "global"
	if g != nil {
		scope = g.name
	}
	c.Header("Retry-After", "1")
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
		"error": "server busy",
		"scope": scope,
	})
}

func ConcurrencyLimit(max int, name string) gin.HandlerFunc {
	guard := NewConcurrencyGuard(max, name)
	if guard == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if !guard.Acquire() {
			guard.Reject(c)
			return
		}
		defer guard.Release()
		c.Next()
	}
}
