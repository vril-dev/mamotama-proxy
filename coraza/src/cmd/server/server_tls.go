package main

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func newHTTPRedirectServer(addr string, tlsListenAddr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := &url.URL{
				Scheme:   "https",
				Host:     redirectTargetHost(r.Host, tlsListenAddr),
				Path:     r.URL.Path,
				RawPath:  r.URL.RawPath,
				RawQuery: r.URL.RawQuery,
			}
			http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
		}),
	}
}

func redirectTargetHost(requestHost string, tlsListenAddr string) string {
	tlsPort := tlsListenPort(tlsListenAddr)
	host := strings.TrimSpace(requestHost)
	if host == "" {
		if tlsPort == "443" {
			return "127.0.0.1"
		}
		return net.JoinHostPort("127.0.0.1", tlsPort)
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		if tlsPort == "443" {
			return strings.Trim(parsedHost, "[]")
		}
		return net.JoinHostPort(strings.Trim(parsedHost, "[]"), tlsPort)
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		if tlsPort == "443" {
			return host
		}
		return net.JoinHostPort(strings.Trim(host, "[]"), tlsPort)
	}
	if tlsPort == "443" {
		return host
	}
	return net.JoinHostPort(host, tlsPort)
}

func tlsListenPort(listenAddr string) string {
	s := strings.TrimSpace(listenAddr)
	if s == "" {
		return "443"
	}
	if strings.HasPrefix(s, ":") {
		return strings.TrimPrefix(s, ":")
	}
	if _, port, err := net.SplitHostPort(s); err == nil && strings.TrimSpace(port) != "" {
		return port
	}
	return "443"
}
