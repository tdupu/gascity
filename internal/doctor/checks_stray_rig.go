package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
)

// StrayRigOnDiskCheck reports immediate subdirectories of the city path
// that look like rig repositories (contain a ".repo.git/" child) but are
// not registered as [[rigs]] in city.toml. Pull-based: it only runs when
// the operator invokes `gc doctor`, never auto-registers, and offers no
// `--fix` because the right action for each stray (register, document as
// deferred, remove) is an operator judgement call.
type StrayRigOnDiskCheck struct {
	cfg      *config.City
	cityPath string
}

// NewStrayRigOnDiskCheck creates a check for unregistered rig directories
// under cityPath.
func NewStrayRigOnDiskCheck(cfg *config.City, cityPath string) *StrayRigOnDiskCheck {
	return &StrayRigOnDiskCheck{cfg: cfg, cityPath: cityPath}
}

// Name returns the check identifier.
func (c *StrayRigOnDiskCheck) Name() string { return "stray-rig-on-disk" }

// Run scans cityPath for unregistered rig directories.
func (c *StrayRigOnDiskCheck) Run(_ *CheckContext) *CheckResult {
	r := &CheckResult{Name: c.Name()}

	registered := registeredRigNames(c.cfg, c.cityPath)

	entries, err := os.ReadDir(c.cityPath)
	if err != nil {
		r.Status = StatusWarning
		r.Message = fmt.Sprintf("could not scan %q: %v", c.cityPath, err)
		return r
	}

	var strays []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if registered[name] {
			continue
		}
		repoGit := filepath.Join(c.cityPath, name, ".repo.git")
		info, err := os.Stat(repoGit)
		if err != nil || !info.IsDir() {
			continue
		}
		strays = append(strays, name)
	}
	sort.Strings(strays)

	if len(strays) == 0 {
		r.Status = StatusOK
		r.Message = "no unregistered rig directories on disk"
		return r
	}

	details := make([]string, 0, len(strays))
	for _, name := range strays {
		details = append(details, fmt.Sprintf(
			"%s/.repo.git/ exists but %q is not registered in city.toml [[rigs]]",
			filepath.Join(c.cityPath, name), name))
	}

	hintLines := []string{"for each stray, choose one:"}
	for _, name := range strays {
		hintLines = append(hintLines, fmt.Sprintf("  gc rig add %s [--prefix <p>]   # register", name))
	}
	hintLines = append(hintLines,
		"  OR document the deferral",
		"  OR rm -rf the directory if unwanted")

	r.Status = StatusWarning
	if len(strays) == 1 {
		r.Message = "1 rig directory on disk not registered in city.toml"
	} else {
		r.Message = fmt.Sprintf("%d rig directories on disk not registered in city.toml", len(strays))
	}
	r.Severity = SeverityAdvisory
	r.Details = details
	r.FixHint = strings.Join(hintLines, "\n")
	return r
}

// CanFix returns false; the right action per stray is an operator choice.
func (c *StrayRigOnDiskCheck) CanFix() bool { return false }

// Fix is a no-op.
func (c *StrayRigOnDiskCheck) Fix(_ *CheckContext) error { return nil }

// registeredRigNames returns the set of subdirectory names under cityPath
// that are covered by a registered rig. A rig covers a subdir when either
// (a) the rig name equals the subdir name, or (b) the rig's resolved Path
// is the subdir's absolute path — this lets rigs with custom paths under
// the city avoid spurious flags.
func registeredRigNames(cfg *config.City, cityPath string) map[string]bool {
	covered := make(map[string]bool, len(cfg.Rigs))
	for _, rig := range cfg.Rigs {
		covered[rig.Name] = true
		if rig.Path == "" {
			continue
		}
		rigPath := rig.Path
		if !filepath.IsAbs(rigPath) {
			rigPath = filepath.Join(cityPath, rigPath)
		}
		rigPath = filepath.Clean(rigPath)
		// Only mark by basename when the path resolves inside cityPath;
		// out-of-city rigs do not shadow a same-named on-disk directory.
		rel, err := filepath.Rel(filepath.Clean(cityPath), rigPath)
		if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
			continue
		}
		covered[filepath.Base(rigPath)] = true
	}
	return covered
}
