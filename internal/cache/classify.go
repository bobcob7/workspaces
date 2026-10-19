package cache

import (
	"errors"
	"regexp"

	"github.com/bobcob7/workspaces/internal/git"
)

// repoMissingPattern matches what git prints when upstream answered that a
// repo isn't there, measured with git 2.50.1:
//   - "ERROR: Repository not found." (GitHub over SSH) and "remote:
//     Repository not found." (GitHub over HTTPS with credentials)
//   - "remote: Not found." (Codeberg)
//   - "repository '<url>' not found" (an HTTP 404)
//   - "does not appear to be a git repository" (a missing path)
//   - "could not read Username for '<url>': terminal prompts disabled": GitHub
//     and GitLab ask for credentials for a repo that doesn't exist, the same as
//     for a private one, so ws can't tell them apart (ADR 0019).
var repoMissingPattern = regexp.MustCompile(`Repository not found|remote: Not found|repository '[^']*' not found|does not appear to be a git repository|could not read Username for '[^']*': terminal prompts disabled`)

// objectMissingPattern matches upstream refusing a commit it doesn't have:
// "upload-pack: not our ref <hash>", or "couldn't find remote ref" for a
// name.
var objectMissingPattern = regexp.MustCompile(`not our ref|couldn't find remote ref`)

// repoMissing reports whether git failed because upstream says the repo
// doesn't exist. Anything else, such as DNS, a refused connection, TLS, a
// rejected login, or an unknown message, is not "missing".
func repoMissing(err error) bool {
	return stderrMatches(err, repoMissingPattern)
}

// objectMissing reports whether git failed because upstream doesn't have
// the commit or ref asked for.
func objectMissing(err error) bool {
	return stderrMatches(err, objectMissingPattern)
}

func stderrMatches(err error, pattern *regexp.Regexp) bool {
	cmdErr, ok := errors.AsType[*git.CommandError](err)
	return ok && pattern.MatchString(cmdErr.Stderr)
}

// isGitUnusable reports whether err means git itself can't run, which no
// retry against another candidate would fix.
func isGitUnusable(err error) bool {
	return errors.Is(err, git.ErrNotInstalled) || errors.Is(err, git.ErrTooOld)
}
