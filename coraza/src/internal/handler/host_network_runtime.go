package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"mamotama/internal/bypassconf"
	"mamotama/internal/config"
	"mamotama/internal/hostnet"
)

type hostNetworkRuntime struct {
	mu   sync.RWMutex
	raw  string
	etag string
	cfg  config.HostNetworkConfig
}

type hostNetworkConflictError struct {
	CurrentETag string
}

func (e hostNetworkConflictError) Error() string {
	return "conflict"
}

var (
	hostNetworkRuntimeMu sync.RWMutex
	hostNetworkRt        *hostNetworkRuntime
)

func InitHostNetworkRuntime(initial config.HostNetworkConfig) error {
	cfg := config.NormalizeHostNetworkConfig(initial)
	if err := config.ValidateHostNetworkConfig(cfg); err != nil {
		return err
	}
	raw, err := marshalHostNetworkConfig(cfg)
	if err != nil {
		return err
	}
	hostNetworkRuntimeMu.Lock()
	hostNetworkRt = &hostNetworkRuntime{
		raw:  string(raw),
		etag: bypassconf.ComputeETag(raw),
		cfg:  cfg,
	}
	hostNetworkRuntimeMu.Unlock()
	return nil
}

func currentHostNetworkRuntime() *hostNetworkRuntime {
	hostNetworkRuntimeMu.RLock()
	defer hostNetworkRuntimeMu.RUnlock()
	return hostNetworkRt
}

func HostNetworkSnapshot() (raw string, etag string, cfg config.HostNetworkConfig, status hostnet.Status) {
	rt := currentHostNetworkRuntime()
	if rt == nil {
		return "", "", config.NormalizeHostNetworkConfig(config.HostNetworkCfg), hostnet.StatusSnapshot(config.NormalizeHostNetworkConfig(config.HostNetworkCfg))
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	cfg = copyHostNetworkConfig(rt.cfg)
	return rt.raw, rt.etag, cfg, hostnet.StatusSnapshot(cfg)
}

func ValidateHostNetworkRaw(raw string) (config.HostNetworkConfig, error) {
	var cfg config.HostNetworkConfig
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return config.HostNetworkConfig{}, err
	}
	cfg = config.NormalizeHostNetworkConfig(cfg)
	if err := config.ValidateHostNetworkConfig(cfg); err != nil {
		return config.HostNetworkConfig{}, err
	}
	return cfg, nil
}

func ApplyHostNetworkRaw(ifMatch, raw string) (string, config.HostNetworkConfig, error) {
	rt := currentHostNetworkRuntime()
	if rt == nil {
		return "", config.HostNetworkConfig{}, errors.New("host network runtime unavailable")
	}
	cfg, err := ValidateHostNetworkRaw(raw)
	if err != nil {
		return "", config.HostNetworkConfig{}, err
	}
	encoded, err := marshalHostNetworkConfig(cfg)
	if err != nil {
		return "", config.HostNetworkConfig{}, err
	}
	nextRaw := string(encoded)
	nextETag := bypassconf.ComputeETag(encoded)

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if ifMatch != "" && ifMatch != rt.etag {
		return "", config.HostNetworkConfig{}, hostNetworkConflictError{CurrentETag: rt.etag}
	}
	if err := config.PersistHostNetworkConfig(config.ConfigFile, cfg); err != nil {
		return "", config.HostNetworkConfig{}, err
	}
	rt.cfg = cfg
	rt.raw = nextRaw
	rt.etag = nextETag
	return rt.etag, copyHostNetworkConfig(rt.cfg), nil
}

func GetHostNetwork(c *gin.Context) {
	raw, etag, cfg, status := HostNetworkSnapshot()
	c.JSON(http.StatusOK, gin.H{
		"etag":             etag,
		"raw":              raw,
		"host_network":     cfg,
		"status":           status,
		"apply_hint":       hostNetworkApplyHint(config.ConfigFile),
		"restart_required": status.ApplyRequired,
	})
}

func ValidateHostNetwork(c *gin.Context) {
	var in struct {
		Raw string `json:"raw"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cfg, err := ValidateHostNetworkRaw(in.Raw)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"ok": false, "messages": []string{err.Error()}})
		return
	}
	status := hostnet.StatusSnapshot(cfg)
	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"messages":         []string{},
		"host_network":     cfg,
		"status":           status,
		"apply_hint":       hostNetworkApplyHint(config.ConfigFile),
		"restart_required": status.ApplyRequired,
	})
}

func PutHostNetwork(c *gin.Context) {
	ifMatch := c.GetHeader("If-Match")
	var in struct {
		Raw string `json:"raw"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	etag, cfg, err := ApplyHostNetworkRaw(ifMatch, in.Raw)
	if err != nil {
		var conflictErr hostNetworkConflictError
		if errors.As(err, &conflictErr) {
			c.JSON(http.StatusConflict, gin.H{"error": "conflict", "currentETag": conflictErr.CurrentETag})
			return
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{"ok": false, "messages": []string{err.Error()}})
		return
	}
	status := hostnet.StatusSnapshot(cfg)
	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"etag":             etag,
		"host_network":     cfg,
		"status":           status,
		"apply_hint":       hostNetworkApplyHint(config.ConfigFile),
		"restart_required": status.ApplyRequired,
	})
}

func marshalHostNetworkConfig(cfg config.HostNetworkConfig) ([]byte, error) {
	return json.MarshalIndent(cfg, "", "  ")
}

func copyHostNetworkConfig(in config.HostNetworkConfig) config.HostNetworkConfig {
	out := in
	if len(in.Sysctls) > 0 {
		out.Sysctls = make(map[string]string, len(in.Sysctls))
		for key, value := range in.Sysctls {
			out.Sysctls[key] = value
		}
	}
	return out
}

func hostNetworkApplyHint(configPath string) string {
	path := strings.TrimSpace(configPath)
	if path == "" {
		path = "conf/config.json"
	}
	return "sudo mamotama-proxy -config " + path + " -apply-host-network"
}
