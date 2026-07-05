package main

import (
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func chooseManagedDoltPort(cityPath, stateFile string) (string, error) {
	cityPath = normalizePathForCompare(cityPath)
	envPort := strings.TrimSpace(os.Getenv("GC_DOLT_PORT"))

	layout, err := resolveManagedDoltRuntimeLayout(cityPath)
	if err != nil {
		return "", err
	}
	canonicalStateFile := layout.StateFile
	if strings.TrimSpace(stateFile) == "" {
		stateFile = layout.StateFile
	} else {
		layout.StateFile = stateFile
	}

	writeAndPublish := func(path string, repaired doltRuntimeState) error {
		if err := writeDoltRuntimeStateFile(path, repaired); err != nil {
			return err
		}
		if !samePath(path, canonicalStateFile) {
			return nil
		}
		return publishManagedDoltRuntimeStateIfOwned(cityPath)
	}
	// repairForProvider tries both port-holder and PID-based repair. The
	// PID-based path is intentionally restricted to chooseManagedDoltPort
	// (the provider context) so that publishManagedDoltRuntimeState never
	// triggers an expensive ps(1) scan or process-inspection hang.
	repairForProvider := func(s doltRuntimeState) (doltRuntimeState, bool) {
		if repaired, ok := repairedManagedDoltRuntimeState(cityPath, layout, s); ok {
			return repaired, true
		}
		return repairedManagedDoltRuntimeStateByPID(layout, s)
	}

	if state, err := readDoltRuntimeStateFile(stateFile); err == nil {
		if validDoltRuntimeState(state, cityPath) {
			return strconv.Itoa(state.Port), nil
		}
		if repaired, ok := repairForProvider(state); ok {
			if repaired != state {
				if err := writeAndPublish(stateFile, repaired); err != nil {
					return "", fmt.Errorf("repair provider runtime state: %w", err)
				}
			}
			return strconv.Itoa(repaired.Port), nil
		}
		if hint, found, hintErr := readPublishedDoltRuntimeStateHint(cityPath); hintErr == nil && found {
			if repaired, ok := repairForProvider(hint); ok {
				if err := writeAndPublish(stateFile, repaired); err != nil {
					return "", fmt.Errorf("repair provider runtime state from published hint: %w", err)
				}
				return strconv.Itoa(repaired.Port), nil
			}
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read provider runtime state: %w", err)
	} else if hint, found, hintErr := readPublishedDoltRuntimeStateHint(cityPath); hintErr == nil && found {
		if repaired, ok := repairForProvider(hint); ok {
			if err := writeAndPublish(stateFile, repaired); err != nil {
				return "", fmt.Errorf("repair missing provider runtime state: %w", err)
			}
			return strconv.Itoa(repaired.Port), nil
		}
	}
	if envPort != "" {
		return envPort, nil
	}
	seed := deterministicManagedDoltPortSeed(cityPath)
	return strconv.Itoa(nextAvailableManagedDoltPort(seed)), nil
}

func repairedManagedDoltRuntimeState(_ string, layout managedDoltRuntimeLayout, state doltRuntimeState) (doltRuntimeState, bool) {
	if state.Port <= 0 {
		return doltRuntimeState{}, false
	}
	if state.DataDir != "" && !samePath(state.DataDir, layout.DataDir) {
		return doltRuntimeState{}, false
	}
	port := strconv.Itoa(state.Port)
	holderPID := findPortHolderPID(port)
	if holderPID <= 0 {
		// No listener on the stored port — let the caller decide whether to
		// attempt the more expensive PID-based reverse lookup. Returning false
		// here prevents publishManagedDoltRuntimeState from triggering a ps(1)
		// scan; only chooseManagedDoltPort (via repairForProvider) does that.
		return doltRuntimeState{}, false
	}
	stateDir := strings.TrimSpace(state.DataDir)
	if stateDir == "" {
		stateDir = layout.DataDir
	}
	if !managedDoltProcessOwnedWithStateDir(holderPID, layout, stateDir) {
		return doltRuntimeState{}, false
	}
	if processHasDeletedDataInodes(holderPID, layout.DataDir) {
		return doltRuntimeState{}, false
	}
	managedPID, _ := findManagedDoltPID(layout, port)
	if managedPID <= 0 || managedPID != holderPID {
		return doltRuntimeState{}, false
	}
	if !managedDoltTCPReachable("127.0.0.1", port) {
		return doltRuntimeState{}, false
	}
	repaired := state
	repaired.Running = true
	repaired.PID = holderPID
	repaired.DataDir = layout.DataDir
	if strings.TrimSpace(repaired.StartedAt) == "" {
		repaired.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return repaired, true
}

// managedPIDFromNonPortSources finds the managed Dolt PID using sources that
// do not depend on the stored port being correct: PID file, config file, and
// data directory (in that preference order).
func managedPIDFromNonPortSources(layout managedDoltRuntimeLayout) int {
	if pid := managedPIDFromPIDFile(layout.PIDFile); pid > 0 {
		return pid
	}
	if pid := managedPIDFromPSByConfig(layout.ConfigFile); pid > 0 {
		return pid
	}
	if pid := managedPIDFromPSByDataDir(layout.DataDir); pid > 0 {
		return pid
	}
	return 0
}

// repairedManagedDoltRuntimeStateByPID recovers when the port stored in state
// has no listener. It finds the managed Dolt PID via non-port sources (PID
// file, config file, data directory), then discovers the actual listening port
// via reverse lookup (PID → ports), and validates ownership and reachability.
func repairedManagedDoltRuntimeStateByPID(layout managedDoltRuntimeLayout, state doltRuntimeState) (doltRuntimeState, bool) {
	// Prefer the PID already recorded in state when it is still alive —
	// this avoids the expensive ps(1) scan in managedPIDFromNonPortSources.
	pid := state.PID
	if pid <= 0 || !pidAlive(pid) {
		pid = managedPIDFromNonPortSources(layout)
	}
	if pid <= 0 {
		return doltRuntimeState{}, false
	}
	stateDir := strings.TrimSpace(state.DataDir)
	if stateDir == "" {
		stateDir = layout.DataDir
	}
	if !managedDoltProcessOwnedWithStateDir(pid, layout, stateDir) {
		return doltRuntimeState{}, false
	}
	if processHasDeletedDataInodes(pid, layout.DataDir) {
		return doltRuntimeState{}, false
	}
	for _, actualPort := range findListeningPortsForPID(pid) {
		if !managedDoltTCPReachable("127.0.0.1", actualPort) {
			continue
		}
		holderPID := findPortHolderPID(actualPort)
		if holderPID > 0 && holderPID != pid {
			continue
		}
		portNum, err := strconv.Atoi(actualPort)
		if err != nil {
			continue
		}
		repaired := state
		repaired.Running = true
		repaired.PID = pid
		repaired.Port = portNum
		repaired.DataDir = layout.DataDir
		if strings.TrimSpace(repaired.StartedAt) == "" {
			repaired.StartedAt = time.Now().UTC().Format(time.RFC3339)
		}
		return repaired, true
	}
	return doltRuntimeState{}, false
}

func deterministicManagedDoltPortSeed(cityPath string) int {
	cityPath = normalizePathForCompare(cityPath)
	if seed, err := cksumManagedDoltPortSeed(cityPath); err == nil {
		return seed
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(cityPath))
	return int(hasher.Sum32()%50000) + 10000
}

func cksumManagedDoltPortSeed(cityPath string) (int, error) {
	cmd := exec.Command("cksum")
	cmd.Stdin = strings.NewReader(cityPath)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty cksum output")
	}
	value, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fmt.Errorf("parse cksum output %q: %w", fields[0], err)
	}
	return value%50000 + 10000, nil
}

func nextAvailableManagedDoltPort(seed int) int {
	port := seed
	for attempts := 0; attempts < 100; attempts++ {
		if port > 60000 {
			port = 10000
		}
		if managedDoltPortAvailable(port) {
			return port
		}
		port++
	}
	return seed
}

// nextAvailableManagedDoltPortForHost is the host-aware variant used by
// startManagedDoltProcessWithOptions after a host-aware wait on the original
// port has failed. Using the same host as the eventual bind avoids picking a
// port that probes free on 127.0.0.1 but is actually busy on the bind host
// (e.g. another process holds 192.168.1.5:X while leaving 127.0.0.1:X free,
// and dolt is binding 0.0.0.0:X, which would fail). Blank host normalizes
// to the loopback bind default inside managedDoltPortAvailableFn (the
// indirection over managedDoltPortAvailableForHost) to match the bind
// default in startManagedDoltProcessWithOptions.
func nextAvailableManagedDoltPortForHost(host string, seed int) int {
	port := seed
	for attempts := 0; attempts < 100; attempts++ {
		if port > 60000 {
			port = 10000
		}
		if managedDoltPortAvailableFn(host, port) {
			return port
		}
		port++
	}
	return seed
}

func managedDoltPortAvailable(port int) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	defer listener.Close() //nolint:errcheck // best-effort cleanup
	return true
}
