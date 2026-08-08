package containersmoke

import (
	"fmt"
	"os"
	"strconv"
)

// Docker containers get no memory limit by default — a single Wine
// invocation can then contend for the entire host's RAM alongside
// everything else running there. These are real, tested minimums, not
// guesses: 2GB was not reliably enough for Wine in real testing here
// (intermittent failures under memory pressure); 4GB was. The plain
// Linux runner needs no compatibility-layer overhead, so a smaller
// default is enough. Override via environment variable if a specific
// host needs more headroom or can run with less — see docs/adr/0028.
const (
	defaultWindowsMemoryMB = 4096
	defaultLinuxMemoryMB   = 2048

	windowsMemoryEnvVar = "FLUXA_BUILDER_WINDOWS_CONTAINER_MEMORY_MB"
	linuxMemoryEnvVar   = "FLUXA_BUILDER_LINUX_CONTAINER_MEMORY_MB"
)

func windowsMemoryLimitMB() int {
	return memoryLimitMB(windowsMemoryEnvVar, defaultWindowsMemoryMB)
}

func linuxMemoryLimitMB() int {
	return memoryLimitMB(linuxMemoryEnvVar, defaultLinuxMemoryMB)
}

func memoryLimitMB(envVar string, fallback int) int {
	raw := os.Getenv(envVar)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// memoryLimitArgs caps both RAM and swap at the same value (no
// additional swap beyond the hard memory limit) — a predictable, bounded
// footprint rather than letting the container degrade into thrashing the
// host's swap instead of failing cleanly.
func memoryLimitArgs(memoryMB int) []string {
	value := fmt.Sprintf("%dm", memoryMB)
	return []string{"--memory", value, "--memory-swap", value}
}
