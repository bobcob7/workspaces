import { afterEach, describe, expect, it, vi } from "vitest";
import { commentPlan, fakeApi, fakeHost, fixPlan, head, testStore } from "../../test/fixtures";
import { attachKey, openTerminal, submitDelay } from "../terminals/terminalsSlice";
import { attachSettle, postReview, sendFix } from "./actionsSlice";

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
    expect(api.reviewFixSent).toHaveBeenCalledWith(subject.repo, head, plan.findings, "fix-login-rate");
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
    expect(api.reviewReport).toHaveBeenCalledWith(subject.repo, head, true);
    expect(store.getState().ui.notice).toEqual({
      severity: "info",
      message: "Posted, but recording it in the review failed: boom",
      action: { label: "View on GitHub", href: url },
    });
  });
});
