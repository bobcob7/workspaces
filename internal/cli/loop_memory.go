package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/loop"
)

// memoryPreviewChars is how much of a value ws loop memory list prints.
const memoryPreviewChars = 60

// memoryEntryJSON is one key of a loop's memory in ws loop memory's and ws
// machine memory's output.
type memoryEntryJSON struct {
	Key       string  `json:"key"`
	Value     string  `json:"value"`
	Rev       int     `json:"rev"`
	UpdatedAt string  `json:"updated_at"`
	UpdatedBy *string `json:"updated_by"`
}

// memoryListJSON is a loop's whole memory: list's output.
type memoryListJSON struct {
	Loop     string            `json:"loop"`
	Entries  []memoryEntryJSON `json:"entries"`
	Bytes    int               `json:"bytes"`
	Warnings []string          `json:"warnings"`
}

// memoryEntryResultJSON is one key of a loop's memory: get's and set's output.
type memoryEntryResultJSON struct {
	Loop     string          `json:"loop"`
	Entry    memoryEntryJSON `json:"entry"`
	Warnings []string        `json:"warnings"`
}

// memoryRemovedJSON is rm's output.
type memoryRemovedJSON struct {
	Loop     string   `json:"loop"`
	Key      string   `json:"key"`
	Removed  bool     `json:"removed"`
	Warnings []string `json:"warnings"`
}

// memorySetOptions are the flags of memory set and rm.
type memorySetOptions struct {
	expectRev int
	file      string
}

// memoryEntryJSONOf is key with its entry as the memory commands print it.
func memoryEntryJSONOf(key string, e loop.MemoryEntry) memoryEntryJSON {
	return memoryEntryJSON{
		Key:       key,
		Value:     e.Value,
		Rev:       e.Rev,
		UpdatedAt: formatTime(e.UpdatedAt),
		UpdatedBy: optional(e.UpdatedBy),
	}
}

func (a *App) newLoopMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory <OP>",
		Short: "Read and change what a loop's runs remember",
		Args:  knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(
		a.newLoopMemoryListCmd(),
		a.newLoopMemoryGetCmd(),
		a.newLoopMemorySetCmd(),
		a.newLoopMemoryRmCmd(),
		a.newLoopMemoryClearCmd(),
	)
	return cmd
}

func (a *App) newLoopMemoryListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <NAME>",
		Short: "List a loop's memory",
		Args:  exactArgs(1, "loop name"),
		RunE: runE(a.withConfig(func(_ *cobra.Command, args []string) error {
			if err := a.requireLoop(args[0]); err != nil {
				return err
			}
			return a.memoryList(args[0])
		})),
	}
}

func (a *App) newLoopMemoryGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <NAME> <KEY>",
		Short: "Print one memory value",
		Args:  exactArgs(2, "arguments: a loop name and a key"),
		RunE: runE(a.withConfig(func(_ *cobra.Command, args []string) error {
			if err := a.requireLoop(args[0]); err != nil {
				return err
			}
			return a.memoryGet(args[0], args[1])
		})),
	}
}

func (a *App) newLoopMemorySetCmd() *cobra.Command {
	var opts memorySetOptions
	cmd := &cobra.Command{
		Use:   "set [--expect-rev <N>] [--file <PATH> | --file -] <NAME> <KEY> [<VALUE>]",
		Short: "Set one memory value",
		Args:  argsBetween(2, 3, "arguments: a loop name, a key, and a value"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			value, err := a.memoryValue(args[2:], opts.file)
			if err != nil {
				return err
			}
			if err := a.requireLoop(args[0]); err != nil {
				return err
			}
			return a.memorySet(cmd.Context(), args[0], args[1], value, "", opts.expectRev)
		})),
	}
	addMemorySetFlags(cmd, &opts)
	return cmd
}

func (a *App) newLoopMemoryRmCmd() *cobra.Command {
	var opts memorySetOptions
	cmd := &cobra.Command{
		Use:   "rm [--expect-rev <N>] <NAME> <KEY>",
		Short: "Remove one memory value",
		Args:  exactArgs(2, "arguments: a loop name and a key"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			if err := a.requireLoop(args[0]); err != nil {
				return err
			}
			return a.memoryRm(cmd.Context(), args[0], args[1], opts.expectRev)
		})),
	}
	cmd.Flags().IntVar(&opts.expectRev, "expect-rev", loop.AnyRev, expectRevUsage)
	return cmd
}

func (a *App) newLoopMemoryClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear <NAME>",
		Short: "Remove all of a loop's memory",
		Args:  exactArgs(1, "loop name"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			if err := a.requireLoop(args[0]); err != nil {
				return err
			}
			return a.memoryClear(cmd.Context(), args[0])
		})),
	}
}

// expectRevUsage is the --expect-rev flag's help text.
const expectRevUsage = "fail with memory_conflict unless the key's revision is N; 0 means the key must not exist"

// addMemorySetFlags adds the flags of memory set to cmd.
func addMemorySetFlags(cmd *cobra.Command, opts *memorySetOptions) {
	cmd.Flags().IntVar(&opts.expectRev, "expect-rev", loop.AnyRev, expectRevUsage)
	cmd.Flags().StringVar(&opts.file, "file", "", "read the value from this file; - reads stdin")
}

// argsBetween requires from lo to hi arguments, as a usage error.
func argsBetween(lo, hi int, what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < lo || len(args) > hi {
			return usageErrorf("%s needs %s, got %d", commandName(cmd), what, len(args))
		}
		return nil
	}
}

// requireLoop is the usage of loop_not_found for a loop that doesn't exist,
// checked before any memory command touches the loop's files.
func (a *App) requireLoop(name string) error {
	if _, err := a.loops.Get(name); err != nil {
		return domainError(err)
	}
	return nil
}

// memoryValue is the value a set stores: its one argument, or the contents of
// --file. Exactly one of them is needed.
func (a *App) memoryValue(args []string, file string) (string, error) {
	switch {
	case len(args) > 0 && file != "":
		return "", usageErrorf("set takes a value or --file, not both")
	case len(args) > 0:
		return args[0], nil
	case file != "":
		return a.readMemoryFile(file)
	}
	return "", usageErrorf("set needs a value, or --file")
}

// readMemoryFile is the contents of file, or of stdin for "-". It reads one
// byte past the value limit, so the store refuses a value that is too long.
func (a *App) readMemoryFile(file string) (string, error) {
	src := a.stdin
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return "", usageErrorf("reading the value: %w", err)
		}
		defer f.Close()
		src = f
	}
	value, err := io.ReadAll(io.LimitReader(src, loop.MaxMemoryValue+1))
	if err != nil {
		return "", usageErrorf("reading the value: %w", err)
	}
	return string(value), nil
}

// memoryList prints a loop's memory, sorted by key.
func (a *App) memoryList(name string) error {
	mem, err := a.loopData.ReadMemory(name)
	if err != nil {
		return domainError(err)
	}
	keys := mem.Keys()
	if a.global.json {
		entries := make([]memoryEntryJSON, 0, len(keys))
		for _, key := range keys {
			entries = append(entries, memoryEntryJSONOf(key, mem.Entries[key]))
		}
		return a.writeJSON(memoryListJSON{Loop: name, Entries: entries, Bytes: mem.Bytes(), Warnings: a.warnings()})
	}
	for _, key := range keys {
		e := mem.Entries[key]
		fmt.Fprintf(a.stdout, "%s  rev %d  %s  %s\n", key, e.Rev, formatTime(e.UpdatedAt), memoryPreview(e.Value))
	}
	a.printWarnings()
	return nil
}

// memoryPreview is the first line of value, cut to memoryPreviewChars.
func memoryPreview(value string) string {
	line, _, _ := strings.Cut(value, "\n")
	if runes := []rune(line); len(runes) > memoryPreviewChars {
		return string(runes[:memoryPreviewChars])
	}
	return line
}

// memoryGet prints the value of key in a loop's memory, exactly as stored.
func (a *App) memoryGet(name, key string) error {
	mem, err := a.loopData.ReadMemory(name)
	if err != nil {
		return domainError(err)
	}
	e, ok := mem.Entries[key]
	if !ok {
		return domainError(fmt.Errorf("%w: %s", loop.ErrMemoryNotFound, key))
	}
	if a.global.json {
		return a.writeJSON(memoryEntryResultJSON{Loop: name, Entry: memoryEntryJSONOf(key, e), Warnings: a.warnings()})
	}
	fmt.Fprint(a.stdout, e.Value)
	a.printWarnings()
	return nil
}

// memorySet stores value under key and prints the key's new revision.
func (a *App) memorySet(ctx context.Context, name, key, value, by string, expectRev int) error {
	e, err := a.loopData.SetMemory(ctx, name, key, value, by, expectRev, a.now())
	if err != nil {
		return domainError(err)
	}
	if a.global.json {
		return a.writeJSON(memoryEntryResultJSON{Loop: name, Entry: memoryEntryJSONOf(key, e), Warnings: a.warnings()})
	}
	fmt.Fprintf(a.stdout, "%s  rev %d\n", key, e.Rev)
	a.printWarnings()
	return nil
}

// memoryRm removes key from a loop's memory.
func (a *App) memoryRm(ctx context.Context, name, key string, expectRev int) error {
	if err := a.loopData.RemoveMemory(ctx, name, key, expectRev); err != nil {
		return domainError(err)
	}
	if a.global.json {
		return a.writeJSON(memoryRemovedJSON{Loop: name, Key: key, Removed: true, Warnings: a.warnings()})
	}
	fmt.Fprintf(a.stdout, "%s removed\n", key)
	a.printWarnings()
	return nil
}

// memoryClear removes every key from a loop's memory and prints how many.
func (a *App) memoryClear(ctx context.Context, name string) error {
	removed, err := a.loopData.ClearMemory(ctx, name)
	if err != nil {
		return domainError(err)
	}
	if a.global.json {
		return a.writeJSON(loopClearedJSON{Loop: name, Removed: removed, Warnings: a.warnings()})
	}
	fmt.Fprintf(a.stdout, "%d keys removed\n", removed)
	a.printWarnings()
	return nil
}
