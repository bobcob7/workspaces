package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SecretsFile is the secrets file in the config directory (ADR 0068).
const SecretsFile = "secrets.env"

// ReadSecrets parses CONFIG_DIR/secrets.env: NAME=value lines, # comments,
// blank lines, optional double quotes, no expansion. A missing file is
// empty. A mode looser than 0600 ignores the file and returns a warning.
// A bad line is an error naming the line number, never its value.
func ReadSecrets(configDir string) (map[string]string, []string, error) {
	path := filepath.Join(configDir, SecretsFile)
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", SecretsFile, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		warning := fmt.Sprintf("%s ignored: mode %04o is looser than 0600", SecretsFile, mode)
		return map[string]string{}, []string{warning}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", SecretsFile, err)
	}
	secrets, err := parseSecrets(data)
	if err != nil {
		return nil, nil, err
	}
	return secrets, nil, nil
}

// SetSecret writes or replaces NAME's line (temp file, rename, 0600),
// keeping other lines and comments. An empty value removes the line.
func SetSecret(configDir, name, value string) error {
	if !runnerEnvPattern.MatchString(name) {
		return fmt.Errorf("secret name %q must match %s", name, runnerEnvPattern)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("secret %s: value has a line break", name)
	}
	path := filepath.Join(configDir, SecretsFile)
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", SecretsFile, err)
	}
	return writeSecrets(path, []byte(editSecretLines(old, name, value)))
}

// parseSecrets reads every assignment in data. Duplicates keep the last value.
func parseSecrets(data []byte) (map[string]string, error) {
	secrets := map[string]string{}
	for number, line := range strings.Split(string(data), "\n") {
		if isSkippedSecretLine(line) {
			continue
		}
		name, value, ok := splitSecretLine(line)
		if !ok || name == "" {
			return nil, fmt.Errorf("%s line %d: want NAME=value", SecretsFile, number+1)
		}
		secrets[name] = value
	}
	return secrets, nil
}

// editSecretLines returns old with NAME's assignment replaced by value, or
// removed when value is empty. Other lines and comments are kept in order;
// extra assignments of NAME are dropped.
func editSecretLines(old []byte, name, value string) string {
	var lines []string
	if text := strings.TrimSuffix(string(old), "\n"); text != "" {
		lines = strings.Split(text, "\n")
	}
	var out []string
	written := false
	for _, line := range lines {
		lineName, _, ok := splitSecretLine(line)
		if !ok || lineName != name {
			out = append(out, line)
			continue
		}
		if value == "" || written {
			continue
		}
		out = append(out, name+"="+value)
		written = true
	}
	if !written && value != "" {
		out = append(out, name+"="+value)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// splitSecretLine returns a line's NAME and unquoted value. ok is false for
// a comment, a blank line, or a line without "=".
func splitSecretLine(line string) (name, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	name, value, ok = strings.Cut(trimmed, "=")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(name), unquote(strings.TrimSpace(value)), true
}

func isSkippedSecretLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

func unquote(value string) string {
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return value[1 : len(value)-1]
	}
	return value
}

// writeSecrets replaces path with data through a temp file, mode 0600.
func writeSecrets(path string, data []byte) error {
	target, err := resolveTarget(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+SecretsFile+"-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", SecretsFile, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := writeSynced(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", SecretsFile, err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("replacing %s: %w", SecretsFile, err)
	}
	return nil
}
