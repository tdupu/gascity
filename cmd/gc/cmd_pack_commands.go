package main

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/spf13/cobra"
)

// packCommandsCityConfigCache stores city configs loaded by registerPackCommands
// so that doBd (and other same-invocation callers) can reuse the loaded config
// without a second full parse + pack expansion (~300ms saving per gc bd call).
var packCommandsCityConfigCache sync.Map // normalizedCityPath → *config.City

// cachedCityConfig returns the *config.City cached by registerPackCommands for
// the given cityPath, or nil/false when no cache entry exists.
func cachedCityConfig(cityPath string) (*config.City, bool) {
	v, ok := packCommandsCityConfigCache.Load(normalizePathForCompare(cityPath))
	if !ok {
		return nil, false
	}
	return v.(*config.City), true
}

func addPackCommandsToRoot(root *cobra.Command, entries []config.PackCommandInfo, cityPath, cityName string, stdout, stderr io.Writer) {
	discovered := make([]config.DiscoveredCommand, 0, len(entries))
	for _, entry := range entries {
		discovered = append(discovered, discoveredCommandFromPackCommandInfo(entry))
	}
	addDiscoveredCommandsToRoot(root, discovered, cityPath, cityName, stdout, stderr, true)
}

func discoveredCommandFromPackCommandInfo(info config.PackCommandInfo) config.DiscoveredCommand {
	helpFile := strings.TrimSpace(info.Entry.LongDescription)
	if helpFile != "" && !filepath.IsAbs(helpFile) {
		helpFile = filepath.Join(info.PackDir, helpFile)
	}
	return config.DiscoveredCommand{
		Name:        info.Entry.Name,
		Command:     []string{info.Entry.Name},
		Description: info.Entry.Description,
		RunScript:   info.Entry.Script,
		HelpFile:    helpFile,
		SourceDir:   info.PackDir,
		PackDir:     info.PackDir,
		PackName:    info.PackName,
		BindingName: info.PackName,
	}
}

// quietLoadCityConfig loads city config with log output suppressed.
// ExpandCityPacks logs "not found, skipping" for uncached remote packs
// which is confusing during cobra command-tree setup (before gc start
// has fetched them). The expander already skips missing packs gracefully;
// we just silence the log noise.
func quietLoadCityConfig(cityPath string) (*config.City, error) {
	prev := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(prev)
	return loadCityConfig(cityPath, io.Discard)
}

// firstNonFlagArg returns the first argument in args that is not a flag
// (does not begin with "-"). Returns "" when no such argument is found.
func firstNonFlagArg(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// registerPackCommands attempts to discover the city, load config, and
// register pack-provided CLI commands as top-level subcommands. Fails
// silently if not in a city or config fails to load — core commands
// always work.
func registerPackCommands(root *cobra.Command, stdout, stderr io.Writer) {
	// git spawns `gc git-credential` mid-clone, while gc may already hold the
	// repo-cache lock for the very import being fetched (a credentialed
	// `gc import install`). Pack-command discovery loads city config, which
	// re-acquires that lock — a self-deadlock that hangs every credentialed
	// import. The helper needs no pack commands, so skip discovery for it.
	if isCredentialHelperInvocation(os.Args) {
		return
	}
	// Skip pack discovery when the user is invoking a built-in command.
	// Pack commands are top-level additions only; they cannot shadow or extend
	// built-in subcommands. Avoids ~300-500ms of city discovery + config load
	// for standard gc bd, gc status, gc hook, etc. invocations.
	if name := firstNonFlagArg(os.Args[1:]); name != "" && coreCommandNames(root)[name] {
		return
	}
	cityPath, err := resolveCity()
	if err != nil {
		return
	}
	cfg, err := quietLoadCityConfig(cityPath)
	if err != nil {
		return
	}
	packCommandsCityConfigCache.Store(normalizePathForCompare(cityPath), cfg)

	if len(cfg.PackCommands) == 0 {
		return
	}

	addDiscoveredCommandsToRoot(root, cfg.PackCommands, cityPath, loadedCityName(cfg, cityPath), stdout, stderr, false)
}

// isCredentialHelperInvocation reports whether argv invokes the hidden
// `gc git-credential` helper (git runs it as `gc git-credential <op>`). The
// helper is a leaf command on git's clone hot path, so it must skip the
// config-loading pack-command discovery that runs for normal invocations.
func isCredentialHelperInvocation(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		if strings.HasPrefix(argv[i], "-") {
			continue
		}
		return argv[i] == "git-credential"
	}
	return false
}

// coreCommandNames returns the set of built-in command names that packs
// must not shadow.
func coreCommandNames(root *cobra.Command) map[string]bool {
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
		for _, alias := range c.Aliases {
			names[alias] = true
		}
	}
	// Also reserve "help" and "completion" which cobra may add.
	names["help"] = true
	names["completion"] = true
	return names
}

// stdin returns os.Stdin. Extracted for testability (tests can override).
var stdin = func() io.Reader { return os.Stdin }

// expandScriptTemplate expands Go text/template variables in the script
// path. On any error, returns the raw script string (graceful fallback).
func expandScriptTemplate(script, cityPath, cityName, packDir string) string {
	if !strings.Contains(script, "{{") {
		return script
	}
	ctx := SessionSetupContext{
		CityRoot:  cityPath,
		CityName:  cityName,
		ConfigDir: packDir,
	}
	tmpl, err := template.New("script").Parse(script)
	if err != nil {
		return script
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return script
	}
	return buf.String()
}

// tryPackCommandFallback is a lazy fallback for the root command's RunE.
// If eager discovery missed a pack command (e.g. config changed), try
// one more time. Returns true if a pack command was found and executed.
func tryPackCommandFallback(args []string, stdout, stderr io.Writer) bool {
	if len(args) == 0 {
		return false
	}

	cityPath, err := resolveCity()
	if err != nil {
		return false
	}
	cfg, err := quietLoadCityConfig(cityPath)
	if err != nil {
		return false
	}

	return tryDiscoveredCommandFallback(args, cfg, cityPath, stdout, stderr)
}
