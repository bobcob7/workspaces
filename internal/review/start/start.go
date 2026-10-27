// Package start decides what a review runs before ws review start starts
// it: which profiles, with which models, and the Claude Code subagent that
// reviews as each (ADR 0038). It runs nothing; the caller lists the files
// and starts the session.
package start

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/review/profile"
	"github.com/bobcob7/workspaces/internal/review/record"
)

// subagentPrefix starts each profile's subagent name: review-<profile>.
const subagentPrefix = "review-"

var (
	// ErrUnknownProfile means a flag or config.toml names a profile that
	// doesn't exist.
	ErrUnknownProfile = errors.New("unknown profile")
	// ErrInvalidChoice means a --profile or --auto value is malformed, a
	// profile is chosen twice, or an --auto profile has no patterns.
	ErrInvalidChoice = errors.New("invalid profile choice")
	// ErrNoProfiles means nothing was left to review with.
	ErrNoProfiles = errors.New("no profiles to review with")
)

// subagentTools are the tools every reviewer subagent gets: enough to
// read the code and run ws review and git, nothing that edits.
func subagentTools() []string {
	return []string{"Read", "Grep", "Glob", "Bash"}
}

// Choice is a --profile or --auto value: a profile, and a model or empty.
type Choice struct {
	Name  string
	Model string
}

// ParseChoice reads NAME or NAME:MODEL.
func ParseChoice(s string) (Choice, error) {
	name, model, hasModel := strings.Cut(s, ":")
	if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return Choice{}, fmt.Errorf("%w: %q: a profile name is lowercase letters, digits, and -", ErrInvalidChoice, s)
	}
	if hasModel && model == "" {
		return Choice{}, fmt.Errorf("%w: %q has an empty model; leave out the colon for the session's model", ErrInvalidChoice, s)
	}
	return Choice{Name: name, Model: model}, nil
}

// Request is everything Choose chooses from.
type Request struct {
	// Profiles are every profile, built-in and the user's.
	Profiles []profile.Profile
	// Config is config.toml's [review.profiles.<name>].
	Config map[string]config.ReviewProfile
	// Explicit are --profile's choices and Auto --auto's. Given either,
	// they replace the configured set.
	Explicit []Choice
	Auto     []Choice
	// Files are the files the review covers, which auto profiles match.
	Files []string
}

// Choose picks the review's profiles (ADR 0038). Without flags, it takes
// each profile's when, as config.toml overrides it: always profiles join,
// auto ones join if one of Files matches, off ones don't. With flags,
// --profile ones join and --auto ones join if one of Files matches. A
// profile's model is its flag's, else config.toml's, else empty for the
// session's.
func Choose(req *Request) ([]record.Profile, error) {
	byName := map[string]*profile.Profile{}
	for i := range req.Profiles {
		byName[req.Profiles[i].Name] = &req.Profiles[i]
	}
	for name := range req.Config {
		if byName[name] == nil {
			return nil, fmt.Errorf("%w: config.toml has [review.profiles.%s], but there's no profile %q", ErrUnknownProfile, name, name)
		}
	}
	var chosen []record.Profile
	var err error
	if len(req.Explicit)+len(req.Auto) > 0 {
		chosen, err = chooseFlags(req, byName)
	} else {
		chosen = chooseConfigured(req)
	}
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("%w: every profile is off or matched no file; choose some with --profile", ErrNoProfiles)
	}
	return chosen, nil
}

// chooseConfigured applies each profile's when, in name order.
func chooseConfigured(req *Request) []record.Profile {
	chosen := []record.Profile{}
	for i := range req.Profiles {
		p := &req.Profiles[i]
		settings := req.Config[p.Name]
		when := p.When
		if settings.When != "" {
			when = profile.When(settings.When)
		}
		switch when {
		case profile.WhenAlways:
			chosen = append(chosen, record.Profile{Name: p.Name, Model: settings.Model})
		case profile.WhenAuto:
			if matchesAny(p, req.Files) {
				chosen = append(chosen, record.Profile{Name: p.Name, Model: settings.Model, Auto: true})
			}
		case profile.WhenOff:
		}
	}
	return chosen
}

// chooseFlags applies --profile and --auto, in the order given.
func chooseFlags(req *Request, byName map[string]*profile.Profile) ([]record.Profile, error) {
	chosen := []record.Profile{}
	seen := map[string]bool{}
	add := func(c Choice, auto bool) error {
		p := byName[c.Name]
		if p == nil {
			return fmt.Errorf("%w: no profile named %q; ws review profiles lists them", ErrUnknownProfile, c.Name)
		}
		if seen[c.Name] {
			return fmt.Errorf("%w: profile %q is chosen twice", ErrInvalidChoice, c.Name)
		}
		seen[c.Name] = true
		if auto && len(p.Match) == 0 {
			return fmt.Errorf("%w: --auto %s: the profile has no match patterns, so it could never join; use --profile", ErrInvalidChoice, c.Name)
		}
		if auto && !matchesAny(p, req.Files) {
			return nil
		}
		model := c.Model
		if model == "" {
			model = req.Config[c.Name].Model
		}
		chosen = append(chosen, record.Profile{Name: c.Name, Model: model, Auto: auto})
		return nil
	}
	for _, c := range req.Explicit {
		if err := add(c, false); err != nil {
			return nil, err
		}
	}
	for _, c := range req.Auto {
		if err := add(c, true); err != nil {
			return nil, err
		}
	}
	return chosen, nil
}

func matchesAny(p *profile.Profile, files []string) bool {
	return slices.ContainsFunc(files, p.Matches)
}

// SubagentName is the subagent of the profile named name:
// review-<name>.
func SubagentName(name string) string {
	return subagentPrefix + name
}

// Subagent is one profile's reviewer: its record entry and its prompt,
// ws review prime --profile's guide followed by the task.
type Subagent struct {
	Profile record.Profile
	Prompt  string
}

// agentJSON is one entry of claude's --agents object.
type agentJSON struct {
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Tools       []string `json:"tools"`
	Model       string   `json:"model,omitempty"`
}

// Agents is the --agents JSON: one review-<profile> entry per subagent,
// with model only when the profile has one (ADR 0038).
func Agents(subagents []Subagent) (string, error) {
	agents := make(map[string]agentJSON, len(subagents))
	for _, s := range subagents {
		agents[SubagentName(s.Profile.Name)] = agentJSON{
			Description: fmt.Sprintf("Reviews this commit as the %s profile and submits with ws review submit", s.Profile.Name),
			Prompt:      s.Prompt,
			Tools:       subagentTools(),
			Model:       s.Profile.Model,
		}
	}
	data, err := json.Marshal(agents)
	if err != nil {
		return "", fmt.Errorf("encoding the review's subagents: %w", err)
	}
	return string(data), nil
}

// maxNameLen is how long a --name may be before ws cuts it to add the
// id's random suffix (ADRs 0012, 0036).
const maxNameLen = 55

// shortCommitLen is how much of the commit a review's name carries.
const shortCommitLen = 7

// Name is a review workspace's --name (ADR 0032):
// review-<repo>-<number>-<sha7>, or review-<repo>-<sha7> without a pull
// request (number 0). The repo name is sanitized as ws sanitizes --name,
// and shortened so ws never cuts off the number or commit.
func Name(repoName string, number int, commit string) string {
	tail := commit[:min(shortCommitLen, len(commit))]
	if number > 0 {
		tail = fmt.Sprintf("%d-%s", number, tail)
	}
	name := sanitize(repoName)
	if room := maxNameLen - len(subagentPrefix) - 1 - len(tail); len(name) > room {
		name = strings.TrimRight(name[:room], "-")
	}
	return subagentPrefix + name + "-" + tail
}

// sanitize lowercases name and turns every run of other characters than
// ASCII letters and digits into one "-", as ws does for --name. Repo names
// are ASCII, so its accent folding never applies.
func sanitize(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	return strings.TrimRight(b.String(), "-")
}
