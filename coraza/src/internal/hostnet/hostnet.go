package hostnet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"mamotama/internal/config"
)

type Status struct {
	Enabled         bool              `json:"enabled"`
	Backend         string            `json:"backend"`
	SysctlProfile   string            `json:"sysctl_profile"`
	Sysctls         map[string]string `json:"sysctls,omitempty"`
	StateFile       string            `json:"state_file"`
	State           string            `json:"state"`
	Supported       bool              `json:"supported"`
	CanApplyNow     bool              `json:"can_apply_now"`
	ApplyRequired   bool              `json:"apply_required"`
	DesiredCount    int               `json:"desired_count"`
	ConfigHash      string            `json:"config_hash,omitempty"`
	LastAppliedAt   string            `json:"last_applied_at,omitempty"`
	LastAppliedHash string            `json:"last_applied_hash,omitempty"`
	LastError       string            `json:"last_error,omitempty"`
}

type stateFile struct {
	ConfigHash   string `json:"config_hash,omitempty"`
	AppliedAt    string `json:"applied_at,omitempty"`
	Backend      string `json:"backend,omitempty"`
	Profile      string `json:"profile,omitempty"`
	DesiredCount int    `json:"desired_count,omitempty"`
}

type plan struct {
	backend       string
	sysctlProfile string
	sysctls       map[string]string
	configHash    string
}

type Plugin interface {
	Name() string
	BuildPlan(cfg config.HostNetworkConfig) (plan, error)
	Apply(ctx context.Context, p plan) error
}

var (
	lookupPath     = exec.LookPath
	commandContext = exec.CommandContext
	geteuid        = os.Geteuid
	currentGOOS    = runtime.GOOS
	readFile       = os.ReadFile
	statFile       = os.Stat
	mkdirAll       = os.MkdirAll
	writeStateFile = writeStateAtomically
	pluginRegistry = map[string]Plugin{
		"sysctl": sysctlPlugin{},
	}
)

func StatusSnapshot(cfg config.HostNetworkConfig) Status {
	status := Status{
		Enabled:       cfg.Enabled,
		Backend:       cfg.Backend,
		SysctlProfile: cfg.SysctlProfile,
		Sysctls:       cloneSysctls(cfg.Sysctls),
		StateFile:     strings.TrimSpace(cfg.StateFile),
	}
	if !cfg.Enabled {
		status.State = "disabled"
		return status
	}
	if currentGOOS != "linux" {
		status.State = "unsupported"
		status.LastError = "host network plugins currently support linux only"
		return status
	}
	plugin, ok := pluginRegistry[strings.TrimSpace(cfg.Backend)]
	if !ok {
		status.State = "unsupported"
		status.LastError = "unsupported backend"
		return status
	}
	if _, err := lookupPath("sysctl"); err != nil {
		status.State = "unsupported"
		status.LastError = "sysctl command not available"
		return status
	}
	p, err := plugin.BuildPlan(cfg)
	if err != nil {
		status.State = "misconfigured"
		status.LastError = err.Error()
		return status
	}
	status.Supported = true
	status.CanApplyNow = geteuid() == 0
	status.DesiredCount = len(p.sysctls)
	status.ConfigHash = p.configHash
	state, err := loadState(cfg.StateFile)
	if err != nil {
		status.State = "drifted"
		status.ApplyRequired = true
		status.LastError = err.Error()
		return status
	}
	status.LastAppliedAt = state.AppliedAt
	status.LastAppliedHash = state.ConfigHash
	if strings.TrimSpace(state.ConfigHash) == "" {
		status.State = "ready"
		status.ApplyRequired = true
		return status
	}
	if state.ConfigHash != p.configHash {
		status.State = "drifted"
		status.ApplyRequired = true
		return status
	}
	status.State = "applied"
	return status
}

func Apply(ctx context.Context, cfg config.HostNetworkConfig) (Status, error) {
	if !cfg.Enabled {
		return StatusSnapshot(cfg), nil
	}
	if currentGOOS != "linux" {
		return StatusSnapshot(cfg), fmt.Errorf("host network plugins currently support linux only")
	}
	plugin, ok := pluginRegistry[strings.TrimSpace(cfg.Backend)]
	if !ok {
		return StatusSnapshot(cfg), fmt.Errorf("unsupported host network backend %q", cfg.Backend)
	}
	if geteuid() != 0 {
		return StatusSnapshot(cfg), fmt.Errorf("host network apply requires root")
	}
	if _, err := lookupPath("sysctl"); err != nil {
		return StatusSnapshot(cfg), fmt.Errorf("sysctl command not available: %w", err)
	}
	p, err := plugin.BuildPlan(cfg)
	if err != nil {
		return StatusSnapshot(cfg), err
	}
	if err := plugin.Apply(ctx, p); err != nil {
		return StatusSnapshot(cfg), err
	}
	if err := writeStateFile(cfg.StateFile, stateFile{
		ConfigHash:   p.configHash,
		AppliedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		Backend:      p.backend,
		Profile:      p.sysctlProfile,
		DesiredCount: len(p.sysctls),
	}); err != nil {
		return StatusSnapshot(cfg), err
	}
	return StatusSnapshot(cfg), nil
}

type sysctlPlugin struct{}

func (sysctlPlugin) Name() string { return "sysctl" }

func (sysctlPlugin) BuildPlan(cfg config.HostNetworkConfig) (plan, error) {
	sysctls := baselineSysctls()
	for key, value := range cfg.Sysctls {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		if strings.ContainsAny(trimmedKey, " \t\r\n") {
			return plan{}, fmt.Errorf("host_network.sysctls[%q] key cannot contain whitespace", trimmedKey)
		}
		if strings.ContainsAny(trimmedValue, " \t\r\n") {
			return plan{}, fmt.Errorf("host_network.sysctls[%q] value cannot contain whitespace", trimmedKey)
		}
		sysctls[trimmedKey] = trimmedValue
	}
	p := plan{
		backend:       "sysctl",
		sysctlProfile: cfg.SysctlProfile,
		sysctls:       sysctls,
	}
	p.configHash = computeHash(p)
	return p, nil
}

func (sysctlPlugin) Apply(ctx context.Context, p plan) error {
	keys := make([]string, 0, len(p.sysctls))
	for key := range p.sysctls {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := p.sysctls[key]
		cmd := commandContext(ctx, "sysctl", "-w", fmt.Sprintf("%s=%s", key, value))
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg != "" {
				return fmt.Errorf("sysctl %s=%s failed: %s", key, value, msg)
			}
			return fmt.Errorf("sysctl %s=%s failed: %w", key, value, err)
		}
	}
	return nil
}

func baselineSysctls() map[string]string {
	return map[string]string{
		"net.ipv4.tcp_syncookies":                "1",
		"net.ipv4.tcp_max_syn_backlog":           "4096",
		"net.core.somaxconn":                     "4096",
		"net.ipv4.conf.all.accept_redirects":     "0",
		"net.ipv4.conf.default.accept_redirects": "0",
		"net.ipv4.conf.all.send_redirects":       "0",
		"net.ipv4.conf.default.send_redirects":   "0",
		"net.ipv4.conf.all.rp_filter":            "1",
		"net.ipv4.conf.default.rp_filter":        "1",
	}
}

func computeHash(p plan) string {
	raw, _ := json.Marshal(struct {
		Backend       string            `json:"backend"`
		SysctlProfile string            `json:"sysctl_profile"`
		Sysctls       map[string]string `json:"sysctls"`
	}{
		Backend:       p.backend,
		SysctlProfile: p.sysctlProfile,
		Sysctls:       p.sysctls,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func loadState(path string) (stateFile, error) {
	if strings.TrimSpace(path) == "" {
		return stateFile{}, fmt.Errorf("state file path is empty")
	}
	if _, err := statFile(path); err != nil {
		if os.IsNotExist(err) {
			return stateFile{}, nil
		}
		return stateFile{}, err
	}
	raw, err := readFile(path)
	if err != nil {
		return stateFile{}, err
	}
	var state stateFile
	if err := json.Unmarshal(raw, &state); err != nil {
		return stateFile{}, err
	}
	return state, nil
}

func writeStateAtomically(path string, state stateFile) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("state file path is empty")
	}
	dir := filepath.Dir(path)
	if err := mkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(dir, ".host-network-state-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func cloneSysctls(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
