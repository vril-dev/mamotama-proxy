package config

import (
	"crypto/tls"
	"fmt"
	"strings"
)

const defaultServerTLSMinVersion = "tls1.2"

func normalizeAppServerTLSConfig(cfg *appServerTLSConfig) {
	cfg.CertFile = strings.TrimSpace(cfg.CertFile)
	cfg.KeyFile = strings.TrimSpace(cfg.KeyFile)
	cfg.MinVersion = normalizeServerTLSMinVersion(cfg.MinVersion)
	cfg.HTTPRedirectAddr = strings.TrimSpace(cfg.HTTPRedirectAddr)
}

func normalizeServerTLSMinVersion(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	switch s {
	case "", "tls1.2", "1.2", "tls12", "1_2":
		return defaultServerTLSMinVersion
	case "tls1.3", "1.3", "tls13", "1_3":
		return "tls1.3"
	default:
		return s
	}
}

func parseServerTLSMinVersion(v string) (uint16, error) {
	switch normalizeServerTLSMinVersion(v) {
	case defaultServerTLSMinVersion:
		return tls.VersionTLS12, nil
	case "tls1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("must be tls1.2 or tls1.3")
	}
}

func BuildServerTLSConfig(certFile string, keyFile string, minVersion string) (*tls.Config, error) {
	version, err := parseServerTLSMinVersion(minVersion)
	if err != nil {
		return nil, fmt.Errorf("min_version %w", err)
	}
	cert, err := tls.LoadX509KeyPair(strings.TrimSpace(certFile), strings.TrimSpace(keyFile))
	if err != nil {
		return nil, fmt.Errorf("load certificate/key pair: %w", err)
	}
	return &tls.Config{
		MinVersion:   version,
		Certificates: []tls.Certificate{cert},
	}, nil
}

func validateAppServerTLSConfig(server appServerConfig) error {
	tlsCfg := server.TLS
	if !tlsCfg.Enabled {
		if tlsCfg.RedirectHTTP {
			return fmt.Errorf("server.tls.redirect_http requires server.tls.enabled=true")
		}
		if _, err := parseServerTLSMinVersion(tlsCfg.MinVersion); err != nil {
			return fmt.Errorf("server.tls.min_version %w", err)
		}
		return nil
	}
	if tlsCfg.CertFile == "" || tlsCfg.KeyFile == "" {
		return fmt.Errorf("server.tls.cert_file and server.tls.key_file are required when server.tls.enabled=true")
	}
	if _, err := BuildServerTLSConfig(tlsCfg.CertFile, tlsCfg.KeyFile, tlsCfg.MinVersion); err != nil {
		return fmt.Errorf("server.tls %w", err)
	}
	if tlsCfg.RedirectHTTP {
		if strings.TrimSpace(tlsCfg.HTTPRedirectAddr) == "" {
			return fmt.Errorf("server.tls.http_redirect_addr is required when server.tls.redirect_http=true")
		}
		redirectAddr := parseListenAddr(tlsCfg.HTTPRedirectAddr)
		if redirectAddr == parseListenAddr(server.ListenAddr) {
			return fmt.Errorf("server.tls.http_redirect_addr must be different from server.listen_addr")
		}
	}
	return nil
}
