package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Without an explicit deadline (no --timeout, no daemon.start_ready_timeout),
// waitForSupervisorCity must wait through a city that is progressing but
// reports a CONSTANT status string -- the exact shape of opening_controller_state
// in #5379 -- and only succeed or fail based on the supervisor's own signals,
// never from elapsed time alone.
func TestWaitForSupervisorCityUnboundedWaitsThroughSilentPhase(t *testing.T) {
	oldRunning := supervisorCityRunningHook
	oldAlive := supervisorAliveHook
	oldPoll := supervisorCityPollInterval
	t.Cleanup(func() {
		supervisorCityRunningHook = oldRunning
		supervisorAliveHook = oldAlive
		supervisorCityPollInterval = oldPoll
	})

	const readyAfter = 25 // polls; deliberately many, to prove there is no hidden cap
	calls := 0
	supervisorCityRunningHook = func(string) (bool, string, bool) {
		calls++
		if calls >= readyAfter {
			return true, "", true
		}
		return false, "opening_controller_state", true // constant status: no progress signal
	}
	supervisorAliveHook = func() int { return 4242 }
	supervisorCityPollInterval = time.Millisecond

	var stdout bytes.Buffer
	if err := waitForSupervisorCity(t.TempDir(), true, 0, &stdout); err != nil {
		t.Fatalf("waitForSupervisorCity(timeout=0) returned %v, want success (unbounded wait)", err)
	}
	if !strings.Contains(stdout.String(), "no readiness deadline set") {
		t.Fatalf("stdout = %q, want the upfront no-deadline notice", stdout.String())
	}
}

// An explicit supervisor-reported init failure is fatal immediately, even in
// unbounded mode. "No deadline" must never mean "no way to fail."
func TestWaitForSupervisorCityUnboundedFailsOnInitFailed(t *testing.T) {
	oldRunning := supervisorCityRunningHook
	oldAlive := supervisorAliveHook
	oldErr := supervisorCityErrorHook
	oldPoll := supervisorCityPollInterval
	t.Cleanup(func() {
		supervisorCityRunningHook = oldRunning
		supervisorAliveHook = oldAlive
		supervisorCityErrorHook = oldErr
		supervisorCityPollInterval = oldPoll
	})

	supervisorCityRunningHook = func(string) (bool, string, bool) {
		return false, "init_failed", true
	}
	supervisorAliveHook = func() int { return 4242 }
	supervisorCityErrorHook = func(string) string { return "boom" }
	supervisorCityPollInterval = time.Millisecond

	start := time.Now()
	err := waitForSupervisorCity(t.TempDir(), true, 0, io.Discard)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("waitForSupervisorCity returned nil, want the init failure surfaced")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %q, want the supervisor's init failure detail", err.Error())
	}
	if elapsed > time.Second {
		t.Fatalf("failed after %s, want an immediate failure (not a wait for a deadline that doesn't exist)", elapsed)
	}
}

// If the supervisor process itself dies mid-wait, unbounded mode must still
// fail immediately rather than poll forever against a dead process.
func TestWaitForSupervisorCityUnboundedFailsWhenSupervisorDies(t *testing.T) {
	oldRunning := supervisorCityRunningHook
	oldAlive := supervisorAliveHook
	oldPoll := supervisorCityPollInterval
	t.Cleanup(func() {
		supervisorCityRunningHook = oldRunning
		supervisorAliveHook = oldAlive
		supervisorCityPollInterval = oldPoll
	})

	calls := 0
	supervisorAliveHook = func() int {
		calls++
		if calls <= 1 {
			return 4242
		}
		return 0
	}
	supervisorCityRunningHook = func(string) (bool, string, bool) { return false, "", false }
	supervisorCityPollInterval = time.Millisecond

	err := waitForSupervisorCity(t.TempDir(), true, 0, io.Discard)
	if err == nil {
		t.Fatal("waitForSupervisorCity returned nil, want supervisor-stopped error")
	}
	if !strings.Contains(err.Error(), "supervisor stopped before city became ready") {
		t.Fatalf("error = %q, want supervisor-stopped message", err.Error())
	}
}

// A long unbounded wait must emit periodic heartbeats naming the no-deadline
// state, so it reads as a deliberate wait rather than a hang.
func TestWaitForSupervisorCityUnboundedHeartbeats(t *testing.T) {
	oldRunning := supervisorCityRunningHook
	oldAlive := supervisorAliveHook
	oldPoll := supervisorCityPollInterval
	oldBeat := supervisorCityHeartbeatInterval
	t.Cleanup(func() {
		supervisorCityRunningHook = oldRunning
		supervisorAliveHook = oldAlive
		supervisorCityPollInterval = oldPoll
		supervisorCityHeartbeatInterval = oldBeat
	})

	calls := 0
	supervisorCityRunningHook = func(string) (bool, string, bool) {
		calls++
		if calls >= 10 {
			return true, "", true
		}
		return false, "opening_controller_state", true
	}
	supervisorAliveHook = func() int { return 4242 }
	supervisorCityPollInterval = 5 * time.Millisecond
	supervisorCityHeartbeatInterval = 10 * time.Millisecond

	var stdout bytes.Buffer
	if err := waitForSupervisorCity(t.TempDir(), true, 0, &stdout); err != nil {
		t.Fatalf("waitForSupervisorCity returned %v, want success", err)
	}
	got := stdout.String()
	for _, want := range []string{"still waiting", "opening_controller_state", "elapsed", "no readiness deadline set"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want heartbeat containing %q", got, want)
		}
	}
}

// resolveSupervisorCityStartWait precedence: --timeout flag, then explicit
// daemon.start_ready_timeout, then unbounded.
func TestResolveSupervisorCityStartWaitPrecedence(t *testing.T) {
	oldFlagSet := startTimeoutFlagSet
	oldFlag := startTimeoutFlag
	t.Cleanup(func() {
		startTimeoutFlagSet = oldFlagSet
		startTimeoutFlag = oldFlag
	})

	t.Run("CLI flag wins", func(t *testing.T) {
		startTimeoutFlagSet = true
		startTimeoutFlag = 42 * time.Second
		timeout, hasDeadline := resolveSupervisorCityStartWait(t.TempDir())
		if !hasDeadline || timeout != 42*time.Second {
			t.Fatalf("resolveSupervisorCityStartWait = (%v, %v), want (42s, true)", timeout, hasDeadline)
		}
	})

	t.Run("explicit daemon config wins without CLI flag", func(t *testing.T) {
		startTimeoutFlagSet = false
		cityPath := writeCityConfigForTest(t, `
[workspace]
name = "explicit-city"

[daemon]
start_ready_timeout = "9m"

[[agent]]
name = "mayor"
`)
		timeout, hasDeadline := resolveSupervisorCityStartWait(cityPath)
		if !hasDeadline || timeout != 9*time.Minute {
			t.Fatalf("resolveSupervisorCityStartWait = (%v, %v), want (9m, true)", timeout, hasDeadline)
		}
	})

	t.Run("nothing configured is unbounded", func(t *testing.T) {
		startTimeoutFlagSet = false
		cityPath := writeCityConfigForTest(t, `
[workspace]
name = "default-city"

[[agent]]
name = "mayor"
`)
		timeout, hasDeadline := resolveSupervisorCityStartWait(cityPath)
		if hasDeadline || timeout != 0 {
			t.Fatalf("resolveSupervisorCityStartWait = (%v, %v), want (0, false)", timeout, hasDeadline)
		}
	})
}

func writeCityConfigForTest(t *testing.T, toml string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "city.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
