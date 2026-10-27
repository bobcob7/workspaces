package validate

import (
	"context"
	"log/slog"

	"github.com/bobcob7/workspaces/internal/review/verify"
)

// GitFinder discovers the git repository containing the working directory.
type GitFinder struct {
	git verify.Runner
	log *slog.Logger
}

// NewGitFinder returns a finder that walks up from the working directory,
// running git through git, normally a *git.Runner built with
// verify.PlainEnv.
func NewGitFinder(git verify.Runner, log *slog.Logger) *GitFinder {
	return &GitFinder{git: git, log: log}
}

// Find returns a verifier for the repository containing dir.
func (f *GitFinder) Find(ctx context.Context, dir string) (verifier, error) { //nolint:revive // unexported-return: it satisfies repositoryFinder, which Validator alone consumes.
	repository, err := verify.Discover(ctx, f.git, dir)
	if err != nil {
		return nil, err
	}
	f.log.DebugContext(ctx, "repository discovered", "root", repository.Root())
	return verify.New(repository, f.log), nil
}
