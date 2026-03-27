package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"mamotama/internal/config"
	"mamotama/internal/handler"
)

const letsEncryptStagingDirectoryURL = "https://acme-staging-v02.api.letsencrypt.org/directory"

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

func newACMEHTTPRedirectServer(addr string, tlsListenAddr string, manager *autocert.Manager) *http.Server {
	redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := &url.URL{
			Scheme:   "https",
			Host:     redirectTargetHost(r.Host, tlsListenAddr),
			Path:     r.URL.Path,
			RawPath:  r.URL.RawPath,
			RawQuery: r.URL.RawQuery,
		}
		http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
	})
	return &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		Handler:           manager.HTTPHandler(redirect),
	}
}

func buildManagedServerTLSConfig() (*tls.Config, *http.Server, error) {
	handler.ResetServerTLSRuntimeStatus()
	if !config.ServerTLSEnabled {
		return nil, nil, nil
	}
	if config.ServerTLSACMEEnabled {
		return buildACMEServerTLSConfig()
	}
	return buildManualServerTLSConfig()
}

func buildManualServerTLSConfig() (*tls.Config, *http.Server, error) {
	tlsConfig, err := config.BuildServerTLSConfig(config.ServerTLSCertFile, config.ServerTLSKeyFile, config.ServerTLSMinVersion)
	if err != nil {
		handler.RecordServerTLSError(err)
		return nil, nil, err
	}
	notAfter, parseErr := certificateNotAfter(tlsConfig.Certificates[0])
	if parseErr != nil {
		handler.RecordServerTLSError(parseErr)
	} else {
		handler.RecordServerTLSConfigured("manual", notAfter)
	}
	var redirectSrv *http.Server
	if config.ServerTLSRedirectHTTP {
		redirectSrv = newHTTPRedirectServer(config.ServerTLSHTTPRedirectAddr, config.ListenAddr)
	}
	return tlsConfig, redirectSrv, nil
}

func buildACMEServerTLSConfig() (*tls.Config, *http.Server, error) {
	minVersion, err := parseServerTLSMinVersion(config.ServerTLSMinVersion)
	if err != nil {
		handler.RecordServerTLSError(err)
		return nil, nil, err
	}
	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Email:      strings.TrimSpace(config.ServerTLSACMEEmail),
		HostPolicy: autocert.HostWhitelist(config.ServerTLSACMEDomains...),
		Cache:      autocert.DirCache(config.ServerTLSACMECacheDir),
	}
	if config.ServerTLSACMEStaging {
		manager.Client = &acme.Client{DirectoryURL: letsEncryptStagingDirectoryURL}
	}
	tlsConfig := manager.TLSConfig()
	tlsConfig.MinVersion = minVersion
	baseGetCertificate := tlsConfig.GetCertificate
	tlsConfig.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert, getErr := baseGetCertificate(hello)
		if getErr != nil {
			handler.RecordServerTLSACMEFailure(getErr)
			return nil, getErr
		}
		notAfter, parseErr := certificateNotAfter(*cert)
		if parseErr != nil {
			handler.RecordServerTLSACMEFailure(parseErr)
		} else {
			handler.RecordServerTLSACMESuccess(notAfter)
		}
		return cert, nil
	}
	handler.RecordServerTLSConfigured("acme", time.Time{})
	var redirectSrv *http.Server
	if config.ServerTLSRedirectHTTP {
		redirectSrv = newACMEHTTPRedirectServer(config.ServerTLSHTTPRedirectAddr, config.ListenAddr, manager)
	}
	return tlsConfig, redirectSrv, nil
}

func parseServerTLSMinVersion(v string) (uint16, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "tls1.2", "1.2", "tls12", "1_2":
		return tls.VersionTLS12, nil
	case "tls1.3", "1.3", "tls13", "1_3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("server tls min_version must be tls1.2 or tls1.3")
	}
}

func certificateNotAfter(cert tls.Certificate) (time.Time, error) {
	if cert.Leaf != nil {
		return cert.Leaf.NotAfter, nil
	}
	if len(cert.Certificate) == 0 {
		return time.Time{}, fmt.Errorf("certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return time.Time{}, err
	}
	return leaf.NotAfter, nil
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
