package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestConcurrencyGuardAcquireRelease(t *testing.T) {
	g := NewConcurrencyGuard(1, "proxy")
	if g == nil {
		t.Fatal("expected guard")
	}
	if !g.Acquire() {
		t.Fatal("expected first acquire to succeed")
	}
	if g.Acquire() {
		t.Fatal("expected second acquire to fail while full")
	}
	g.Release()
	if !g.Acquire() {
		t.Fatal("expected acquire to succeed after release")
	}
}

func TestConcurrencyLimitRejectsWhenBusy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	guard := NewConcurrencyGuard(1, "global")
	if !guard.Acquire() {
		t.Fatal("expected pre-acquire success")
	}
	defer guard.Release()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		if !guard.Acquire() {
			guard.Reject(c)
			return
		}
		defer guard.Release()
		c.Next()
	})
	r.GET("/ok", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want=%d", w.Code, http.StatusServiceUnavailable)
	}
	if got := w.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q want=1", got)
	}
}
