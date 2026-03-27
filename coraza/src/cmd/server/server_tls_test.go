package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/acme/autocert"
)

func TestACMEHTTPRedirectServerPreservesChallengePath(t *testing.T) {
	t.Parallel()

	manager := &autocert.Manager{Prompt: autocert.AcceptTOS, Cache: autocert.DirCache(t.TempDir())}
	srv := newACMEHTTPRedirectServer(":8080", ":9443", manager)

	challengeReq := httptest.NewRequest(http.MethodGet, "http://proxy.example.com/.well-known/acme-challenge/token", nil)
	challengeRes := httptest.NewRecorder()
	srv.Handler.ServeHTTP(challengeRes, challengeReq)
	if challengeRes.Code == http.StatusPermanentRedirect {
		t.Fatal("acme challenge path should not redirect")
	}

	appReq := httptest.NewRequest(http.MethodGet, "http://proxy.example.com/app", nil)
	appRes := httptest.NewRecorder()
	srv.Handler.ServeHTTP(appRes, appReq)
	if appRes.Code != http.StatusPermanentRedirect {
		t.Fatalf("unexpected app redirect status: %d", appRes.Code)
	}
	if location := appRes.Header().Get("Location"); location != "https://proxy.example.com:9443/app" {
		t.Fatalf("unexpected redirect location: %q", location)
	}
}
