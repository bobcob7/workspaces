package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// CheckPath resolves p against dir, following symlinks of the existing
// part, and fails unless the result is inside root and not under any .git.
func CheckPath(root, dir, p string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	resolved, err := resolveExisting(filepath.Clean(p))
	if err != nil {
		return "", err
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolving workspace: %w", err)
	}
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %s is outside the workspace", p)
	}
	if slices.Contains(strings.Split(rel, string(filepath.Separator)), ".git") {
		return "", fmt.Errorf("path %s is under .git", p)
	}
	return resolved, nil
}

// resolveExisting resolves the symlinks of the longest existing prefix of
// p and appends the rest. A dangling symlink is an error, since writing
// through it would leave the directory it points from.
func resolveExisting(p string) (string, error) {
	existing, rest := p, ""
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			resolved, evalErr := filepath.EvalSymlinks(existing)
			if evalErr != nil {
				return "", fmt.Errorf("resolving %s: %w", existing, evalErr)
			}
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", err
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
}

// CheckCommand refuses: a word "ws" not followed by "machine result";
// "claude"; "sudo"; "git push" with -f/--force/--force-with-lease to a ref
// other than branch; "git push" to main or master. Words: strings.Fields
// after replacing ;&|() with spaces.
func CheckCommand(command, branch string) error {
	words := strings.Fields(commandSeparators(command))
	if err := checkWS(words); err != nil {
		return err
	}
	for _, word := range words {
		if word == "claude" || word == "sudo" {
			return fmt.Errorf("command refused: %s", word)
		}
	}
	for _, segment := range strings.FieldsFunc(command, isSegmentEnd) {
		if err := checkPush(strings.Fields(segment), branch); err != nil {
			return err
		}
	}
	return nil
}

// commandSeparators replaces the shell operators with spaces.
func commandSeparators(command string) string {
	return strings.NewReplacer(";", " ", "&", " ", "|", " ", "(", " ", ")", " ").Replace(command)
}

func isSegmentEnd(r rune) bool {
	return strings.ContainsRune(";&|()\n", r)
}

func checkWS(words []string) error {
	for i, word := range words {
		if word != "ws" {
			continue
		}
		if i+2 < len(words) && words[i+1] == "machine" && words[i+2] == "result" {
			continue
		}
		return errors.New(`command refused: ws may only run "ws machine result"`)
	}
	return nil
}

// checkPush applies the push rules to the words of one command segment,
// if they are a git push.
func checkPush(words []string, branch string) error {
	for i := 0; i+1 < len(words); i++ {
		if words[i] == "git" && words[i+1] == "push" {
			return checkPushArgs(words[i+2:], branch)
		}
	}
	return nil
}

// checkPushArgs refuses pushes to main or master, and forced pushes to
// anything but branch. The first target is the remote.
func checkPushArgs(args []string, branch string) error {
	var targets []string
	force := false
	for _, arg := range args {
		switch {
		case isForceFlag(arg):
			force = true
		case !strings.HasPrefix(arg, "-"):
			targets = append(targets, arg)
		}
	}
	for _, target := range targets {
		destination := pushDestination(target)
		if destination == "main" || destination == "master" {
			return fmt.Errorf("command refused: git push to %s", destination)
		}
		force = force || strings.HasPrefix(target, "+")
	}
	if !force {
		return nil
	}
	if len(targets) < 2 {
		return errors.New("command refused: a force push needs an explicit branch")
	}
	return checkForcePush(targets[1:], branch)
}

// checkForcePush allows a forced push only to branch.
func checkForcePush(refs []string, branch string) error {
	for _, ref := range refs {
		destination := pushDestination(ref)
		if branch == "" || destination != branch {
			return fmt.Errorf("command refused: force push to %s; only %q is allowed", destination, branch)
		}
	}
	return nil
}

// pushDestination returns the ref a push target names: the part after the
// refspec's colon, without a leading + or refs/heads/.
func pushDestination(target string) string {
	destination := strings.TrimPrefix(target, "+")
	destination = destination[strings.LastIndex(destination, ":")+1:]
	return strings.TrimPrefix(destination, "refs/heads/")
}

// isForceFlag reports whether a push argument forces the update. Short
// flags may be bundled, as in -uf.
func isForceFlag(arg string) bool {
	if strings.HasPrefix(arg, "--") {
		return arg == "--force" || arg == "--force-if-includes" || strings.HasPrefix(arg, "--force-with-lease")
	}
	return strings.HasPrefix(arg, "-") && strings.ContainsRune(arg, 'f')
}
