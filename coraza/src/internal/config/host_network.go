package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultHostNetworkStateFile = "/var/lib/mamotama-proxy/host_network_state.json"

type HostNetworkConfig struct {
	Enabled       bool              `json:"enabled"`
	Backend       string            `json:"backend"`
	SysctlProfile string            `json:"sysctl_profile"`
	Sysctls       map[string]string `json:"sysctls,omitempty"`
	StateFile     string            `json:"state_file,omitempty"`
}

func NormalizeHostNetworkConfig(cfg HostNetworkConfig) HostNetworkConfig {
	cfg.Backend = strings.ToLower(strings.TrimSpace(cfg.Backend))
	if cfg.Backend == "" {
		cfg.Backend = "sysctl"
	}
	cfg.SysctlProfile = strings.ToLower(strings.TrimSpace(cfg.SysctlProfile))
	if cfg.SysctlProfile == "" {
		cfg.SysctlProfile = "baseline"
	}
	cfg.StateFile = strings.TrimSpace(cfg.StateFile)
	if cfg.StateFile == "" {
		cfg.StateFile = defaultHostNetworkStateFile
	}
	if len(cfg.Sysctls) > 0 {
		out := make(map[string]string, len(cfg.Sysctls))
		for key, value := range cfg.Sysctls {
			trimmedKey := strings.TrimSpace(key)
			trimmedValue := strings.TrimSpace(value)
			if trimmedKey == "" || trimmedValue == "" {
				continue
			}
			out[trimmedKey] = trimmedValue
		}
		if len(out) == 0 {
			cfg.Sysctls = nil
		} else {
			cfg.Sysctls = out
		}
	}
	return cfg
}

func ValidateHostNetworkConfig(cfg HostNetworkConfig) error {
	switch cfg.Backend {
	case "sysctl":
	default:
		return fmt.Errorf("host_network.backend must be: sysctl")
	}
	switch cfg.SysctlProfile {
	case "baseline":
	default:
		return fmt.Errorf("host_network.sysctl_profile must be: baseline")
	}
	if strings.TrimSpace(cfg.StateFile) == "" {
		return fmt.Errorf("host_network.state_file is required")
	}
	for key, value := range cfg.Sysctls {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("host_network.sysctls contains an empty key")
		}
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("host_network.sysctls[%q] cannot be empty", key)
		}
	}
	return nil
}

func PersistHostNetworkConfig(path string, next HostNetworkConfig) error {
	cfg, err := loadAppConfigFile(path)
	if err != nil {
		return fmt.Errorf("load config for persist: %w", err)
	}
	cfg.HostNetwork = NormalizeHostNetworkConfig(next)
	if err := ValidateHostNetworkConfig(cfg.HostNetwork); err != nil {
		return err
	}
	if err := writeAppConfigFile(path, cfg); err != nil {
		return fmt.Errorf("write config for persist: %w", err)
	}
	return nil
}

func writeAppConfigFile(path string, cfg appConfigFile) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
