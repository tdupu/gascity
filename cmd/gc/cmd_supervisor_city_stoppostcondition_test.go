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

// A controller-stop wait that expires is not proof the stop failed. If the
// controller has in fact stopped, gc stop must not restore the registration --
// doing so makes the supervisor boot the city again, converting the operator's
// stop into a restart. Observed live: "timed out waiting for supervisor-hosted
// controller (PID 35344) to stop; restored registration for 'gt'" while tmux was
// already gone and 0 agent processes remained. Refs #5380.
func TestUnregisterCityFromSupervisorKeepsCityUnregisteredWhenControllerAlreadyStopped(t *testing.T) {
	gcHome := t.TempDir()
	t.Setenv("GC_HOME", gcHome)

	cityPath := filepath.Join(t.TempDir(), "bright-lights")
	if err := os.MkdirAll(cityPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[workspace]\nname = \"bright-lights\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := supervisor.NewRegistry(supervisor.RegistryPath())
	if err := reg.Register(cityPath, "bright-lights"); err != nil {
		t.Fatal(err)
	}

	withSupervisorTestHooks(
		t,
		func(_, _ io.Writer) int { return 0 },
		func(_, _ io.Writer) int { return 0 },
		func() int { return 4242 },
		func(string) (bool, string, bool) { return false, "", false },
		20*time.Millisecond,
		time.Millisecond,
	)

	// The wait expires, but the controller is actually gone.
	waitForSupervisorControllerStopHook = func(string, time.Duration) error {
		return io.EOF
	}
	oldControllerAlive := controllerAliveHook
	controllerAliveHook = func(string) int { return 0 }
	t.Cleanup(func() { controllerAliveHook = oldControllerAlive })

	var stdout, stderr bytes.Buffer
	handled, code := unregisterCityFromSupervisor(cityPath, &stdout, &stderr)
	if !handled || code != 0 {
		t.Fatalf("unregisterCityFromSupervisor = (%t, %d), want (true, 0); stderr=%q", handled, code, stderr.String())
	}
	if strings.Contains(stderr.String(), "restored registration") {
		t.Fatalf("stderr = %q, must not restore registration when the controller is stopped", stderr.String())
	}
	if !strings.Contains(stderr.String(), "controller is stopped") {
		t.Fatalf("stderr = %q, want an explanation that the controller is stopped", stderr.String())
	}

	entries, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("registry = %v, want the city to stay unregistered so the supervisor does not restart it", entries)
	}
}
