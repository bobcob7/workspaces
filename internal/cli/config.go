package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/review/profile"
)

// configOwnersKey and configHostsKey are the settings that take any number
// of values (ADR 0054, ADR 0061).
const (
	configOwnersKey = "defaults.owners"
	configHostsKey  = "hosts"
)

// newConfigCmd is ws config: list, get, set, and unset config.toml's keys (ADR 0054).
func (a *App) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config <OP>",
		Short: "List, read, set, and unset config.toml's settings",
		Args:  knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(
		a.newConfigListCmd(),
		a.newConfigGetCmd(),
		a.newConfigSetCmd(),
		a.newConfigUnsetCmd(),
	)
	return cmd
}

// configSettingJSON is ws config get's output.
type configSettingJSON struct {
	Path     string         `json:"path"`
	Setting  config.Setting `json:"setting"`
	Warnings []string       `json:"warnings"`
}

// configListJSON is ws config list's output.
type configListJSON struct {
	Path     string           `json:"path"`
	Settings []config.Setting `json:"settings"`
	Warnings []string         `json:"warnings"`
}

// configChangeJSON is ws config set's and unset's output.
type configChangeJSON struct {
	Path     string         `json:"path"`
	Setting  config.Setting `json:"setting"`
	Changed  bool           `json:"changed"`
	Warnings []string       `json:"warnings"`
}

func (a *App) newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every setting, with its value and default",
		Args:  noArgs,
		RunE: runE(a.withConfigUnswept(func(_ *cobra.Command, _ []string) error {
			return a.configList()
		})),
	}
}

func (a *App) newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <KEY>",
		Short: "Print one setting's value",
		Args:  exactArgs(1, "setting key"),
		RunE: runE(a.withConfigUnswept(func(_ *cobra.Command, args []string) error {
			return a.configGet(args[0])
		})),
	}
}

func (a *App) newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <KEY> <VALUE>...",
		Short: "Set a setting, keeping the file's comments",
		Args:  configSetArgs,
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			return a.configSet(cmd.Context(), args[0], args[1:])
		})),
	}
}

func (a *App) newConfigUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <KEY>",
		Short: "Remove a setting, so its default applies",
		Args:  exactArgs(1, "setting key"),
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			return a.configUnset(cmd.Context(), args[0])
		})),
	}
}

// configSetArgs checks set's key and value count before config.toml is read.
func configSetArgs(cmd *cobra.Command, args []string) error {
	if err := atLeastOneArg("a setting key")(cmd, args); err != nil {
		return err
	}
	key := args[0]
	if _, err := config.Lookup(nil, key); err != nil {
		return configError(err)
	}
	if key == configOwnersKey || key == configHostsKey || len(args) == 2 {
		return nil
	}
	return usageErrorf("%s takes exactly one value for %s", commandName(cmd), key)
}

func (a *App) configList() error {
	data, err := a.readConfig()
	if err != nil {
		return err
	}
	settings, err := config.Settings(data)
	if err != nil {
		return configError(err)
	}
	if a.global.json {
		return a.writeJSON(configListJSON{Path: a.paths.ConfigFile, Settings: settings, Warnings: a.warnings()})
	}
	for _, s := range settings {
		fmt.Fprintln(a.stdout, configLine(s))
	}
	a.printWarnings()
	return nil
}

func (a *App) configGet(key string) error {
	data, err := a.readConfig()
	if err != nil {
		return err
	}
	setting, err := config.Lookup(data, key)
	if err != nil {
		return configError(err)
	}
	if a.global.json {
		return a.writeJSON(configSettingJSON{Path: a.paths.ConfigFile, Setting: setting, Warnings: a.warnings()})
	}
	a.printConfigValue(setting.Value)
	a.printWarnings()
	return nil
}

// configSet sets key to values in config.toml. A review profile's key must
// name a profile that ws review profiles lists (ADR 0054).
func (a *App) configSet(ctx context.Context, key string, values []string) error {
	if err := a.checkProfileKey(key); err != nil {
		return err
	}
	setting, changed, err := a.editConfig(ctx, key, func(data []byte) ([]byte, bool, error) {
		return config.Set(data, key, values)
	})
	if err != nil {
		return err
	}
	return a.printConfigChange(setting, changed)
}

func (a *App) configUnset(ctx context.Context, key string) error {
	setting, changed, err := a.editConfig(ctx, key, func(data []byte) ([]byte, bool, error) {
		return config.Unset(data, key)
	})
	if err != nil {
		return err
	}
	return a.printConfigChange(setting, changed)
}

// editConfig applies edit to config.toml under the config lock. It writes
// the file only when edit changed it, and returns key's setting as the file
// now says.
func (a *App) editConfig(ctx context.Context, key string, edit func([]byte) ([]byte, bool, error)) (config.Setting, bool, error) {
	unlock, err := lockfile.Exclusive(ctx, filepath.Join(a.paths.Root, "config.lock"))
	if err != nil {
		return config.Setting{}, false, fmt.Errorf("locking config: %w", err)
	}
	defer unlock()
	data, err := a.readConfig()
	if err != nil {
		return config.Setting{}, false, err
	}
	out, changed, err := edit(data)
	if err != nil {
		return config.Setting{}, false, configError(err)
	}
	if changed {
		err = config.WriteFile(a.paths.ConfigFile, out)
		if err != nil {
			return config.Setting{}, false, fmt.Errorf("writing config: %w", err)
		}
	}
	setting, err := config.Lookup(out, key)
	if err != nil {
		return config.Setting{}, false, configError(err)
	}
	return setting, changed, nil
}

// checkProfileKey refuses a review.profiles.<name>.* key for a profile that
// ws review profiles doesn't list.
func (a *App) checkProfileKey(key string) error {
	name, ok := profileKeyName(key)
	if !ok {
		return nil
	}
	profiles, err := a.reviewProfiles()
	if err != nil {
		return fmt.Errorf("listing review profiles: %w", err)
	}
	if slices.ContainsFunc(profiles, func(p profile.Profile) bool { return p.Name == name }) {
		return nil
	}
	return newError(codeProfileUnknown, fmt.Errorf("no review profile named %q", name))
}

// profileKeyName is the profile that a review.profiles.<name>.* key names.
func profileKeyName(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, "review.profiles.")
	if !ok {
		return "", false
	}
	name, _, _ := strings.Cut(rest, ".")
	return name, true
}

// readConfig returns config.toml's text.
func (a *App) readConfig() ([]byte, error) {
	data, err := os.ReadFile(a.paths.ConfigFile)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return data, nil
}

// printConfigChange prints set's or unset's result, as JSON or as the key's
// line.
func (a *App) printConfigChange(setting config.Setting, changed bool) error {
	if a.global.json {
		return a.writeJSON(configChangeJSON{Path: a.paths.ConfigFile, Setting: setting, Changed: changed, Warnings: a.warnings()})
	}
	fmt.Fprintln(a.stdout, configLine(setting))
	a.printWarnings()
	return nil
}

// printConfigValue prints a value bare, as ws config get does: a list has one
// item per line, and a host list one name=kind per line.
func (a *App) printConfigValue(value any) {
	switch v := value.(type) {
	case []string:
		for _, s := range v {
			fmt.Fprintln(a.stdout, s)
		}
	case []config.Host:
		for _, h := range v {
			fmt.Fprintln(a.stdout, h.Name+"="+h.Kind)
		}
	default:
		fmt.Fprintln(a.stdout, value)
	}
}

// configLine is a setting as ws config list prints it, with a default marker
// when the file doesn't set it.
func configLine(s config.Setting) string {
	line := s.Key + " = " + configTOML(s.Value)
	if s.Set {
		return line
	}
	return line + "  # default"
}

// configTOML is a value written as TOML.
func configTOML(value any) string {
	switch v := value.(type) {
	case bool:
		return strconv.FormatBool(v)
	case []string:
		quoted := make([]string, 0, len(v))
		for _, s := range v {
			quoted = append(quoted, strconv.Quote(s))
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	case []config.Host:
		tables := make([]string, 0, len(v))
		for _, h := range v {
			tables = append(tables, "{name = "+strconv.Quote(h.Name)+", kind = "+strconv.Quote(h.Kind)+"}")
		}
		return "[" + strings.Join(tables, ", ") + "]"
	}
	return strconv.Quote(fmt.Sprint(value))
}

// configError gives an error from reading or editing config.toml its error
// code (ADR 0054). Value errors wrap ErrInvalid too, so they're checked first.
func configError(err error) *cliError {
	switch {
	case errors.Is(err, config.ErrKeyUnknown):
		return newError(codeConfigKeyUnknown, err)
	case errors.Is(err, config.ErrValueInvalid):
		return newError(codeConfigValueInvalid, err)
	case errors.Is(err, config.ErrUneditable):
		return newError(codeConfigUneditable, err)
	case errors.Is(err, config.ErrInvalid):
		return newError(codeConfigInvalid, err)
	}
	return newError(codeInternal, err)
}
