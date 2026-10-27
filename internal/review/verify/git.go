package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/git"
)

// GitTimeout bounds one git call. Every call here is local and object-store
// bound, so a slow one means something is wrong rather than something is big,
// and a review tool must not hang a caller's pipeline waiting on it. The
// wait after the kill is bounded by internal/git.
const GitTimeout = 30 * time.Second

// errNotAnswered marks a git call that ended because the deadline passed rather
// than because git decided anything. The context is asked directly rather than
// the process state, so the answer holds on every platform.
var errNotAnswered = errors.New("git did not answer")

// ErrNoRepository means the working directory is not inside a git repository.
// It is exported because it is the one discovery failure a caller may treat as
// ordinary and go on without: every other way discovery fails is this machine
// failing, and skipping the anchor checks for those would pass a document
// nobody ever checked.
var ErrNoRepository = errors.New("not a git repository")

// Runner runs one git command in a directory. *git.Runner is the one
// implementation: git runs only through internal/git (ADR 0003).
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// gitError is a git call that failed, carrying what git said on stderr. The
// status alone is not enough to tell a refusal from a complaint: cat-file -e
// exits 1 both for an object that is absent and for one it could not look for,
// and only the silence of stderr separates them.
type gitError struct {
	args   []string
	stderr string
	err    error
}

// Error keeps only the sentence git led with. The whole message can run to
// several lines, and this one ends up in a Reason a caller reads as one string;
// the rest is advice for a person and is already on stderr.
func (e *gitError) Error() string {
	cause := e.err
	if failure, ok := errors.AsType[*git.CommandError](e.err); ok {
		cause = failure.Err
	}
	if e.stderr == "" {
		return fmt.Sprintf("git %s: %v", strings.Join(e.args, " "), cause)
	}
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), cause, firstLine(e.stderr))
}

func (e *gitError) Unwrap() error { return e.err }

// stderrOf returns what git wrote to stderr before failing, or "" when it wrote
// nothing. An empty result is a claim about git's silence, so anything that is
// not a recognised git failure reports empty rather than guessing.
func stderrOf(err error) string {
	if failure, ok := errors.AsType[*gitError](err); ok {
		return failure.stderr
	}
	if failure, ok := errors.AsType[*git.CommandError](err); ok {
		return strings.TrimSpace(failure.Stderr)
	}
	return ""
}

// Repository is a git repository discovered from the working directory.
type Repository struct {
	git  Runner
	root string
}

// Discover finds the repository containing dir the way git itself does, by
// walking up until a repository root is found. It never touches the network.
func Discover(ctx context.Context, runner Runner, dir string) (*Repository, error) {
	ctx, cancel := context.WithTimeout(ctx, GitTimeout)
	defer cancel()
	out, err := runner.Run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: running git: %w", errNotAnswered, err)
		}
		return nil, discoveryFailure(err)
	}
	return &Repository{git: runner, root: strings.TrimSpace(string(out))}, nil
}

// discoveryFailure separates the one answer that means there is no repository
// here from every other way rev-parse can fail. Git exits 128 both for a plain
// directory and for a checkout it refuses to touch — dubious ownership, a bare
// repository — so the exit status alone cannot tell them apart, and reading a
// refusal as an absent repository skips every anchor check and passes the
// document while standing inside the repository that would have refuted it.
func discoveryFailure(err error) error {
	if _, exited := exitStatus(err); !exited {
		return fmt.Errorf("running git: %w", err)
	}
	// Matching git's own sentence is the only signal there is; the exit status
	// is 128 for both. Note the one case it cannot separate: a .git that exists
	// but cannot be read produces this identical message, so an unreadable
	// checkout reports absence. That mis-states the reason but not the outcome,
	// since neither verifies anything.
	refusal := firstLine(stderrOf(err))
	if strings.Contains(refusal, "not a git repository") {
		return ErrNoRepository
	}
	if refusal == "" {
		return fmt.Errorf("git could not identify a repository here: %w", err)
	}
	return fmt.Errorf("git could not identify a repository here: %s", refusal)
}

// PlainEnv gives git an environment whose answers mean what they say. The
// review's git.Runner is built with it.
//
// LC_ALL and LANGUAGE pin the untranslated messages: discovery decides whether
// a directory is a repository by reading what git said, and a German checkout
// would otherwise answer in a sentence no match here recognises.
//
// GIT_NO_LAZY_FETCH stops a partial clone reaching for the network when it is
// asked about an object it does not have. Absence is read from git's silence,
// and a lazy fetch turns the plainest absence there is into a fatal error about
// a remote — which would be read as "could not look" and skipped, letting a
// review name a commit that exists nowhere and still pass.
//
// The trace variables are dropped because they write to the same stderr the
// silence is read from. Inheriting one from the caller's shell would make every
// absent object look unreadable.
func PlainEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+3)
	for _, entry := range environ {
		if strings.HasPrefix(entry, "GIT_TRACE") || strings.HasPrefix(entry, "GIT_CURL_VERBOSE") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "LC_ALL=C", "LANGUAGE=", "GIT_NO_LAZY_FETCH=1")
}

// complained reports whether git raised a problem, as opposed to saying nothing
// or merely warning. The two prefixes are git's own severity markers and are
// not translated, so this holds wherever it runs. It is the backstop under
// PlainEnv: silence carries the claim, and anything git calls an error or a
// fatal withdraws it, whatever else may have reached stderr.
func complained(stderr string) bool {
	for line := range strings.SplitSeq(stderr, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "error:") || strings.HasPrefix(line, "fatal:") {
			return true
		}
	}
	return false
}

// firstLine keeps the sentence git leads with. Its later lines are advice for a
// person, and this one is going into a machine-readable reason.
func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if before, _, found := strings.Cut(text, "\n"); found {
		return strings.TrimSpace(before)
	}
	return text
}

// exitStatus returns the status git exited with, and false when it never
// exited at all: a git that was never found, or one killed by the deadline or
// a signal, which internal/git reports with exit code -1 and no opinion
// whatsoever about the repository.
func exitStatus(err error) (int, bool) {
	if errors.Is(err, errNotAnswered) {
		return 0, false
	}
	failure, ok := errors.AsType[*git.CommandError](err)
	if !ok || failure.ExitCode < 0 {
		return 0, false
	}
	return failure.ExitCode, true
}

// Root is the absolute path of the repository root.
func (r *Repository) Root() string {
	return r.root
}

func (r *Repository) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, GitTimeout)
	defer cancel()
	out, err := r.git.Run(ctx, r.root, args...)
	if err != nil {
		failure := &gitError{args: args, stderr: stderrOf(err), err: err}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %w", errNotAnswered, failure)
		}
		return nil, failure
	}
	return out, nil
}

// worktreeDiverged reports whether path's working-tree copy exists and
// differs from the blob ref names, using git's own comparison — git
// hash-object on the worktree file against git rev-parse on the blob at
// ref — so .gitattributes, core.autocrlf, and any clean/smudge filter are
// honored exactly as they are everywhere else in the user's tooling. A raw
// byte comparison would invent a second definition of "changed" that
// disagrees with git's own: under core.autocrlf=true it would see line-ending
// normalization as a difference and call every tracked file diverged.
//
// A path with no working-tree copy is reported not diverged, never as an
// error: a deleted file says nothing about what a reviewer read, so ref
// stays authoritative for it. This also keeps the answer disjoint from
// "changed" — git itself reports a deletion as a difference, and this
// function deliberately does not.
//
// It does not check that ref is HEAD; that restriction belongs to whoever
// decides when the comparison is meaningful, not to the comparison itself.
func (r *Repository) worktreeDiverged(ctx context.Context, ref, path string) (bool, error) {
	_, err := os.Lstat(filepath.Join(r.root, filepath.FromSlash(path)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("statting working-tree copy of %s: %w", path, err)
	}
	blob, err := r.run(ctx, "rev-parse", "--verify", ref+":"+path)
	if err != nil {
		return false, fmt.Errorf("resolving %s at %s: %w", path, short(ref), err)
	}
	worktree, err := r.run(ctx, "hash-object", "--", path)
	if err != nil {
		return false, fmt.Errorf("hashing working-tree copy of %s: %w", path, err)
	}
	return strings.TrimSpace(string(blob)) != strings.TrimSpace(string(worktree)), nil
}

// worktreeExists reports whether path has a working-tree copy at all,
// independent of any ref. It answers a narrower question than
// worktreeDiverged: not whether the copy differs from what a ref names, only
// whether the file is on disk — which is what separates a path that was
// never committed from one that was never real, when an anchor points
// somewhere no ref has ever heard of (refinery-qu7). It never touches git:
// an untracked file has no object to compare against, so the filesystem is
// the only source that can answer this.
func (r *Repository) worktreeExists(path string) (bool, error) {
	_, err := os.Lstat(filepath.Join(r.root, filepath.FromSlash(path)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("statting working-tree copy of %s: %w", path, err)
	}
	return true, nil
}
