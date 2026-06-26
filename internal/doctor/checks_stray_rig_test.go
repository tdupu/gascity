package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

// makeRigDir creates <city>/<name>/.repo.git/ so the check sees a rig
// repository on disk.
func makeRigDir(t *testing.T, city, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(city, name, ".repo.git"), 0o755); err != nil {
		t.Fatalf("makeRigDir(%s): %v", name, err)
	}
}

func TestStrayRigOnDiskCheck_NoStrays(t *testing.T) {
	city := t.TempDir()
	makeRigDir(t, city, "agent_skills")
	makeRigDir(t, city, "lmfdb")

	cfg := &config.City{Rigs: []config.Rig{
		{Name: "agent_skills"},
		{Name: "lmfdb"},
	}}
	r := NewStrayRigOnDiskCheck(cfg, city).Run(&CheckContext{})
	if r.Status != StatusOK {
		t.Fatalf("status = %d, want OK; msg = %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "no unregistered") {
		t.Errorf("message = %q, want no-unregistered text", r.Message)
	}
}

func TestStrayRigOnDiskCheck_DetectsStrays(t *testing.T) {
	city := t.TempDir()
	makeRigDir(t, city, "agent_skills")     // registered
	makeRigDir(t, city, "diff_alg_examples") // stray
	makeRigDir(t, city, "diff_alg_problems") // stray
	makeRigDir(t, city, "diff_alg_public")   // stray
	makeRigDir(t, city, "dupuy_cv")          // stray

	cfg := &config.City{Rigs: []config.Rig{
		{Name: "agent_skills"},
	}}
	r := NewStrayRigOnDiskCheck(cfg, city).Run(&CheckContext{})
	if r.Status != StatusWarning {
		t.Fatalf("status = %d, want Warning; msg = %s", r.Status, r.Message)
	}
	if r.Severity != SeverityAdvisory {
		t.Errorf("severity = %d, want SeverityAdvisory", r.Severity)
	}
	if !strings.Contains(r.Message, "4 rig directories") {
		t.Errorf("message = %q, want '4 rig directories' count", r.Message)
	}
	// Details and hint must list every stray, sorted.
	wantStrays := []string{"diff_alg_examples", "diff_alg_problems", "diff_alg_public", "dupuy_cv"}
	for _, name := range wantStrays {
		var seenInDetails bool
		for _, d := range r.Details {
			if strings.Contains(d, name) {
				seenInDetails = true
				break
			}
		}
		if !seenInDetails {
			t.Errorf("details missing stray %q; details = %v", name, r.Details)
		}
		if !strings.Contains(r.FixHint, name) {
			t.Errorf("fix hint missing %q; hint = %s", name, r.FixHint)
		}
	}
	if !strings.Contains(r.FixHint, "gc rig add") {
		t.Errorf("fix hint missing gc rig add; hint = %s", r.FixHint)
	}
}

func TestStrayRigOnDiskCheck_IgnoresNonRigDirs(t *testing.T) {
	city := t.TempDir()
	makeRigDir(t, city, "agent_skills")
	// Plain config / agent directories without .repo.git/ must not be
	// flagged.
	for _, name := range []string{"agents", "docs", "mayor", "witness"} {
		if err := os.MkdirAll(filepath.Join(city, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}

	cfg := &config.City{Rigs: []config.Rig{{Name: "agent_skills"}}}
	r := NewStrayRigOnDiskCheck(cfg, city).Run(&CheckContext{})
	if r.Status != StatusOK {
		t.Fatalf("status = %d, want OK; msg = %s, details = %v", r.Status, r.Message, r.Details)
	}
}

func TestStrayRigOnDiskCheck_IgnoresHiddenDirs(t *testing.T) {
	city := t.TempDir()
	makeRigDir(t, city, "agent_skills")
	// A .gc/ runtime dir contains many things but is never a stray rig
	// candidate. Even if it happened to contain a .repo.git/ child by
	// accident, we never recurse into a dotfile-prefixed directory.
	if err := os.MkdirAll(filepath.Join(city, ".gc", ".repo.git"), 0o755); err != nil {
		t.Fatalf("mkdir .gc: %v", err)
	}

	cfg := &config.City{Rigs: []config.Rig{{Name: "agent_skills"}}}
	r := NewStrayRigOnDiskCheck(cfg, city).Run(&CheckContext{})
	if r.Status != StatusOK {
		t.Fatalf("status = %d, want OK; msg = %s, details = %v", r.Status, r.Message, r.Details)
	}
}

func TestStrayRigOnDiskCheck_RigWithCustomPath(t *testing.T) {
	city := t.TempDir()
	// A registered rig points at an in-city directory whose name differs
	// from the rig name. The on-disk basename must not be flagged.
	makeRigDir(t, city, "external_repo")

	cfg := &config.City{Rigs: []config.Rig{
		{Name: "logical_name", Path: filepath.Join(city, "external_repo")},
	}}
	r := NewStrayRigOnDiskCheck(cfg, city).Run(&CheckContext{})
	if r.Status != StatusOK {
		t.Fatalf("status = %d, want OK; msg = %s, details = %v", r.Status, r.Message, r.Details)
	}
}

func TestStrayRigOnDiskCheck_OutOfCityRigDoesNotShadow(t *testing.T) {
	city := t.TempDir()
	external := t.TempDir()
	// A rig registered to an external path must not exempt a same-named
	// on-disk directory inside the city — those are distinct repositories.
	makeRigDir(t, city, "ghost_rig")

	cfg := &config.City{Rigs: []config.Rig{
		{Name: "ghost_rig_external", Path: filepath.Join(external, "ghost_rig")},
	}}
	r := NewStrayRigOnDiskCheck(cfg, city).Run(&CheckContext{})
	if r.Status != StatusWarning {
		t.Fatalf("status = %d, want Warning; msg = %s", r.Status, r.Message)
	}
	if !strings.Contains(r.FixHint, "ghost_rig") {
		t.Errorf("fix hint missing ghost_rig; hint = %s", r.FixHint)
	}
}

func TestStrayRigOnDiskCheck_SingularMessage(t *testing.T) {
	city := t.TempDir()
	makeRigDir(t, city, "only_stray")

	cfg := &config.City{}
	r := NewStrayRigOnDiskCheck(cfg, city).Run(&CheckContext{})
	if r.Status != StatusWarning {
		t.Fatalf("status = %d, want Warning; msg = %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "1 rig directory") {
		t.Errorf("message = %q, want '1 rig directory' singular form", r.Message)
	}
}

func TestStrayRigOnDiskCheck_NoFix(t *testing.T) {
	c := NewStrayRigOnDiskCheck(&config.City{}, t.TempDir())
	if c.CanFix() {
		t.Error("CanFix() = true; want false (operator decides per stray)")
	}
	if err := c.Fix(&CheckContext{}); err != nil {
		t.Errorf("Fix() = %v; want nil", err)
	}
}

func TestStrayRigOnDiskCheck_NotWarmupEligible(t *testing.T) {
	c := NewStrayRigOnDiskCheck(&config.City{}, t.TempDir())
	if c.WarmupEligible() {
		t.Error("WarmupEligible() = true; want false")
	}
}
