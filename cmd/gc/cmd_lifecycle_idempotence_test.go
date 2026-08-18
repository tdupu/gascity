package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/supervisor"
)

// gc stop on a city that was never registered and has no standalone
// controller running must say so and return success immediately, instead of
// running the full interrupt/kill/orphan-cleanup sequence against nothing.
func TestCmdStopReportsAlreadyStoppedForUnregisteredCity(t *testing.T) {
	gcHome := t.TempDir()
	t.Setenv("GC_HOME", gcHome)

	cityPath := filepath.Join(t.TempDir(), "quiet-town")
	if err := os.MkdirAll(cityPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[workspace]\nname = \"quiet-town\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	outcome := cmdStopJSONSequence([]string{cityPath}, &stdout, &stderr, false, false, true)
	if outcome.code != 0 {
		t.Fatalf("cmdStopJSONSequence code = %d, want 0; stderr=%q", outcome.code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "quiet-town") || !strings.Contains(stdout.String(), "already stopped") {
		t.Fatalf("stdout = %q, want an already-stopped message naming the city", stdout.String())
	}
}

// A REGISTERED city must never be short-circuited as "already stopped", even
// if the supervisor reports it not running -- only unregistered + no
// standalone controller counts. This is the conservative half of the fix
// (#5380 is exactly the failure mode of a stray/incorrect registration
// determining runtime state); cityFullyStopped must fall through to the
// real stop sequence whenever a registration is present.
func TestCityFullyStoppedIsFalseWhenRegistered(t *testing.T) {
	gcHome := t.TempDir()
	t.Setenv("GC_HOME", gcHome)

	cityPath := filepath.Join(t.TempDir(), "busy-town")
	if err := os.MkdirAll(cityPath, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := supervisor.NewRegistry(supervisor.RegistryPath())
	if err := reg.Register(cityPath, "busy-town"); err != nil {
		t.Fatal(err)
	}

	stopped, name := cityFullyStopped(cityPath)
	if stopped {
		t.Fatalf("cityFullyStopped = (true, %q), want false for a registered city", name)
	}
}

// gc start on a city the supervisor already reports running must say so and
// return success immediately, without re-registering, re-reloading the
// supervisor, or re-entering the readiness wait.
func TestShortCircuitAlreadyStartingOrRunningReportsAlreadyRunning(t *testing.T) {
	oldRunning := supervisorCityRunningHook
	t.Cleanup(func() { supervisorCityRunningHook = oldRunning })
	supervisorCityRunningHook = func(string) (bool, string, bool) { return true, "", true }

	var stdout, stderr bytes.Buffer
	handled, code := shortCircuitAlreadyStartingOrRunning(t.TempDir(), "sunny-side", &stdout, &stderr)
	if !handled || code != 0 {
		t.Fatalf("shortCircuitAlreadyStartingOrRunning = (%t, %d), want (true, 0)", handled, code)
	}
	if !strings.Contains(stdout.String(), "sunny-side") || !strings.Contains(stdout.String(), "already running") {
		t.Fatalf("stdout = %q, want an already-running message naming the city", stdout.String())
	}
}

// gc start on a city that is mid-boot (a real, non-terminal status) must
// report that a start is already in progress and attach to the existing
// wait, rather than re-triggering ensureSupervisorRunning/reload -- which
// would collide with the reconcile-queue-busy path (#5343) on a live city.
func TestShortCircuitAlreadyStartingOrRunningAttachesToInProgressStart(t *testing.T) {
	oldRunning := supervisorCityRunningHook
	oldWait := waitForSupervisorCityHook
	oldPoll := supervisorCityPollInterval
	t.Cleanup(func() {
		supervisorCityRunningHook = oldRunning
		waitForSupervisorCityHook = oldWait
		supervisorCityPollInterval = oldPoll
	})

	calls := 0
	supervisorCityRunningHook = func(string) (bool, string, bool) {
		calls++
		if calls == 1 {
			return false, "running_pool_on_boot:3/10:alpha", true
		}
		return true, "", true
	}
	waitCalls := 0
	waitForSupervisorCityHook = func(cityPath string, wantRunning bool, timeout time.Duration, stdout io.Writer) error {
		waitCalls++
		return nil
	}

	var stdout, stderr bytes.Buffer
	handled, code := shortCircuitAlreadyStartingOrRunning(t.TempDir(), "", &stdout, &stderr)
	if !handled || code != 0 {
		t.Fatalf("shortCircuitAlreadyStartingOrRunning = (%t, %d), want (true, 0); stderr=%q", handled, code, stderr.String())
	}
	if waitCalls != 1 {
		t.Fatalf("waitForSupervisorCityHook called %d times, want exactly 1 (attach, don't re-register/reload)", waitCalls)
	}
	if !strings.Contains(stdout.String(), "already in progress") {
		t.Fatalf("stdout = %q, want an already-in-progress message", stdout.String())
	}
}

// A fresh, not-yet-known-to-be-starting registration (status=="") must not be
// short-circuited -- the normal register/reload/wait sequence owns that case.
func TestShortCircuitAlreadyStartingOrRunningIgnoresFreshRegistration(t *testing.T) {
	oldRunning := supervisorCityRunningHook
	t.Cleanup(func() { supervisorCityRunningHook = oldRunning })
	supervisorCityRunningHook = func(string) (bool, string, bool) { return false, "", true }

	var stdout, stderr bytes.Buffer
	handled, _ := shortCircuitAlreadyStartingOrRunning(t.TempDir(), "", &stdout, &stderr)
	if handled {
		t.Fatalf("shortCircuitAlreadyStartingOrRunning handled a fresh (status=\"\") registration; want it to fall through, stdout=%q", stdout.String())
	}
}
