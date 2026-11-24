package cli

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strconv"
	"unicode/utf8"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/redact"
	"github.com/bobcob7/workspaces/internal/repo"
)

// resultSlack is kept free of MaxRequestBytes for the reply's own envelope.
const resultSlack = 4 << 10

// truncatedFieldCap is the first length an oversized comment's text is cut to.
const truncatedFieldCap = 64 << 10

// runReview runs a review.* operation with ws review's own command. Every
// result and every error goes through the redactor before it leaves. The
// review.start rate limit is applied by the agent's limiter before this runs.
func (o *agentOperations) runReview(ctx context.Context, op string, args any) (json.RawMessage, error) {
	out, err := o.dispatchReview(ctx, op, args)
	if err != nil {
		return nil, o.redactError(replyError(err))
	}
	return out, nil
}

// dispatchReview runs the review.* operation op and finishes its result.
func (o *agentOperations) dispatchReview(ctx context.Context, op string, args any) (json.RawMessage, error) {
	root := o.app.paths.Root
	switch a := args.(type) {
	case *envproto.ReviewStartArgs:
		if _, err := repo.ParseSpec(reviewSpec(a)); err != nil {
			return nil, &envproto.ReplyError{Code: string(codeSpecInvalid), Message: err.Error()}
		}
		return o.runReviewCLI(ctx, reviewStartArgv(root, a))
	case *envproto.ReviewRef:
		return o.reviewStatus(ctx, a.Review)
	case *envproto.ReviewListArgs:
		return o.reviewList(ctx, a)
	case *envproto.ReviewReportArgs:
		return o.reviewReport(ctx, a)
	case *envproto.ReviewDecideArgs:
		return o.runReviewCLI(ctx, reviewDecideArgv(root, a))
	case *envproto.ReviewActedArgs:
		return o.runReviewCLI(ctx, reviewActedArgv(root, a))
	case *envproto.ReviewProfilesArgs:
		return o.runReviewCLI(ctx, append(cliPrefix(root), "review", "profiles"))
	}
	return nil, &envproto.ReplyError{Code: string(codeUsage), Message: "unknown operation " + op}
}

// runReviewCLI runs argv and finishes its stdout object.
func (o *agentOperations) runReviewCLI(ctx context.Context, argv []string) (json.RawMessage, error) {
	out, err := o.runCLI(ctx, argv)
	if err != nil {
		return nil, err
	}
	return o.finishResult(out)
}

// reviewSpec is the spec ws review start gets: repo, or repo@head.
func reviewSpec(args *envproto.ReviewStartArgs) string {
	if args.Head != nil {
		return args.Repo + "@" + *args.Head
	}
	return args.Repo
}

// reviewStartArgv is the ws review start command line for args:
// --json --config-dir <root> review start --requester none [--name N]
// [--pr N] [--profile P]... [--auto P]... <repo>[@<head>]
// [-- [--model=M] [--permission-mode=P] [<focus>]].
func reviewStartArgv(root string, args *envproto.ReviewStartArgs) []string {
	argv := append(cliPrefix(root), "review", "start", "--requester", requesterNone)
	if args.Name != nil {
		argv = append(argv, "--name", *args.Name)
	}
	if args.PR != nil {
		argv = append(argv, "--pr", strconv.Itoa(*args.PR))
	}
	for _, p := range args.Profiles {
		argv = append(argv, "--profile", p)
	}
	for _, p := range args.Auto {
		argv = append(argv, "--auto", p)
	}
	argv = append(argv, reviewSpec(args))
	var after []string
	if args.Model != nil {
		after = append(after, "--model="+*args.Model)
	}
	if args.PermissionMode != nil {
		after = append(after, "--permission-mode="+*args.PermissionMode)
	}
	if args.Focus != nil {
		after = append(after, *args.Focus)
	}
	if len(after) == 0 {
		return argv
	}
	return append(append(argv, "--"), after...)
}

// reviewDecideArgv is the ws review decide command line: flags from typed
// fields, then "--", then the id and findings.
func reviewDecideArgv(root string, args *envproto.ReviewDecideArgs) []string {
	argv := append(cliPrefix(root), "review", "decide", "--"+args.Decision)
	if args.Suggestion != nil {
		argv = append(argv, "--suggestion", strconv.Itoa(*args.Suggestion))
	}
	if args.Note != nil {
		argv = append(argv, "--note="+*args.Note)
	}
	argv = append(argv, "--", args.Review)
	return append(argv, args.Findings...)
}

// reviewActedArgv is the ws review acted command line.
func reviewActedArgv(root string, args *envproto.ReviewActedArgs) []string {
	argv := append(cliPrefix(root), "review", "acted")
	if args.Commented != nil {
		argv = append(argv, "--commented="+*args.Commented)
	}
	if args.FixSent != nil {
		argv = append(argv, "--fix-sent="+*args.FixSent)
	}
	argv = append(argv, "--", args.Review)
	return append(argv, args.Findings...)
}

// reviewRuns runs review list and returns its object's fields.
func (o *agentOperations) reviewRuns(ctx context.Context, extra ...string) (map[string]json.RawMessage, error) {
	out, err := o.runCLI(ctx, append(append(cliPrefix(o.app.paths.Root), "review", "list"), extra...))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(out, &fields); err != nil {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "review list printed an unexpected object"}
	}
	return fields, nil
}

// reviewStatus is the run whose id is id, with finished as ws review wait
// decides it.
func (o *agentOperations) reviewStatus(ctx context.Context, id string) (json.RawMessage, error) {
	fields, err := o.reviewRuns(ctx)
	if err != nil {
		return nil, err
	}
	var runs []struct {
		ID    string           `json:"id"`
		Agent *reviewAgentJSON `json:"agent"`
	}
	if err = json.Unmarshal(fields["reviews"], &runs); err != nil {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "review list printed unexpected reviews"}
	}
	for _, run := range runs {
		if run.ID != id {
			continue
		}
		agent := json.RawMessage("null")
		if run.Agent != nil {
			agent = mustMarshal(run.Agent)
		}
		return o.finishResult(mustMarshal(envproto.ReviewStatusResult{Review: id, Agent: agent, Finished: reviewFinished(run.Agent)}))
	}
	return nil, &envproto.ReplyError{Code: string(codeReviewNotFound), Message: "no review " + id}
}

// reviewList is review list's runs, the first limit of them.
func (o *agentOperations) reviewList(ctx context.Context, args *envproto.ReviewListArgs) (json.RawMessage, error) {
	var extra []string
	if args.Repo != nil {
		extra = append(extra, "--repo", *args.Repo)
	}
	if args.PR != nil {
		extra = append(extra, "--pr", strconv.Itoa(*args.PR))
	}
	fields, err := o.reviewRuns(ctx, extra...)
	if err != nil {
		return nil, err
	}
	limit := args.Limit
	if limit == 0 {
		limit = envproto.DefaultListLimit
	}
	var runs []json.RawMessage
	if err := json.Unmarshal(fields["reviews"], &runs); err != nil {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "review list printed unexpected reviews"}
	}
	fields["truncated"] = mustMarshal(len(runs) > limit)
	if len(runs) > limit {
		runs = runs[:limit]
	}
	fields["reviews"] = mustMarshal(runs)
	return o.finishResult(mustMarshal(fields))
}

// reviewReportArgv is the ws review report command line: "--" then the id.
func reviewReportArgv(root string, args *envproto.ReviewReportArgs) []string {
	return append(cliPrefix(root), "review", "report", "--", args.Review)
}

// reviewReport is one page of one section of review report's output. The
// whole report is redacted before it is measured and cut.
func (o *agentOperations) reviewReport(ctx context.Context, args *envproto.ReviewReportArgs) (json.RawMessage, error) {
	out, err := o.runCLI(ctx, reviewReportArgv(o.app.paths.Root, args))
	if err != nil {
		return nil, err
	}
	clean, n, err := o.redactorOrNew().JSON(out)
	if err != nil {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "review report printed invalid JSON"}
	}
	page, err := reportPage(clean, args.Section, args.Offset)
	if err != nil {
		return nil, err
	}
	return o.finishResultCounting(page, n)
}

// reportPage cuts a ws review report --json object to one page of section
// from offset, within envproto.MaxPageBytes, and adds section, offset,
// next_offset and total. The report's items must already be redacted.
func reportPage(report json.RawMessage, section string, offset int) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(report, &fields); err != nil {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "review report printed an unexpected object"}
	}
	var items []json.RawMessage
	if raw := fields[section]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "review report's " + section + " is not a list"}
		}
	}
	other := envproto.SectionSuperseded
	if section == envproto.SectionSuperseded {
		other = envproto.SectionComments
	}
	fields[other] = json.RawMessage("[]")
	fields["section"] = mustMarshal(section)
	fields["offset"] = mustMarshal(offset)
	fields["total"] = mustMarshal(len(items))
	fields["next_offset"] = mustMarshal(len(items))
	fields[section] = json.RawMessage("[]")
	budget := envproto.MaxPageBytes - len(mustMarshal(fields)) - resultSlack/4
	page := []json.RawMessage{}
	used := 0
	next := offset
	for ; next < len(items); next++ {
		item := items[next]
		if len(page) == 0 && len(item) > budget {
			item = truncateItem(item, budget)
		}
		if used+len(item)+1 > budget && len(page) > 0 {
			break
		}
		page = append(page, item)
		used += len(item) + 1
	}
	fields[section] = mustMarshal(page)
	if next >= len(items) {
		fields["next_offset"] = json.RawMessage("null")
	} else {
		fields["next_offset"] = mustMarshal(next)
	}
	return mustMarshal(fields), nil
}

// truncateItem cuts a comment's body, code and suggestions' code until the
// comment fits budget, and marks it truncated.
func truncateItem(item json.RawMessage, budget int) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(item, &fields); err != nil {
		return item
	}
	fields["truncated"] = json.RawMessage("true")
	for limit := truncatedFieldCap; ; limit /= 2 {
		cut := cutComment(fields, limit)
		if len(cut) <= budget || limit == 0 {
			return cut
		}
	}
}

// cutComment is the comment with body, code and each suggestion's code cut to limit bytes.
func cutComment(fields map[string]json.RawMessage, limit int) json.RawMessage {
	out := make(map[string]json.RawMessage, len(fields))
	maps.Copy(out, fields)
	for _, key := range []string{"body", "code"} {
		if _, ok := out[key]; ok {
			out[key] = cutString(out[key], limit)
		}
	}
	var suggestions []map[string]json.RawMessage
	if json.Unmarshal(out["suggestions"], &suggestions) == nil {
		for _, s := range suggestions {
			if _, ok := s["code"]; ok {
				s["code"] = cutString(s["code"], limit)
			}
		}
		out["suggestions"] = mustMarshal(suggestions)
	}
	return mustMarshal(out)
}

// cutString is raw, a JSON string, cut to at most limit bytes on a rune
// boundary. Anything else is returned as it is.
func cutString(raw json.RawMessage, limit int) json.RawMessage {
	var s string
	if raw == nil || json.Unmarshal(raw, &s) != nil || len(s) <= limit {
		return raw
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return mustMarshal(s[:limit])
}

// finishResult redacts every string in result, adds "redacted", and checks
// the size: over envproto.MaxRequestBytes - 4 KiB is internal "result too large".
func (o *agentOperations) finishResult(result json.RawMessage) (json.RawMessage, error) {
	return o.finishResultCounting(result, 0)
}

// finishResultCounting is finishResult for a result whose strings were
// already redacted, with already replacements counted.
func (o *agentOperations) finishResultCounting(result json.RawMessage, already int) (json.RawMessage, error) {
	clean, n, err := o.redactorOrNew().JSON(result)
	if err != nil {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "the result is not JSON"}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(clean, &fields); err != nil {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "the result is not an object"}
	}
	fields["redacted"] = mustMarshal(n + already)
	out := mustMarshal(fields)
	if len(out) > envproto.MaxRequestBytes-resultSlack {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "result too large"}
	}
	return out, nil
}

// redactError is err with its message and every string of its details
// redacted.
func (o *agentOperations) redactError(err error) error {
	re, ok := errors.AsType[*envproto.ReplyError](err)
	if !ok {
		return &envproto.ReplyError{Code: string(codeInternal), Message: o.redactString(err.Error())}
	}
	out := &envproto.ReplyError{Code: re.Code, Message: o.redactString(re.Message)}
	if re.Details == nil {
		return out
	}
	raw, err := json.Marshal(re.Details)
	if err == nil {
		raw, _, err = o.redactorOrNew().JSON(raw)
	}
	if err == nil {
		_ = json.Unmarshal(raw, &out.Details)
	}
	return out
}

func (o *agentOperations) redactString(s string) string {
	clean, _ := o.redactorOrNew().String(s)
	return clean
}

// mustMarshal is v as JSON. v is always a plain value built here.
func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// redactorOrNew is the agent's redactor, or one with only the token patterns.
func (o *agentOperations) redactorOrNew() *redact.Redactor {
	if o.redactor == nil {
		return redact.New()
	}
	return o.redactor
}
