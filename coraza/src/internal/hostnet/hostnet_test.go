package hostnet

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"mamotama/internal/config"
)

func TestStatusSnapshotReportsApplyRequiredWhenNeverApplied(t *testing.T) {
	origLookup := lookupPath
	origGeteuid := geteuid
	origGOOS := currentGOOS
	defer func() {
		lookupPath = origLookup
		geteuid = origGeteuid
		currentGOOS = origGOOS
	}()

	lookupPath = func(file string) (string, error) { return "/sbin/sysctl", nil }
	geteuid = func() int { return 1000 }
	currentGOOS = "linux"

	status := StatusSnapshot(config.HostNetworkConfig{
		Enabled:       true,
		Backend:       "sysctl",
		SysctlProfile: "baseline",
		StateFile:     filepath.Join(t.TempDir(), "host-network-state.json"),
	})
	if status.State != "ready" {
		t.Fatalf("unexpected state: %q", status.State)
	}
	if !status.ApplyRequired {
		t.Fatal("expected apply_required=true")
	}
	if status.CanApplyNow {
		t.Fatal("expected can_apply_now=false")
	}
}

func TestApplyWritesStateFile(t *testing.T) {
	origLookup := lookupPath
	origCommand := commandContext
	origGeteuid := geteuid
	origGOOS := currentGOOS
	defer func() {
		lookupPath = origLookup
		commandContext = origCommand
		geteuid = origGeteuid
		currentGOOS = origGOOS
	}()

	lookupPath = func(file string) (string, error) { return "/sbin/sysctl", nil }
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "/bin/sh", "-c", "exit 0")
	}
	geteuid = func() int { return 0 }
	currentGOOS = "linux"

	stateFile := filepath.Join(t.TempDir(), "host-network-state.json")
	status, err := Apply(context.Background(), config.HostNetworkConfig{
		Enabled:       true,
		Backend:       "sysctl",
		SysctlProfile: "baseline",
		StateFile:     stateFile,
		Sysctls: map[string]string{
			"net.core.somaxconn": "8192",
		},
	})
	if err != nil {
		t.Fatalf("apply returned error: %v", err)
	}
	if status.State != "applied" {
		t.Fatalf("unexpected state after apply: %q", status.State)
	}
	if status.LastAppliedAt == "" {
		t.Fatal("expected last_applied_at to be populated")
	}
	if status.LastAppliedHash == "" {
		t.Fatal("expected last_applied_hash to be populated")
	}
}
