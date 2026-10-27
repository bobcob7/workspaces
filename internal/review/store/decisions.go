package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/collect"
)

// decisionsName is the file in a commit's directory that holds the
// decisions on its findings (ADR 0040). list skips it, and no profile may
// be called decisions.
const decisionsName = "decisions.json"

// decisionsVersion is decisions.json's version.
const decisionsVersion = 1

// What a person chose to do with a finding.
const (
	DecisionComment = "comment"
	DecisionFix     = "fix"
	DecisionSkip    = "skip"
)

// Decision is the choice made on one finding.
type Decision struct {
	// Decision is DecisionComment, DecisionFix, or DecisionSkip.
	Decision string `json:"decision"`
	// Suggestion is the index of the chosen suggestion for a fix, or nil.
	Suggestion *int      `json:"suggestion"`
	Note       string    `json:"note"`
	At         time.Time `json:"at"`
	// Done is set once the decision has been acted on.
	Done *Done `json:"done"`
}

// Done is how a decision was acted on (ADR 0041): one of Commented and
// FixSent is set.
type Done struct {
	// Commented is the URL of the posted review.
	Commented string `json:"commented,omitempty"`
	// FixSent is the agent the fix was sent to.
	FixSent string    `json:"fix_sent,omitempty"`
	At      time.Time `json:"at"`
}

// Decisions are the decisions on a commit's findings, by qualified id.
type Decisions map[string]Decision

// decisionsFile is decisions.json.
type decisionsFile struct {
	Version   int       `json:"version"`
	Decisions Decisions `json:"decisions"`
}

// Report combines the submissions for id at commit, as Collect does, and
// reads their decisions, both under one shared lock.
func (s *Store) Report(ctx context.Context, id repo.ID, commit string) (*collect.Result, Decisions, error) {
	if !commitPattern.MatchString(commit) {
		return nil, nil, fmt.Errorf("%q isn't a full commit", commit)
	}
	dir := s.Dir(id, commit)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		result, err := assemble(ctx, id, commit, []File{})
		return result, Decisions{}, err
	}
	unlock, err := lockfile.Shared(ctx, filepath.Join(dir, lockName))
	if err != nil {
		return nil, nil, fmt.Errorf("locking %s: %w", dir, err)
	}
	defer unlock()
	result, err := collectDir(ctx, id, commit, dir)
	if err != nil {
		return nil, nil, err
	}
	decisions, err := readDecisions(dir)
	if err != nil {
		return nil, nil, err
	}
	return result, decisions, nil
}

// Decide lets change edit the decisions for id at commit, given the
// combined report, and stores them if it returns nil. It holds the
// directory's exclusive lock throughout, so the findings change can see
// are the ones its decisions are stored against.
func (s *Store) Decide(ctx context.Context, id repo.ID, commit string, change func(*collect.Result, Decisions) error) error {
	if !commitPattern.MatchString(commit) {
		return fmt.Errorf("%q isn't a full commit", commit)
	}
	dir := s.Dir(id, commit)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		// Nothing was submitted, so there's nothing to decide on, and
		// nothing to store.
		result, err := assemble(ctx, id, commit, []File{})
		if err != nil {
			return err
		}
		return change(result, Decisions{})
	}
	unlock, err := lockfile.Exclusive(ctx, filepath.Join(dir, lockName))
	if err != nil {
		return fmt.Errorf("locking %s: %w", dir, err)
	}
	defer unlock()
	result, err := collectDir(ctx, id, commit, dir)
	if err != nil {
		return err
	}
	decisions, err := readDecisions(dir)
	if err != nil {
		return err
	}
	if err := change(result, decisions); err != nil {
		return err
	}
	return writeDecisions(dir, decisions)
}

// dropDecisions removes the decisions on profile's findings, which a
// resubmission has superseded: the same id in the new submission can be a
// different finding. The caller holds dir's exclusive lock.
func dropDecisions(dir, profile string) error {
	decisions, err := readDecisions(dir)
	if err != nil {
		return err
	}
	dropped := false
	for id := range decisions {
		if strings.HasPrefix(id, profile+":") {
			delete(decisions, id)
			dropped = true
		}
	}
	if !dropped {
		return nil
	}
	return writeDecisions(dir, decisions)
}

// readDecisions reads dir's decisions.json; a missing file is no
// decisions.
func readDecisions(dir string) (Decisions, error) {
	path := filepath.Join(dir, decisionsName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Decisions{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var file decisionsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if file.Version != decisionsVersion {
		return nil, fmt.Errorf("reading %s: version %d isn't supported, only %d", path, file.Version, decisionsVersion)
	}
	if file.Decisions == nil {
		file.Decisions = Decisions{}
	}
	return file.Decisions, nil
}

func writeDecisions(dir string, decisions Decisions) error {
	path := filepath.Join(dir, decisionsName)
	data, err := json.MarshalIndent(decisionsFile{Version: decisionsVersion, Decisions: decisions}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := writeFile(path, append(data, '\n')); err != nil {
		return fmt.Errorf("storing %s: %w", path, err)
	}
	return nil
}
