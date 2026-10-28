import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { invoke } from "@tauri-apps/api/core";
import { describe, expect, it, vi } from "vitest";
import type { Api, FindingDecision, ReviewReport } from "../../api";
import { commentPlan, fakeApi, fakeHost, fixPlan, head, pullRequest, reviewRun, reviewsView, testStore, type FakeHost } from "../../test/fixtures";
import { realReport, reviewReport } from "../../test/reportFixture";
import { renderWithStore } from "../../test/render";
import { attachKey, messageKeys, openTerminal } from "../terminals/terminalsSlice";
import { CommentDialog } from "./CommentDialog";
import { FixDialog } from "./FixDialog";
import { ReviewsPane } from "./ReviewsPane";
import { fetchReviews, reviewSelected } from "./reviewsSlice";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(async () => undefined),
  Channel: class {},
}));

const commentDecision: FindingDecision = { decision: "comment", suggestion: null, note: "", at: "2026-10-26T18:30:00Z", done: null };

/** realReport with retry-after-zero decided comment, and spoofable-client-ip decided fix. */
const decided = (): ReviewReport =>
  reviewReport({
    comments: realReport.comments.map((c) => (c.id === "security:retry-after-zero-1" ? { ...c, decision: commentDecision } : c)),
  });

const repo = "github.com/bobcob7/buff-bear";

const renderPane = (overrides: Partial<Api> = {}, host: FakeHost = fakeHost()) => {
  const api = fakeApi({ reviewReport: vi.fn(async () => decided()), ...overrides });
  const store = testStore({ api, terminals: host });
  const pr = pullRequest({ group: "reviewed", reviews: [reviewRun({ requester: "fix-login-rate" })] });
  store.dispatch(fetchReviews.fulfilled(reviewsView({ prs: [pr] }), "req"));
  store.dispatch(reviewSelected(pr.key));
  renderWithStore(
    <>
      <ReviewsPane />
      <CommentDialog />
      <FixDialog />
    </>,
    store,
  );
  return { store, api, host };
};

const openComment = async (overrides: Partial<Api> = {}) => {
  const rendered = renderPane(overrides);
  await userEvent.click(await screen.findByRole("button", { name: "Comment on 1…" }));
  return { ...rendered, dialog: await screen.findByRole("dialog", { name: "Post a review on bobcob7/buff-bear#42" }) };
};

const openFix = async (overrides: Partial<Api> = {}, host?: FakeHost) => {
  const rendered = renderPane(overrides, host);
  await userEvent.click(await screen.findByRole("button", { name: "Fix 1…" }));
  return { ...rendered, dialog: await screen.findByRole("dialog", { name: "Fix 1 finding" }) };
};

describe("CommentDialog", () => {
  it("previews the review at the reviewed commit without posting anything", async () => {
    const { api, dialog } = await openComment();
    expect(api.reviewCommentPlan).toHaveBeenCalledWith(repo, head, 42);
    expect(await within(dialog).findByText(/One review, as/)).toHaveTextContent(
      "One review, as bobcob7, at 3f2a9c1 (the PR's current head). Nothing is posted until you choose Post review.",
    );
    expect(within(dialog).getByLabelText("Review summary")).toHaveValue(commentPlan().body);
    expect(within(dialog).getByText("1 line comment · 1 file comment · 0 in summary")).toBeInTheDocument();
    expect(api.reviewCommentPost).not.toHaveBeenCalled();
  });

  it("defaults the event to Comment", async () => {
    const { dialog } = await openComment();
    expect(await within(dialog).findByRole("combobox", { name: "Event" })).toHaveTextContent("Comment");
  });

  it("shows each comment's place and kind, with agent mentions as code", async () => {
    const { dialog } = await openComment();
    const line = await within(dialog).findByRole("region", { name: "Comment on ratelimit/limiter.go" });
    expect(within(line).getByText("line 58")).toBeInTheDocument();
    expect(within(line).getByText("line comment")).toBeInTheDocument();
    const file = within(dialog).getByRole("region", { name: "Comment on ratelimit/middleware.go" });
    expect(within(file).getByText("file comment")).toBeInTheDocument();
    expect(within(file).getByText("@alice").tagName).toBe("CODE");
  });

  it("posts the edited summary, comment, and chosen event as one review", async () => {
    const { api, store, dialog } = await openComment();
    const summary = await within(dialog).findByLabelText("Review summary");
    await userEvent.clear(summary);
    await userEvent.type(summary, "Two things.");
    const card = within(dialog).getByRole("region", { name: "Comment on ratelimit/limiter.go" });
    await userEvent.click(within(card).getByRole("button", { name: "Edit" }));
    const text = within(card).getByLabelText("Text of the comment on ratelimit/limiter.go");
    await userEvent.clear(text);
    await userEvent.type(text, "Edited.");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Event" }));
    await userEvent.click(screen.getByRole("option", { name: "Request changes" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Post review" }));
    const plan = commentPlan();
    expect(api.reviewCommentPost).toHaveBeenCalledWith(repo, head, {
      number: 42,
      commitId: head,
      body: "Two things.",
      event: "REQUEST_CHANGES",
      comments: [{ ...plan.comments[0], body: "Edited." }, plan.comments[1]],
      findings: plan.findings,
    });
    expect(store.getState().actions.comment).toBeNull();
    expect(store.getState().ui.notice).toEqual({
      severity: "success",
      message: "Review posted.",
      action: { label: "View on GitHub", href: "https://github.com/bobcob7/buff-bear/pull/42#pullrequestreview-99" },
    });
  });

  it("says the comments may show as outdated when the head has moved", async () => {
    const { dialog } = await openComment({
      reviewCommentPlan: vi.fn(async () => commentPlan({ outdated: true, head: "e71b0d4aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" })),
    });
    expect(await within(dialog).findByText(/may show them as outdated/)).toHaveTextContent("The pull request's head is now e71b0d4");
  });

  it("replaces Post review with Open on GitHub when you have a pending review", async () => {
    const url = "https://github.com/bobcob7/buff-bear/pull/42#pullrequestreview-7";
    const { dialog } = await openComment({ reviewCommentPlan: vi.fn(async () => commentPlan({ pendingReview: url })) });
    const alert = await within(dialog).findByText(/You have an unsubmitted review/);
    expect(within(dialog).queryByRole("button", { name: "Post review" })).not.toBeInTheDocument();
    await userEvent.click(within(alert.closest("[role=alert]") ?? dialog).getByRole("button", { name: "Open on GitHub" }));
    expect(invoke).toHaveBeenCalledWith("open_external", { target: url });
  });

  it("keeps the dialog open with gh's message when posting fails", async () => {
    const { store, dialog } = await openComment({
      reviewCommentPost: vi.fn(async () => {
        throw { code: "gh_failed", message: "gh: Unprocessable Entity (HTTP 422) Nothing was posted." };
      }),
    });
    await userEvent.click(await within(dialog).findByRole("button", { name: "Post review" }));
    expect(await within(dialog).findByText("gh: Unprocessable Entity (HTTP 422) Nothing was posted.")).toBeInTheDocument();
    expect(store.getState().actions.comment).not.toBeNull();
  });

  it("shows the pending-review Alert when GitHub refuses a second one", async () => {
    const { dialog } = await openComment({
      reviewCommentPost: vi.fn(async () => {
        throw { code: "review_pending", message: "You have an unsubmitted review on this pull request." };
      }),
    });
    await userEvent.click(await within(dialog).findByRole("button", { name: "Post review" }));
    expect(await within(dialog).findByRole("button", { name: "Open on GitHub" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Post review" })).not.toBeInTheDocument();
  });
});

describe("FixDialog", () => {
  it("asks for a plan with the findings decided fix and their titles", async () => {
    const { api } = await openFix();
    expect(api.reviewFixPlan).toHaveBeenCalledWith(repo, head, "review-buff-bear-42-3f2a9c1", [
      { id: "security:spoofable-client-ip-1", title: expect.any(String) },
    ]);
  });

  it("sends to the requester by default, through its terminal, then records it", async () => {
    const host = fakeHost();
    const { api, store, dialog } = await openFix({}, host);
    // Already attached, as when its agent was shown, so no settle delay.
    await store.dispatch(
      openTerminal({ key: attachKey("e8cdf5d8"), title: "fix-login-rate", spec: { kind: "attach", sessionId: "e8cdf5d8", cwd: "/w" } }),
    );
    expect(await within(dialog).findByRole("radio", { name: "Send to fix-login-rate" })).toBeChecked();
    expect(within(dialog).getByLabelText("Message")).toHaveValue(fixPlan().requesterMessage);
    await userEvent.click(within(dialog).getByRole("button", { name: "Send to fix-login-rate" }));
    await vi.waitFor(() => expect(api.reviewFixSent).toHaveBeenCalled());
    expect(host.send).toHaveBeenNthCalledWith(1, attachKey("e8cdf5d8"), messageKeys(fixPlan().requesterMessage));
    expect(host.send).toHaveBeenNthCalledWith(2, attachKey("e8cdf5d8"), "\r");
    expect(api.reviewFixSent).toHaveBeenCalledWith(repo, head, ["security:spoofable-client-ip-1"], "fix-login-rate");
    expect(store.getState().ui.notice?.action).toEqual({ label: "Open agent", agent: "fix-login-rate" });
    expect(store.getState().actions.fix).toBeNull();
  });

  it("starts a fix agent when the requester is gone", async () => {
    const plan = fixPlan({
      target: "agent",
      requester: { workspaceId: "fix-login-rate", sessionId: null, path: "", status: null, available: false, reason: "removed" },
    });
    const { api, dialog } = await openFix({ reviewFixPlan: vi.fn(async () => plan) });
    expect(await within(dialog).findByRole("radio", { name: "Send to fix-login-rate" })).toBeDisabled();
    expect(within(dialog).getByText("removed")).toBeInTheDocument();
    expect(within(dialog).getByRole("radio", { name: "Start a fix agent" })).toBeChecked();
    expect(within(dialog).getByText(plan.agent.command)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Start fix agent" }));
    expect(api.reviewFixStart).toHaveBeenCalledWith(repo, head, {
      findings: plan.findings,
      agent: plan.agent,
      message: plan.agentMessage,
    });
  });

  it("switches an untouched message with the target and keeps an edited one", async () => {
    const { dialog } = await openFix();
    const message = await within(dialog).findByLabelText("Message");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Start a fix agent" }));
    expect(message).toHaveValue(fixPlan().agentMessage);
    await userEvent.type(message, " Please.");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Send to fix-login-rate" }));
    expect(message).toHaveValue(`${fixPlan().agentMessage} Please.`);
  });

  it("names the fix branch for a fork's pull request", async () => {
    const plan = fixPlan({ agent: { ...fixPlan().agent, branch: "fix/pr-42-3f2a9c1" } });
    const { dialog } = await openFix({ reviewFixPlan: vi.fn(async () => plan) });
    expect(await within(dialog).findByText("fix/pr-42-3f2a9c1")).toBeInTheDocument();
  });

  it("keeps the dialog open with the error when starting fails", async () => {
    const { store, dialog } = await openFix({
      reviewFixPlan: vi.fn(async () => fixPlan({ target: "agent" })),
      reviewFixStart: vi.fn(async () => {
        throw { code: "claude_untrusted", message: "not trusted" };
      }),
    });
    await userEvent.click(await within(dialog).findByRole("button", { name: "Start fix agent" }));
    expect(await within(dialog).findByText("not trusted")).toBeInTheDocument();
    expect(store.getState().actions.fix).not.toBeNull();
  });
});
