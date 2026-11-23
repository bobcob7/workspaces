// Package artifact stores files that people and agents share, with their
// revisions and comment threads (ADRs 0078, 0079).
package artifact

import (
	"errors"
	"fmt"
	"time"
)

// Kind is what an artifact holds, decided from its bytes.
type Kind string

const (
	// KindImage is a PNG, JPEG, GIF, WebP or SVG image.
	KindImage Kind = "image"
	// KindMarkdown is a Markdown document.
	KindMarkdown Kind = "markdown"
	// KindText is plain UTF-8 text.
	KindText Kind = "text"
)

// Limits (README.md › Kinds, types and limits).
const (
	MaxImageSize  = 10 << 20
	MaxTextSize   = 1 << 20 // markdown, text and SVG
	MaxRevisions  = 100
	MaxStoreBytes = 2 << 30
	MaxThreads    = 200
	MaxComments   = 50
	MaxNameRunes  = 128
	MaxLineRunes  = 200 // title, revision note
	MaxBodyRunes  = 10_000
	MaxQuoteRunes = 2_000
	ContextRunes  = 32
	FormatVersion = 1
)

// Errors. Each maps to a ws error code (README.md › Errors).
var (
	ErrInvalid  = errors.New("invalid artifact")       // -> artifact_invalid
	ErrNotFound = errors.New("no such artifact")       // -> artifact_unknown
	ErrConflict = errors.New("artifact changed")       // -> artifact_conflict
	ErrFull     = errors.New("artifact limit reached") // -> artifact_full
	ErrNewer    = errors.New("written by a newer ws")  // -> artifact_invalid
)

// ConflictError is ErrConflict with the revisions involved, for details.
type ConflictError struct {
	Base   int
	Latest int
}

// Error names the latest revision and the one the caller based its change on.
func (e *ConflictError) Error() string {
	return fmt.Sprintf("the latest revision is r%d, not r%d", e.Latest, e.Base)
}

// Is reports whether target is ErrConflict.
func (e *ConflictError) Is(target error) bool {
	return target == ErrConflict
}

// Actor is who created a revision or wrote a comment.
type Actor struct {
	Kind      string  `json:"kind"`
	Workspace *string `json:"workspace"`
	Name      *string `json:"name"`
}

// Person is the actor for every people command and the desktop app.
func Person() Actor {
	return Actor{Kind: "person"}
}

// Agent is the actor for a ws machine command run in workspace id.
func Agent(workspace, name string) Actor {
	return Actor{Kind: "agent", Workspace: &workspace, Name: &name}
}

// Revision is one immutable version of an artifact's bytes.
type Revision struct {
	Rev         int       `json:"rev"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   Actor     `json:"created_by"`
	Note        *string   `json:"note"`
}

// Record is artifact.json.
type Record struct {
	Version     int        `json:"version"`
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Title       *string    `json:"title"`
	Kind        Kind       `json:"kind"`
	ContentType string     `json:"content_type"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   Actor      `json:"created_by"`
	Root        *string    `json:"root"`
	Revisions   []Revision `json:"revisions"`
}

// Latest is the newest revision. A stored record always has one.
func (r *Record) Latest() Revision {
	return r.Revisions[len(r.Revisions)-1]
}

// ThreadCounts summarises a record's threads (README.md › Artifact summary).
type ThreadCounts struct {
	Open     int `json:"open"`
	Resolved int `json:"resolved"`
	Detached int `json:"detached"`
}

// Anchor is where a thread points in a revision (ADR 0079). T1 only reads
// and counts anchors; T2 writes them.
type Anchor struct {
	Rev       int    `json:"rev"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Quote     string `json:"quote"`
	Prefix    string `json:"prefix"`
	Suffix    string `json:"suffix"`
	State     string `json:"state"`
}

// Comment is one message in a thread.
type Comment struct {
	ID        string    `json:"id"`
	Author    Actor     `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// Thread is a comment thread, anchored to a passage or to the whole artifact.
type Thread struct {
	ID         string     `json:"id"`
	Anchor     *Anchor    `json:"anchor"`
	Resolved   bool       `json:"resolved"`
	ResolvedAt *time.Time `json:"resolved_at"`
	ResolvedBy *Actor     `json:"resolved_by"`
	Comments   []Comment  `json:"comments"`
}

// Counts summarises threads: open is the unresolved ones, detached those
// whose anchor is detached, open or not.
func Counts(threads []Thread) ThreadCounts {
	var counts ThreadCounts
	for _, t := range threads {
		if t.Resolved {
			counts.Resolved++
		} else {
			counts.Open++
		}
		if t.Anchor != nil && t.Anchor.State == "detached" {
			counts.Detached++
		}
	}
	return counts
}
