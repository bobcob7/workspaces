import { afterEach, describe, expect, it, vi } from "vitest";
import { commentPlan, fakeApi, fakeHost, fixPlan, head, testStore } from "../../test/fixtures";
import { attachKey, openTerminal, submitDelay } from "../terminals/terminalsSlice";
import type { PostRequest } from "../../api";
import { attachSettle, openCommentDialog, openFixDialog, postReview, sendFix } from "./actionsSlice";
import { reportKey } from "./reportsSlice";

const subject = { prKey: "github.com/bobcob7/buff-bear#42", repo: "github.com/bobcob7/buff-bear", ref: head };

afterEach(() => {
  vi.useRealTimers();
});

describe("sendFix", () => {
  it("attaches to a requester that isn't open, waits for it, then types the message", async () => {
    vi.useFakeTimers();
    const api = fakeApi();
    const host = fakeHost();
    const store = testStore({ api, terminals: host });
    const plan = fixPlan();
    const sent = store.dispatch(sendFix({ ...subject, plan, target: "requester", message: "Fix it" }));
    await vi.advanceTimersByTimeAsync(0);
    expect(host.open).toHaveBeenCalledWith(
      attachKey("e8cdf5d8"),
      { kind: "attach", sessionId: "e8cdf5d8", cwd: "/w/fix-login-rate" },
      expect.any(Function),
    );
    expect(host.send).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(attachSettle + submitDelay);
    await sent;
    expect(host.send).toHaveBeenCalledWith(attachKey("e8cdf5d8"), "Fix it");
    expect(api.reviewFixSent).toHaveBeenCalledWith(subject.repo, head, plan.findings, "fix-login-rate", "local", undefined);
  });

  it("refuses a requester that can't take messages, and records nothing", async () => {
    const api = fakeApi();
    const host = fakeHost();
    const store = testStore({ api, terminals: host });
    const plan = fixPlan({
      requester: { workspaceId: "fix-login-rate", sessionId: "e8cdf5d8", path: "/w", status: "stopped", available: false, reason: "stopped: resume it first" },
    });
    const result = await store.dispatch(sendFix({ ...subject, plan, target: "requester", message: "Fix it" }));
    expect(result.meta.requestStatus).toBe("rejected");
    expect(host.send).not.toHaveBeenCalled();
    expect(api.reviewFixSent).not.toHaveBeenCalled();
  });

  it("doesn't record a send when typing fails", async () => {
    const api = fakeApi();
    const host = fakeHost();
    host.send = vi.fn(async () => {
      throw { code: "terminal_closed", message: "terminal 1 isn't open" };
    });
    const store = testStore({ api, terminals: host });
    await store.dispatch(
      openTerminal({ key: attachKey("e8cdf5d8"), title: "fix-login-rate", spec: { kind: "attach", sessionId: "e8cdf5d8", cwd: "/w" } }),
    );
    const result = await store.dispatch(sendFix({ ...subject, plan: fixPlan(), target: "requester", message: "x" }));
    expect(result.meta.requestStatus).toBe("rejected");
    expect(api.reviewFixSent).not.toHaveBeenCalled();
  });
});

describe("postReview", () => {
  it("re-reads the report and passes on the warning when ws couldn't record the post", async () => {
    const url = "https://github.com/bobcob7/buff-bear/pull/42#pullrequestreview-99";
    const api = fakeApi({
      reviewCommentPost: vi.fn(async () => ({ url, report: null, warning: "Posted, but recording it in the review failed: boom" })),
    });
    const store = testStore({ api });
    const plan = commentPlan();
    await store.dispatch(
      postReview({
        ...subject,
        request: { number: 42, commitId: head, body: plan.body, event: "COMMENT", comments: plan.comments, findings: plan.findings },
      }),
    );
    expect(api.reviewReport).toHaveBeenCalledWith(subject.repo, head, true, "local", undefined);
    expect(store.getState().ui.notice).toEqual({
      severity: "info",
      message: "Posted, but recording it in the review failed: boom",
      action: { label: "View on GitHub", href: url },
    });
  });
});

describe("remote review actions", () => {
  const remote = { ...subject, environment: "linux-server", reviewId: "review-1" };
  const postRequest: PostRequest = { number: 42, commitId: head, body: "", event: "COMMENT", comments: [], findings: [] };

  it("opens the comment preview on the review's environment", async () => {
    const api = fakeApi();
    await testStore({ api }).dispatch(openCommentDialog({ ...remote, number: 42 }));
    expect(api.reviewCommentPlan).toHaveBeenCalledWith(subject.repo, head, 42, "linux-server", "review-1");
  });

  it("posts on the review's environment and keeps the report under its key", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(
      postReview({ ...remote, request: postRequest }),
    );
    expect(api.reviewCommentPost).toHaveBeenCalledWith(subject.repo, head, expect.anything(), "linux-server", "review-1");
    expect(store.getState().reports.byKey[reportKey(subject.repo, head, "linux-server")]).toBeDefined();
    expect(store.getState().reports.byKey[reportKey(subject.repo, head)]).toBeUndefined();
  });

  it("plans a fix on the review's environment", async () => {
    const api = fakeApi();
    await testStore({ api }).dispatch(openFixDialog({ ...remote, review: "review-1", findings: [] }));
    expect(api.reviewFixPlan).toHaveBeenCalledWith(subject.repo, head, "review-1", [], "linux-server", "review-1");
  });

  it("starts a fix agent on the review's environment", async () => {
    const api = fakeApi();
    await testStore({ api }).dispatch(sendFix({ ...remote, plan: fixPlan(), target: "agent", message: "Fix it" }));
    expect(api.reviewFixStart).toHaveBeenCalledWith(subject.repo, head, expect.anything(), "linux-server", "review-1");
  });

  it("keeps a local action on local", async () => {
    const api = fakeApi();
    await testStore({ api }).dispatch(openCommentDialog({ ...subject, number: 42 }));
    expect(api.reviewCommentPlan).toHaveBeenCalledWith(subject.repo, head, 42, "local", undefined);
  });
});
