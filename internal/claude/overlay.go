package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// OverlayMarker is the file that marks an overlay; its one line is the base's absolute path.
const OverlayMarker = ".ws-overlay"

// overlayKeys are the top-level .claude.json keys an overlay keeps (ADR 0085).
func overlayKeys() []string {
	return []string{"userID", "oauthAccount", "hasCompletedOnboarding", "lastOnboardingVersion", "installMethod", "autoUpdates", "mcpServers", "theme"}
}

// overlayCopies are the base's files an overlay copies, so its edits stay private.
func overlayCopies() []string {
	return []string{"settings.json", "CLAUDE.md", "keybindings.json"}
}

// overlayLinks are the base's entries an overlay symlinks, the user's sign-in and add-ons.
func overlayLinks() []string {
	return []string{".credentials.json", "hooks", "agents", "commands", "skills", "plugins", "output-styles"}
}

// overlayDirs are symlinked too, and made in the base first if it lacks them.
func overlayDirs() []string {
	return []string{"projects", "jobs"}
}

// BaseConfigDir is the user's Claude Code config directory: the marker's path
// when $CLAUDE_CONFIG_DIR is an overlay, else $CLAUDE_CONFIG_DIR, else
// $HOME/.claude, else "".
func BaseConfigDir(environ []string) string {
	dir := lookup(environ, "CLAUDE_CONFIG_DIR")
	if base, ok := overlayBase(dir); ok {
		return base
	}
	if dir != "" {
		return dir
	}
	if home := userHome(environ); home != "" {
		return filepath.Join(home, ".claude")
	}
	return ""
}

// BaseEnviron is environ with CLAUDE_CONFIG_DIR set to BaseConfigDir when
// $CLAUDE_CONFIG_DIR is an overlay; otherwise environ unchanged (same slice contents).
func BaseEnviron(environ []string) []string {
	base, ok := overlayBase(lookup(environ, "CLAUDE_CONFIG_DIR"))
	if !ok {
		return environ
	}
	return append(slices.Clone(environ), "CLAUDE_CONFIG_DIR="+base)
}

// GlobalConfigFile is Claude Code's global config for environ:
// configFile(environ) exported.
func GlobalConfigFile(environ []string) string {
	return configFile(environ)
}

// SeedOverlay makes dir (0700) an overlay of base for a workspace under
// workspacesRoot, as ADR 0085's table says, with a cut-down copy of
// globalConfig (the base's .claude.json). It never writes under base,
// except to create empty projects/ and jobs/. An existing dir is removed
// and made again.
func SeedOverlay(dir, base, globalConfig, workspacesRoot string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clearing %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("making %s: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, OverlayMarker), []byte(base+"\n"), 0o600); err != nil {
		return fmt.Errorf("marking %s: %w", dir, err)
	}
	if err := copyBaseFiles(dir, base); err != nil {
		return err
	}
	if err := linkBaseEntries(dir, base); err != nil {
		return err
	}
	return seedGlobalConfig(dir, globalConfig, workspacesRoot)
}

// overlayBase is the base directory that dir's marker names, and whether dir is an overlay.
func overlayBase(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(dir, OverlayMarker))
	if err != nil {
		return "", false
	}
	line, _, _ := strings.Cut(string(data), "\n")
	base := strings.TrimSpace(line)
	return base, base != ""
}

// copyBaseFiles copies overlayCopies that base has into dir.
func copyBaseFiles(dir, base string) error {
	for _, name := range overlayCopies() {
		data, err := os.ReadFile(filepath.Join(base, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("copying %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil { //nolint:gosec // G703: name is from the fixed overlayCopies list.
			return fmt.Errorf("copying %s: %w", name, err)
		}
	}
	return nil
}

// linkBaseEntries makes base's overlayDirs, then symlinks every overlay
// link and directory that base has into dir.
func linkBaseEntries(dir, base string) error {
	for _, name := range overlayDirs() {
		if err := os.MkdirAll(filepath.Join(base, name), 0o700); err != nil {
			return fmt.Errorf("making %s in %s: %w", name, base, err)
		}
	}
	for _, name := range slices.Concat(overlayDirs(), overlayLinks()) {
		if err := linkIfPresent(dir, base, name); err != nil {
			return err
		}
	}
	return nil
}

// linkIfPresent symlinks dir/name to base/name, unless base lacks name.
func linkIfPresent(dir, base, name string) error {
	target := filepath.Join(base, name)
	_, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking %s: %w", target, err)
	}
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("linking %s: %w", name, err)
	}
	return nil
}

// seedGlobalConfig writes dir/.claude.json: overlayConfig of globalConfig.
func seedGlobalConfig(dir, globalConfig, workspacesRoot string) error {
	out, err := overlayConfig(globalConfig, workspacesRoot)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding .claude.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), data, 0o600); err != nil {
		return fmt.Errorf("writing .claude.json: %w", err)
	}
	return nil
}

// overlayConfig is the global config an overlay keeps: overlayKeys, and the
// projects of workspacesRoot and its parents that have accepted the trust
// dialog. A missing globalConfig gives an empty object.
func overlayConfig(globalConfig, workspacesRoot string) (map[string]any, error) {
	out := map[string]any{}
	data, err := os.ReadFile(globalConfig)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", globalConfig, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("reading %s: %w", globalConfig, err)
	}
	for _, key := range overlayKeys() {
		if value, ok := top[key]; ok {
			out[key] = value
		}
	}
	if projects := keptProjects(top["projects"], workspacesRoot); projects != nil {
		out["projects"] = projects
	}
	return out, nil
}

// keptProjects is the projects entries of root and its parents that are
// trusted, or nil when there are none or raw isn't an object.
func keptProjects(raw json.RawMessage, root string) map[string]map[string]json.RawMessage {
	var projects map[string]json.RawMessage
	if err := json.Unmarshal(raw, &projects); err != nil {
		return nil
	}
	root = resolved(root)
	kept := map[string]map[string]json.RawMessage{}
	for key, value := range projects {
		var project map[string]json.RawMessage
		if err := json.Unmarshal(value, &project); err != nil || !trusted(project) {
			continue
		}
		if within(root, resolved(key)) {
			kept[key] = project
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// within is whether path is dir or lies under it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
