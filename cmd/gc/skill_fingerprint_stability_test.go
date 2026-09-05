package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/materialize"
)

// TestSkillFingerprintIgnoresVolatileGCArtifacts is the regression for the
// fleet-wide config-drift collapse (gt-c4g63): a stage-2 (tmux) skill source
// tree that overlaps a materialization sink / session workdir accumulates
// gc-managed transient artifacts on every reconcile tick — the
// `<workdir>/.gc/tmp/skill-catalog-<rand>.tmp` snapshot temp
// (writeSkillSnapshotFile), the `.gc-skill-ownership.json` sink marker, and
// atomic-write orphans named `<base>.tmp.<pid>.<unixnano>` (fsys.WriteFileAtomic).
//
// Those artifacts are NOT skill content, and their names/contents change every
// tick (UnixNano nonce ⇒ a fresh, never-repeating hash per tick). If the skill
// content hash includes them, an unchanged skill-set fingerprints differently
// on every reconcile, so every session drains-for-drift and respawns forever —
// the pool decays to one run-op and throughput collapses.
//
// The per-skill fingerprint must hash stable skill CONTENT only, so an
// unchanged skill-set fingerprints identically across reconciles regardless of
// gc's own materialization bookkeeping landing in the tree.
func TestSkillFingerprintIgnoresVolatileGCArtifacts(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	mustCreateSkill(t, filepath.Join(src, "plan"))
	// A real supporting file that IS content and must keep participating.
	if err := os.WriteFile(filepath.Join(src, "plan", "helper.sh"), []byte("echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	desired := []materialize.SkillEntry{{Name: "plan", Source: filepath.Join(src, "plan")}}

	baseline := mergeSkillFingerprintEntries(nil, desired)["skills:plan"]
	if baseline == "" {
		t.Fatal("baseline skills:plan hash empty")
	}

	// Simulate several reconcile ticks, each dropping fresh gc-managed
	// transient/volatile artifacts into the source tree (as happens when the
	// source overlaps the sink/workdir). Every tick's hash must equal baseline.
	skillDir := filepath.Join(src, "plan")
	for tick := 0; tick < 4; tick++ {
		nonce := strconv.FormatInt(time.Now().UnixNano()+int64(tick), 36)

		// snapshot temp under .gc/tmp (writeSkillSnapshotFile / os.CreateTemp)
		gcTmp := filepath.Join(skillDir, ".gc", "tmp")
		if err := os.MkdirAll(gcTmp, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gcTmp, "skill-catalog-"+nonce+".tmp"),
			[]byte("blob-"+nonce), 0o600); err != nil {
			t.Fatal(err)
		}
		// sink ownership marker
		if err := os.WriteFile(filepath.Join(skillDir, ".gc-skill-ownership.json"),
			[]byte(`{"targets":{"plan":"`+nonce+`"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		// atomic-write orphan (fsys.WriteFileAtomic temp: <base>.tmp.<pid>.<nanos>)
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md.tmp."+strconv.Itoa(os.Getpid())+"."+nonce),
			[]byte("partial-"+nonce), 0o644); err != nil {
			t.Fatal(err)
		}

		got := mergeSkillFingerprintEntries(nil, desired)["skills:plan"]
		if got != baseline {
			t.Fatalf("tick %d: skills:plan drifted with no skill edit: got %s want %s", tick, got, baseline)
		}
	}

	// A REAL content edit must still change the hash (the feature is preserved).
	if err := os.WriteFile(filepath.Join(skillDir, "helper.sh"), []byte("echo changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mergeSkillFingerprintEntries(nil, desired)["skills:plan"]; got == baseline {
		t.Fatalf("real content edit did not change skills:plan hash: %s", got)
	}
}
