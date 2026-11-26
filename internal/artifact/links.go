package artifact

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"
)

// Conversation actions (ADR 0084). Read and cited are stored in links.json;
// the others are derived from the record and the threads.
const (
	ActionCreated   = "created"
	ActionRevised   = "revised"
	ActionCommented = "commented"
	ActionResolved  = "resolved"
	ActionRead      = "read"
	ActionCited     = "cited"
)

const (
	linksFile = "links.json"
	// MaxLinks is how many entries links.json keeps; past it the entry with
	// the oldest last_at is dropped.
	MaxLinks = 500
	// LinkLockWait bounds the wait for an artifact's lock when recording a
	// link, so a busy artifact never holds up the command.
	LinkLockWait = 2 * time.Second
)

// actionOrder is the order actions are listed in.
func actionOrder() []string {
	return []string{ActionCreated, ActionRevised, ActionCommented, ActionResolved, ActionRead, ActionCited}
}

// Link is one stored entry of links.json: a workspace's reads, or its
// citations, of one revision (Rev nil: no revision named).
type Link struct {
	Workspace   string    `json:"workspace"`
	Name        *string   `json:"name"`
	Root        *string   `json:"root"`
	Environment *string   `json:"environment"`
	Action      string    `json:"action"`
	Rev         *int      `json:"rev"`
	FirstAt     time.Time `json:"first_at"`
	LastAt      time.Time `json:"last_at"`
	Count       int       `json:"count"`
}

// linksDocument is links.json.
type linksDocument struct {
	Version int    `json:"version"`
	Links   []Link `json:"links"`
}

// NewLink is one read or citation to record. Rev 0 is no revision; a zero
// At is now, and an At after now is now.
type NewLink struct {
	Workspace   string
	Name        string
	Root        string
	Environment string
	Action      string
	Rev         int
	At          time.Time
}

// Conversation is one workspace's part in an artifact: what it did, which
// revisions, and when (ADR 0084).
type Conversation struct {
	Workspace   string
	Name        *string
	Environment *string
	Actions     []string
	Revs        []int
	FirstAt     time.Time
	LastAt      time.Time
}

// RecordLinks adds links to artifact id under its lock, waiting at most
// LinkLockWait. Each one updates the entry for its workspace, action and
// revision, or adds one.
func (s *Store) RecordLinks(ctx context.Context, id string, links ...NewLink) error {
	for _, l := range links {
		if l.Workspace == "" || (l.Action != ActionRead && l.Action != ActionCited) {
			return fmt.Errorf("%w: a link needs a workspace and the action read or cited", ErrInvalid)
		}
	}
	dir, err := s.dirOf(id)
	if err != nil {
		return err
	}
	if err = s.prepare(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.linkLockWait)
	defer cancel()
	unlock, err := lockArtifact(ctx, dir)
	if err != nil {
		return fmt.Errorf("recording a link on %s: %w", id, err)
	}
	defer unlock()
	stored, err := readLinks(dir)
	if errors.Is(err, ErrNewer) {
		return err
	}
	now := s.now().UTC().Truncate(time.Second)
	for _, l := range links {
		stored = addLink(stored, &l, now)
	}
	return writeLinks(s.tmpDir(), dir, capLinks(stored))
}

// linksOrWarning is the links in dir. A links.json that can't be used is
// no links and a warning: it is never needed to read the artifact.
func linksOrWarning(dir, id string) ([]Link, []string) {
	links, err := readLinks(dir)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: links ignored: %v", id, err)}
	}
	return links, nil
}

// addLink folds n into links: the matching entry's count and last_at, or a
// new entry.
func addLink(links []Link, n *NewLink, now time.Time) []Link {
	at := n.At.UTC().Truncate(time.Second)
	if at.IsZero() || at.After(now) {
		at = now
	}
	for i := range links {
		l := &links[i]
		if l.Workspace != n.Workspace || l.Action != n.Action || revOf(l.Rev) != n.Rev {
			continue
		}
		l.Count++
		l.LastAt = laterOf(l.LastAt, at)
		l.FirstAt = earlierOf(l.FirstAt, at)
		l.Name = keepOr(l.Name, n.Name)
		l.Root = keepOr(l.Root, n.Root)
		l.Environment = keepOr(l.Environment, n.Environment)
		return links
	}
	return append(links, Link{
		Workspace: n.Workspace, Name: optionalString(n.Name), Root: optionalString(n.Root),
		Environment: optionalString(n.Environment), Action: n.Action, Rev: optionalInt(n.Rev),
		FirstAt: at, LastAt: at, Count: 1,
	})
}

// capLinks keeps the MaxLinks entries with the latest last_at.
func capLinks(links []Link) []Link {
	if len(links) <= MaxLinks {
		return links
	}
	sorted := slices.Clone(links)
	slices.SortStableFunc(sorted, func(a, b Link) int { return b.LastAt.Compare(a.LastAt) })
	return sorted[:MaxLinks]
}

// Conversations merges what rec and threads record and the stored links into
// one row per workspace, latest first (ties by workspace). People are not
// conversations, so only agents' actions count.
func Conversations(rec *Record, threads []Thread, links []Link) []Conversation {
	rows := make(map[string]*Conversation)
	for _, e := range derivedEvents(rec, threads) {
		addEvent(rows, &e)
	}
	for i := range links {
		l := &links[i]
		addEvent(rows, &event{workspace: l.Workspace, name: l.Name, environment: l.Environment, action: l.Action, rev: revOf(l.Rev), first: l.FirstAt, last: l.LastAt})
	}
	out := make([]Conversation, 0, len(rows))
	for _, c := range rows {
		c.Actions = ordered(c.Actions)
		slices.Sort(c.Revs)
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b Conversation) int {
		if c := b.LastAt.Compare(a.LastAt); c != 0 {
			return c
		}
		return cmp.Compare(a.Workspace, b.Workspace)
	})
	return out
}

// event is one thing a workspace did with an artifact.
type event struct {
	workspace   string
	name        *string
	environment *string
	action      string
	rev         int
	first, last time.Time
}

// derivedEvents is what the record and threads say agents did.
func derivedEvents(rec *Record, threads []Thread) []event {
	var events []event
	for _, r := range rec.Revisions {
		action := ActionRevised
		if r.Rev == 1 {
			action = ActionCreated
		}
		events = appendActor(events, r.CreatedBy, action, r.Rev, r.CreatedAt)
	}
	for i := range threads {
		t := &threads[i]
		for _, c := range t.Comments {
			events = appendActor(events, c.Author, ActionCommented, 0, c.CreatedAt)
		}
		if t.ResolvedBy != nil && t.ResolvedAt != nil {
			events = appendActor(events, *t.ResolvedBy, ActionResolved, 0, *t.ResolvedAt)
		}
	}
	return events
}

func appendActor(events []event, a Actor, action string, rev int, at time.Time) []event {
	if a.Kind != "agent" || a.Workspace == nil || *a.Workspace == "" {
		return events
	}
	return append(events, event{workspace: *a.Workspace, name: a.Name, action: action, rev: rev, first: at, last: at})
}

// addEvent folds e into its workspace's row.
func addEvent(rows map[string]*Conversation, e *event) {
	c, ok := rows[e.workspace]
	if !ok {
		c = &Conversation{Workspace: e.workspace, FirstAt: e.first, LastAt: e.last}
		rows[e.workspace] = c
	}
	c.Name = keepPointer(c.Name, e.name, !e.last.Before(c.LastAt))
	c.Environment = keepPointer(c.Environment, e.environment, false)
	c.FirstAt = earlierOf(c.FirstAt, e.first)
	c.LastAt = laterOf(c.LastAt, e.last)
	if !slices.Contains(c.Actions, e.action) {
		c.Actions = append(c.Actions, e.action)
	}
	if e.rev > 0 && !slices.Contains(c.Revs, e.rev) {
		c.Revs = append(c.Revs, e.rev)
	}
}

// ordered is actions in actionOrder.
func ordered(actions []string) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actionOrder() {
		if slices.Contains(actions, a) {
			out = append(out, a)
		}
	}
	return out
}

// keepPointer is next when it's set and either current isn't or prefer is
// true; else current.
func keepPointer(current, next *string, prefer bool) *string {
	if next == nil || *next == "" {
		return current
	}
	if current == nil || prefer {
		return next
	}
	return current
}

// keepOr is s when set, else current.
func keepOr(current *string, s string) *string {
	if s == "" {
		return current
	}
	return &s
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func earlierOf(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}

func revOf(rev *int) int {
	if rev == nil {
		return 0
	}
	return *rev
}

func optionalInt(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

// readLinks reads <dir>/links.json; a missing file is no links. Bad JSON is
// ErrInvalid and a newer version ErrNewer, both wrapped with the path.
func readLinks(dir string) ([]Link, error) {
	path := filepath.Join(dir, linksFile)
	data, err := readFileIfExists(path)
	if err != nil || data == nil {
		return nil, err
	}
	var doc linksDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("reading %s: %w: %w", path, ErrInvalid, err)
	}
	if doc.Version > FormatVersion {
		return nil, fmt.Errorf("reading %s: %w", path, ErrNewer)
	}
	return doc.Links, nil
}

// writeLinks writes links.json in dir.
func writeLinks(tmpDir, dir string, links []Link) error {
	data, err := json.Marshal(linksDocument{Version: FormatVersion, Links: links})
	if err != nil {
		return fmt.Errorf("encoding %s: %w", linksFile, err)
	}
	return writeAtomic(tmpDir, filepath.Join(dir, linksFile), data)
}
