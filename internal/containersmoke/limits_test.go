package containersmoke

import (
	"strings"
	"testing"
)

func TestMemoryLimitMBUsesEnvVarOverride(t *testing.T) {
	t.Setenv(windowsMemoryEnvVar, "8192")
	if got := windowsMemoryLimitMB(); got != 8192 {
		t.Fatalf("windowsMemoryLimitMB() = %d, want 8192", got)
	}
}

func TestMemoryLimitMBFallsBackOnInvalidEnvVar(t *testing.T) {
	for _, value := range []string{"", "not-a-number", "0", "-1"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(linuxMemoryEnvVar, value)
			if got := linuxMemoryLimitMB(); got != defaultLinuxMemoryMB {
				t.Fatalf("linuxMemoryLimitMB() with %s=%q = %d, want default %d", linuxMemoryEnvVar, value, got, defaultLinuxMemoryMB)
			}
		})
	}
}

func TestMemoryLimitArgsCapsSwapAtTheSameValue(t *testing.T) {
	args := memoryLimitArgs(4096)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--memory 4096m") || !strings.Contains(joined, "--memory-swap 4096m") {
		t.Fatalf("memoryLimitArgs(4096) = %v, want matching --memory/--memory-swap", args)
	}
}
