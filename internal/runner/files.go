package runner

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	json "encoding/json/v2"

	"github.com/BurntSushi/toml"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/mcp"
)

const (
	runDirName     = "runner"
	runFile        = "run.toml"
	statusFile     = "status.json"
	transcriptFile = "transcript.jsonl"
	logFile        = "runner.log"
	stopFile       = "stop"
	reportFile     = "report.md"
	usageFile      = "usage.json"
	fileMode       = 0o600
	dirMode        = 0o700
)

// Files is a runner session's directory, state/<id>/runner.
type Files struct {
	Dir string // state/<id>/runner
	ID  string // the workspace id, the state directory's name
}

// FilesFor returns the runner files of workspace id under stateDir.
func FilesFor(stateDir, id string) Files {
	return Files{Dir: filepath.Join(stateDir, id, runDirName), ID: id}
}

// ReadRun reads run.toml.
func (f Files) ReadRun() (Run, error) {
	var run Run
	data, err := os.ReadFile(f.path(runFile))
	if err != nil {
		return Run{}, fmt.Errorf("reading run.toml: %w", err)
	}
	if _, err := toml.Decode(string(data), &run); err != nil {
		return Run{}, fmt.Errorf("parsing run.toml: %w", err)
	}
	return run, nil
}

// WriteRun writes run.toml through a temp file and a rename.
func (f Files) WriteRun(run *Run) error {
	var buf bytes.Buffer
	//nolint:gosec // G117: "pass" is the role's verdict list, not a secret.
	if err := toml.NewEncoder(&buf).Encode(run); err != nil {
		return fmt.Errorf("encoding run.toml: %w", err)
	}
	return writeAtomic(f.path(runFile), buf.Bytes())
}

// AppendEndpoint adds e to run.toml's endpoint history. Only the runner
// calls it, after the spawn.
//
//nolint:gocritic // hugeParam: the spec fixes the by-value signature.
func (f Files) AppendEndpoint(e EndpointEntry) error {
	run, err := f.ReadRun()
	if err != nil {
		return err
	}
	run.EndpointHistory = append(run.EndpointHistory, e)
	return f.WriteRun(&run)
}

// ReadStatus reads status.json. A missing file wraps fs.ErrNotExist.
func (f Files) ReadStatus() (Status, error) {
	var st Status
	data, err := os.ReadFile(f.path(statusFile))
	if err != nil {
		return Status{}, fmt.Errorf("reading status.json: %w", err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return Status{}, fmt.Errorf("parsing status.json: %w", err)
	}
	return st, nil
}

// WriteStatus writes status.json through a temp file and a rename. Its lists
// are written as [] rather than null, so readers can always range over them.
func (f Files) WriteStatus(st *Status) error {
	st.Version = statusVersion
	out := *st
	if out.MCP == nil {
		out.MCP = []mcp.Status{}
	}
	if out.Endpoints == nil {
		out.Endpoints = []agent.EndpointUsage{}
	}
	data, err := json.Marshal(&out)
	if err != nil {
		return fmt.Errorf("encoding status.json: %w", err)
	}
	return writeAtomic(f.path(statusFile), data)
}

// Transcript is transcript.jsonl's path.
func (f Files) Transcript() string { return f.path(transcriptFile) }

// Log is runner.log's path.
func (f Files) Log() string { return f.path(logFile) }

// RequestStop creates the stop file, which the loop checks before each step.
func (f Files) RequestStop() error {
	if err := os.MkdirAll(f.Dir, dirMode); err != nil {
		return fmt.Errorf("creating runner directory: %w", err)
	}
	if err := os.WriteFile(f.path(stopFile), nil, fileMode); err != nil {
		return fmt.Errorf("writing stop: %w", err)
	}
	return nil
}

// StopRequested reports whether the stop file exists.
func (f Files) StopRequested() bool {
	_, err := os.Stat(f.path(stopFile))
	return err == nil
}

// UsagePath is usage.json's path: next to the result under the parent's
// machine directory, or in the runner directory without a parent.
func (f Files) UsagePath(run *Run, store resultStore) (string, error) {
	if run.Parent == "" {
		return f.path(usageFile), nil
	}
	root, _, err := store.Lineage(f.ID)
	if err != nil {
		return "", fmt.Errorf("finding %s's root: %w", f.ID, err)
	}
	return filepath.Join(store.Dir(root, f.ID), usageFile), nil
}

func (f Files) path(name string) string {
	return filepath.Join(f.Dir, name)
}

// isMissing reports whether err is a file that doesn't exist.
func isMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// writeAtomic writes data to path through a temp file in the same
// directory, with mode 0600, and renames it into place.
func writeAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name()) // the write already failed; the temp file is only debris
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err = tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting mode of %s: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", path, err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("renaming into %s: %w", path, err)
	}
	return nil
}
