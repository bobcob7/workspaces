package envproto

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Operation names of ADR 0080's review rows.
const (
	OpReviewStart    = "review.start"
	OpReviewStatus   = "review.status"
	OpReviewList     = "review.list"
	OpReviewReport   = "review.report"
	OpReviewDecide   = "review.decide"
	OpReviewActed    = "review.acted"
	OpReviewProfiles = "review.profiles"
)

// Bounds of review and loop results and arguments (ADR 0080).
const (
	MaxPageBytes     = 192 << 10 // a paged result's JSON
	MaxFindings      = 200       // finding ids in one decide or acted
	DefaultListLimit = 100
	MaxListLimit     = 200
	MaxFocusBytes    = MaxMessageBytes
	maxProfiles      = 16
)

// Report sections a page reads.
const (
	SectionComments   = "comments"
	SectionSuperseded = "superseded"
)

// Decisions of review.decide.
const (
	DecideComment = "comment"
	DecideFix     = "fix"
	DecideSkip    = "skip"
	DecideClear   = "clear"
)

// ReviewStartArgs are review.start's arguments. Exactly one of PR and Head
// is set: PR for a GitHub pull request (ws reads it with gh on the agent),
// Head for a commit (a Forgejo pull request's head, ADR 0060).
type ReviewStartArgs struct {
	Repo           string   `json:"repo"`            // a spec without @ (repo.ParseSpec on the agent)
	PR             *int     `json:"pr"`              // 1..2^31-1
	Head           *string  `json:"head"`            // 40 lowercase hex
	Name           *string  `json:"name"`            // validateName
	Profiles       []string `json:"profiles"`        // "NAME[:MODEL]", [] for ws's choice
	Auto           []string `json:"auto"`            // "NAME[:MODEL]"
	Model          *string  `json:"model"`           // modelPattern
	PermissionMode *string  `json:"permission_mode"` // one of ReviewPermissionModes
	Focus          *string  `json:"focus"`           // 1..MaxFocusBytes, not starting with "-"
}

// ReviewRef is the argument of review.status: one review by id.
type ReviewRef struct {
	Review string `json:"review"` // ValidReviewID
}

// ReviewListArgs are review.list's arguments.
type ReviewListArgs struct {
	Repo  *string `json:"repo"`  // a cache id host/owner/name, or null for all
	PR    *int    `json:"pr"`    // needs Repo
	Limit int     `json:"limit"` // 0: DefaultListLimit; at most MaxListLimit
}

// ReviewReportArgs are review.report's arguments: one page of one section.
type ReviewReportArgs struct {
	Review  string `json:"review"`
	Section string `json:"section"` // SectionComments or SectionSuperseded
	Offset  int    `json:"offset"`  // >= 0
}

// ReviewDecideArgs are review.decide's arguments.
type ReviewDecideArgs struct {
	Review     string   `json:"review"`
	Findings   []string `json:"findings"`   // 1..MaxFindings ValidFindingID
	Decision   string   `json:"decision"`   // DecideComment, DecideFix, DecideSkip, DecideClear
	Suggestion *int     `json:"suggestion"` // fix only, >= 0, counting from 0 like ws review decide
	Note       *string  `json:"note"`       // 1..MaxMessageBytes; not with clear
}

// ReviewActedArgs are review.acted's arguments; exactly one of Commented and FixSent.
type ReviewActedArgs struct {
	Review    string   `json:"review"`
	Findings  []string `json:"findings"`
	Commented *string  `json:"commented"` // an https:// URL, <= 2048 bytes
	FixSent   *string  `json:"fix_sent"`  // the fix agent's workspace id on the controller, validateName
}

// ReviewProfilesArgs are review.profiles's arguments (none).
type ReviewProfilesArgs struct{}

// ReviewStatusResult is review.status's result.
type ReviewStatusResult struct {
	Review   string          `json:"review"`
	Agent    json.RawMessage `json:"agent"`    // ws review list's {"workspace","status"} or null
	Finished bool            `json:"finished"` // as ws review wait decides (reviewFinished)
	Redacted int             `json:"redacted"`
}

var (
	reviewIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	findingIDPattern  = regexp.MustCompile(`^[a-z0-9-]{1,40}:[A-Za-z0-9._-]{1,64}$`)
	profilePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}(:[A-Za-z0-9][A-Za-z0-9._:/-]{0,127})?$`)
	headPattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repoFilterPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+(:\d+)?/[^/\s]+/[^/\s]+$`)
)

// newReviewArgs returns an empty argument struct for a review operation.
func newReviewArgs(op string) (any, bool) {
	switch op {
	case OpReviewStart:
		return &ReviewStartArgs{}, true
	case OpReviewStatus:
		return &ReviewRef{}, true
	case OpReviewList:
		return &ReviewListArgs{}, true
	case OpReviewReport:
		return &ReviewReportArgs{}, true
	case OpReviewDecide:
		return &ReviewDecideArgs{}, true
	case OpReviewActed:
		return &ReviewActedArgs{}, true
	case OpReviewProfiles:
		return &ReviewProfilesArgs{}, true
	}
	return nil, false
}

// validateReviewArgs checks the arguments of a review operation, and refuses any other type.
func validateReviewArgs(args any) error {
	switch a := args.(type) {
	case *ReviewStartArgs:
		return validateReviewStart(a)
	case *ReviewRef:
		return validateReviewID(a.Review)
	case *ReviewListArgs:
		return validateReviewList(a)
	case *ReviewReportArgs:
		return validateReviewReport(a)
	case *ReviewDecideArgs:
		return validateReviewDecide(a)
	case *ReviewActedArgs:
		return validateReviewActed(a)
	case *ReviewProfilesArgs:
		return nil
	}
	return validateLoopArgs(args)
}

// PermissionModes are the permission modes a review or a loop may ask claude for: the ones its --permission-mode flag accepts (ADR 0051).
func PermissionModes() []string {
	return []string{"default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions", "manual"}
}

// ReviewPermissionModes are the permission modes review.start accepts: PermissionModes without bypassPermissions, which a review reading untrusted pull request content must never run with, and without manual, an alias of default.
func ReviewPermissionModes() []string {
	return []string{"default", "acceptEdits", "plan", "auto", "dontAsk"}
}

// ValidReviewID reports whether id names a review: its workspace id on the environment.
func ValidReviewID(id string) bool {
	return reviewIDPattern.MatchString(id)
}

// ValidFindingID reports whether id names one finding, as profile:id.
func ValidFindingID(id string) bool {
	return findingIDPattern.MatchString(id)
}

func validateReviewStart(a *ReviewStartArgs) error {
	if err := validateSpec(a.Repo); err != nil {
		return err
	}
	if strings.Contains(a.Repo, "@") {
		return &ArgsError{Code: "spec_invalid", Message: "a review repo is a spec without @"}
	}
	if err := validateReviewTarget(a.PR, a.Head); err != nil {
		return err
	}
	if a.Name != nil {
		if err := validateName(*a.Name); err != nil {
			return err
		}
	}
	if err := validateProfiles(a.Profiles); err != nil {
		return err
	}
	if err := validateProfiles(a.Auto); err != nil {
		return err
	}
	if a.Model != nil && !modelPattern.MatchString(*a.Model) {
		return usageError("model " + strconv.Quote(*a.Model) + " is not a valid model name")
	}
	if a.PermissionMode != nil && !slices.Contains(ReviewPermissionModes(), *a.PermissionMode) {
		return usageError("permission mode " + strconv.Quote(*a.PermissionMode) + " is not one of " + strings.Join(ReviewPermissionModes(), ", "))
	}
	if a.Focus != nil {
		return validateFocus(*a.Focus)
	}
	return nil
}

func validateReviewTarget(pr *int, head *string) error {
	if (pr == nil) == (head == nil) {
		return usageError("a review needs exactly one of pr and head")
	}
	if pr != nil && !validPullRequest(*pr) {
		return usageError(fmt.Sprintf("pr must be 1 to %d", math.MaxInt32))
	}
	if head != nil && !headPattern.MatchString(*head) {
		return usageError("head must be 40 lowercase hex characters")
	}
	return nil
}

func validPullRequest(pr int) bool {
	return pr >= 1 && pr <= math.MaxInt32
}

func validateProfiles(profiles []string) error {
	if len(profiles) > maxProfiles {
		return usageError(fmt.Sprintf("at most %d profiles", maxProfiles))
	}
	for _, profile := range profiles {
		if !profilePattern.MatchString(profile) {
			return usageError("profile " + strconv.Quote(profile) + " is not NAME or NAME:MODEL")
		}
	}
	return nil
}

func validateFocus(focus string) error {
	if focus == "" || len(focus) > MaxFocusBytes {
		return usageError(fmt.Sprintf("focus must be 1 to %d bytes", MaxFocusBytes))
	}
	if strings.HasPrefix(strings.TrimLeftFunc(focus, unicode.IsSpace), "-") {
		return usageError("a focus cannot start with a dash, claude would read it as a flag")
	}
	return nil
}

func validateReviewID(id string) error {
	if !ValidReviewID(id) {
		return usageError("review " + strconv.Quote(id) + " is not a review id")
	}
	return nil
}

func validateReviewList(a *ReviewListArgs) error {
	if a.Repo != nil && !repoFilterPattern.MatchString(*a.Repo) {
		return usageError("repo " + strconv.Quote(*a.Repo) + " is not host/owner/name")
	}
	if a.PR != nil && a.Repo == nil {
		return usageError("a pull request filter needs a repo")
	}
	if a.PR != nil && !validPullRequest(*a.PR) {
		return usageError(fmt.Sprintf("pr must be 1 to %d", math.MaxInt32))
	}
	if a.Limit < 0 || a.Limit > MaxListLimit {
		return usageError(fmt.Sprintf("limit must be 0 to %d", MaxListLimit))
	}
	return nil
}

func validateReviewReport(a *ReviewReportArgs) error {
	if err := validateReviewID(a.Review); err != nil {
		return err
	}
	if a.Section != SectionComments && a.Section != SectionSuperseded {
		return usageError("section must be " + SectionComments + " or " + SectionSuperseded)
	}
	if a.Offset < 0 {
		return usageError("offset must be 0 or more")
	}
	return nil
}

func validateReviewDecide(a *ReviewDecideArgs) error {
	if err := validateReviewID(a.Review); err != nil {
		return err
	}
	if err := validateFindings(a.Findings); err != nil {
		return err
	}
	if !slices.Contains([]string{DecideComment, DecideFix, DecideSkip, DecideClear}, a.Decision) {
		return usageError("decision " + strconv.Quote(a.Decision) + " is not comment, fix, skip or clear")
	}
	if a.Suggestion != nil && (a.Decision != DecideFix || *a.Suggestion < 0) {
		return usageError("a suggestion needs fix and a number of 0 or more (counting from 0)")
	}
	if a.Note != nil && (a.Decision == DecideClear || *a.Note == "" || len(*a.Note) > MaxMessageBytes) {
		return usageError(fmt.Sprintf("a note is 1 to %d bytes and not allowed with clear", MaxMessageBytes))
	}
	return nil
}

func validateFindings(findings []string) error {
	if len(findings) == 0 || len(findings) > MaxFindings {
		return usageError(fmt.Sprintf("findings are 1 to %d", MaxFindings))
	}
	for _, finding := range findings {
		if !ValidFindingID(finding) {
			return usageError("finding " + strconv.Quote(finding) + " is not profile:id")
		}
	}
	return nil
}

func validateReviewActed(a *ReviewActedArgs) error {
	if err := validateReviewID(a.Review); err != nil {
		return err
	}
	if err := validateFindings(a.Findings); err != nil {
		return err
	}
	if (a.Commented == nil) == (a.FixSent == nil) {
		return usageError("acted needs exactly one of commented and fix_sent")
	}
	if a.Commented != nil {
		return validateCommentedURL(*a.Commented)
	}
	return validateName(*a.FixSent)
}

func validateCommentedURL(url string) error {
	if !strings.HasPrefix(url, "https://") || len(url) > 2048 || strings.ContainsFunc(url, unicode.IsSpace) || strings.ContainsFunc(url, unicode.IsControl) {
		return usageError("commented must be an https URL of at most 2048 bytes without spaces or control characters")
	}
	return nil
}
