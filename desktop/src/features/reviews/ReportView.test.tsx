import { act, fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { invoke } from "@tauri-apps/api/core";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Api, FindingDecision, PullRequest, ReviewReport } from "../../api";
import { fakeApi, head, pullRequest, reviewRun, reviewsView, testStore } from "../../test/fixtures";
import { realReport, reportComment, reviewReport } from "../../test/reportFixture";
import { renderWithStore } from "../../test/render";
import { ReviewsList } from "./ReviewsList";
import { ReviewsPane } from "./ReviewsPane";
import { fetchReviews, reviewSelected } from "./reviewsSlice";
import { reportInterval } from "./useReports";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(async () => undefined),
  Channel: class {},
}));

const fixSent: FindingDecision = {
  decision: "fix",
  suggestion: null,
  note: "",
  at: "2026-10-26T18:30:00Z",
  done: { fix_sent: "fix-login-rate", at: "2026-10-26T18:31:00Z" },
};

const reviewed = (overrides: Partial<PullRequest> = {}): PullRequest =>
  pullRequest({ group: "reviewed", reviews: [reviewRun()], ...overrides });

const renderReport = (report: ReviewReport = realReport, pr: PullRequest = reviewed(), api?: Api) => {
  const calls = api ?? fakeApi({ reviewReport: vi.fn(async () => report), reviewDecide: vi.fn(async () => report) });
  const store = testStore({ api: calls });
  store.dispatch(fetchReviews.fulfilled(reviewsView({ prs: [pr] }), "req"));
  store.dispatch(reviewSelected(pr.key));
  renderWithStore(<ReviewsPane />, store);
  return { store, api: calls };
};

const row = (id: string): HTMLElement => {
  const head = document.getElementById(`finding-${id}`);
  if (head === null) {
    throw new Error(`no row for ${id}`);
  }
  return head;
};

const ws = { repo: "github.com/bobcob7/buff-bear", ref: head };

afterEach(() => {
  vi.useRealTimers();
});

describe("ReportView", () => {
  it("opens on Findings with bands in order and Optional collapsed", async () => {
    renderReport();
    expect(await screen.findByRole("tab", { name: "Findings3" })).toHaveAttribute("aria-selected", "true");
    const headings = screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(headings).toEqual(["Must fix1", "Should fix1"]);
    expect(screen.getByRole("button", { name: /Optional/ })).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByLabelText("Tally")).toHaveTextContent("1 to fix2 undecided1 optional hidden");
  });

  it("reads the report again when the pull request is selected", async () => {
    const { api } = renderReport();
    await screen.findByRole("tab", { name: "Findings3" });
    expect(api.reviewReport).toHaveBeenCalledWith(ws.repo, ws.ref, true);
  });

  it("shows each profile's model, verdict, and severity", async () => {
    renderReport(reviewReport({ profiles: realReport.profiles.map((p) => ({ ...p, model: "opus" })) }));
    const strip = await screen.findByLabelText("Profiles");
    expect(within(strip).getByRole("button", { name: /security/ })).toHaveTextContent("securityopusrequest changes1 must · 1 should");
  });

  it("sets a decision with the control", async () => {
    const { api } = renderReport();
    await userEvent.click(within(await screen.findByRole("group", { name: "Decision for security:retry-after-zero-1" })).getByRole("button", { name: "Comment" }));
    expect(api.reviewDecide).toHaveBeenCalledWith(ws.repo, ws.ref, {
      findings: ["security:retry-after-zero-1"],
      decision: "comment",
      suggestion: null,
      note: "",
    });
  });

  it("clears the decision when the selected segment is clicked, keeping nothing else", async () => {
    const { api } = renderReport();
    const control = await screen.findByRole("group", { name: "Decision for security:spoofable-client-ip-1" });
    expect(within(control).getByRole("button", { name: "Fix" })).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(within(control).getByRole("button", { name: "Fix" }));
    expect(api.reviewDecide).toHaveBeenCalledWith(ws.repo, ws.ref, {
      findings: ["security:spoofable-client-ip-1"],
      decision: "clear",
      suggestion: null,
      note: "",
    });
  });

  it("keeps a decided finding's note when the decision changes", async () => {
    const { api } = renderReport();
    const control = await screen.findByRole("group", { name: "Decision for security:spoofable-client-ip-1" });
    await userEvent.click(within(control).getByRole("button", { name: "Skip" }));
    expect(api.reviewDecide).toHaveBeenCalledWith(ws.repo, ws.ref, expect.objectContaining({ decision: "skip", note: "trusted proxies only" }));
  });

  it("expands a row to its body, anchors, code, and suggestions; a suggestion sets Fix", async () => {
    const { api } = renderReport();
    await userEvent.click(await screen.findByRole("button", { name: /^9 Any client can choose/ }));
    expect(screen.getByRole("link", { name: "ratelimit/middleware.go:12" })).toBeInTheDocument();
    expect(screen.getByLabelText("Code")).toHaveTextContent("31func clientIP");
    const radios = screen.getAllByRole("radio");
    expect(radios[0]).toBeChecked();
    await userEvent.click(radios[1] as HTMLElement);
    expect(api.reviewDecide).toHaveBeenCalledWith(ws.repo, ws.ref, {
      findings: ["security:spoofable-client-ip-1"],
      decision: "fix",
      suggestion: 1,
      note: "trusted proxies only",
    });
  });

  it("links the first anchor to the reviewed commit on GitHub, with a count of the rest", async () => {
    renderReport();
    await screen.findByRole("tab", { name: "Findings3" });
    const link = screen.getByRole("link", { name: /^ratelimit\/ip\.go:31-34/ });
    expect(link).toHaveTextContent("ratelimit/ip.go:31-34 +1");
    await userEvent.click(link);
    expect(invoke).toHaveBeenCalledWith("open_external", {
      target: `https://github.com/bobcob7/buff-bear/blob/${head}/ratelimit/ip.go#L31-L34`,
    });
  });

  it("saves a note typed on an undecided finding with its next decision", async () => {
    const { api } = renderReport();
    await userEvent.click(await screen.findByRole("button", { name: /^7 Retry-After/ }));
    await userEvent.click(screen.getByRole("button", { name: "Add a note for the fixer or the comment" }));
    await userEvent.type(screen.getByLabelText("Note"), "round up");
    fireEvent.blur(screen.getByLabelText("Note"));
    expect(api.reviewDecide).not.toHaveBeenCalled();
    await userEvent.click(within(screen.getByRole("group", { name: "Decision for security:retry-after-zero-1" })).getByRole("button", { name: "Fix" }));
    expect(api.reviewDecide).toHaveBeenCalledWith(ws.repo, ws.ref, expect.objectContaining({ decision: "fix", note: "round up" }));
  });

  it("filters by profile and shows `n of total` headings", async () => {
    renderReport();
    const filters = await screen.findByRole("toolbar", { name: "Filters" });
    await userEvent.click(within(filters).getByRole("button", { name: "tests" }));
    expect(screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent)).toEqual(["Must fix0 of 1", "Should fix0 of 1"]);
  });

  it("filters to a profile from its tile, and clears it with a second click", async () => {
    renderReport();
    const tile = within(await screen.findByLabelText("Profiles")).getByRole("button", { name: /security/ });
    await userEvent.click(tile);
    expect(tile).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(tile);
    expect(tile).toHaveAttribute("aria-pressed", "false");
  });

  it("hides decided findings", async () => {
    renderReport();
    await userEvent.click(await screen.findByRole("button", { name: "Hide decided" }));
    expect(document.getElementById("finding-security:spoofable-client-ip-1")).toBeNull();
  });

  it("shows superseded findings read-only, without a control", async () => {
    renderReport();
    await userEvent.click(await screen.findByRole("button", { name: "More filters" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "Show superseded" }));
    await userEvent.click(screen.getByRole("button", { name: /Optional/ }));
    const superseded = row("#2:duplicate-name-1");
    expect(superseded).toHaveTextContent("read-only");
    expect(within(superseded).queryByRole("group")).toBeNull();
    expect(screen.getByLabelText("Tally")).toHaveTextContent("1 read-only (superseded)");
  });

  it("decides several selected findings in one call", async () => {
    const { api } = renderReport();
    await userEvent.click(await screen.findByRole("button", { name: /Optional/ }));
    fireEvent.click(row("security:retry-after-zero-1"), { metaKey: true });
    fireEvent.click(row("tests:missing-over-limit-1"), { metaKey: true });
    await userEvent.click(within(screen.getByRole("group", { name: "Decision for tests:missing-over-limit-1" })).getByRole("button", { name: "Skip" }));
    expect(api.reviewDecide).toHaveBeenCalledTimes(1);
    expect(api.reviewDecide).toHaveBeenCalledWith(ws.repo, ws.ref, {
      findings: ["security:retry-after-zero-1", "tests:missing-over-limit-1"],
      decision: "skip",
      suggestion: null,
      note: "",
    });
  });

  it("drives the list from the keyboard", async () => {
    const { api } = renderReport();
    const first = await screen.findByRole("button", { name: /^9 Any client/ });
    first.focus();
    await userEvent.keyboard("{ArrowDown}");
    expect(document.getElementById("toggle-security:retry-after-zero-1")).toHaveFocus();
    await userEvent.keyboard("c");
    expect(api.reviewDecide).toHaveBeenLastCalledWith(ws.repo, ws.ref, expect.objectContaining({ findings: ["security:retry-after-zero-1"], decision: "comment" }));
    await userEvent.keyboard("{ArrowUp}{Backspace}");
    expect(api.reviewDecide).toHaveBeenLastCalledWith(ws.repo, ws.ref, expect.objectContaining({ findings: ["security:spoofable-client-ip-1"], decision: "clear" }));
    await userEvent.keyboard("{Enter}");
    expect(screen.getAllByRole("radio")).toHaveLength(2);
    await userEvent.keyboard("2");
    expect(api.reviewDecide).toHaveBeenLastCalledWith(ws.repo, ws.ref, expect.objectContaining({ decision: "fix", suggestion: 1 }));
  });

  it("ignores suggestion keys on a collapsed finding and decision keys on a read-only one", async () => {
    const { api } = renderReport();
    (await screen.findByRole("button", { name: /^9 Any client/ })).focus();
    await userEvent.keyboard("1");
    expect(api.reviewDecide).not.toHaveBeenCalled();
  });

  it("shows what was done, and Act again puts the decision back", async () => {
    const report = reviewReport({ comments: [reportComment({ id: "security:a-1", priority: 9, decision: fixSent })] });
    const { api, store } = renderReport(report);
    expect(await screen.findByText("fix-login-rate")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "✓ Fix sent to" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "Act again" }));
    expect(api.reviewDecide).toHaveBeenCalledWith(ws.repo, ws.ref, { findings: ["security:a-1"], decision: "fix", suggestion: null, note: "" });
    await userEvent.click(screen.getByRole("link", { name: "fix-login-rate" }));
    expect(store.getState().ui.section).toBe("agents");
    expect(store.getState().ui.selectedKey).toBe("fix-login-rate");
  });

  it("disables Comment and Fix while the agent works, and says how many profiles are in", async () => {
    const report = reviewReport({ profiles: [...realReport.profiles, { name: "go", model: "", verdict: null, severity: null, missing: true }] });
    renderReport(report, pullRequest({ group: "reviewing", reviews: [reviewRun({ status: "working" })] }));
    expect(await screen.findByText(/2 of 3 profiles have submitted/)).toBeInTheDocument();
    expect(screen.getByText("not in yet")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Comment…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Fix…" })).toBeDisabled();
  });

  it("names a profile still missing once the agent is done, quoting its last message", async () => {
    const report = reviewReport({ profiles: [...realReport.profiles, { name: "architecture", model: "opus", verdict: null, severity: null, missing: true }] });
    const api = fakeApi({
      reviewReport: vi.fn(async () => report),
      readTranscript: vi.fn(async () => ({
        items: [{ id: "1", at: null, kind: "assistant" as const, text: "review-architecture failed with review_unverifiable" }],
        next: 1,
        reset: false,
        dropped: 0,
        found: true,
      })),
    });
    renderReport(report, reviewed(), api);
    expect(await screen.findByText(/never submitted/)).toHaveTextContent(
      'architecture never submitted: the review agent finished without it. Its last message: "review-architecture failed with review_unverifiable". Review again or Open agent.',
    );
    expect(screen.getByText("missing")).toBeInTheDocument();
  });

  it("says when the agent finished without submitting", async () => {
    const report = reviewReport({ profiles: realReport.profiles.map((p) => ({ ...p, missing: true, verdict: null, severity: null })), comments: [], superseded: [] });
    renderReport(report);
    expect(await screen.findByText("The review agent finished without submitting a review.")).toBeInTheDocument();
  });

  it("says when every profile found nothing", async () => {
    renderReport(reviewReport({ comments: [], superseded: [] }));
    expect(await screen.findByText("No findings from 2 profiles.")).toBeInTheDocument();
  });

  it("shows ws's error and keeps the Review agent tab", async () => {
    const api = fakeApi({ reviewReport: vi.fn(async () => Promise.reject({ code: "review_not_found", message: "no review" })) });
    renderReport(realReport, reviewed(), api);
    expect(await screen.findByRole("alert")).toHaveTextContent("review_not_found: no review");
    await userEvent.click(screen.getByRole("tab", { name: "Review agent" }));
    expect(screen.getByRole("button", { name: "Open agent" })).toBeInTheDocument();
  });

  it("opens the full report", async () => {
    const { api } = renderReport();
    await userEvent.click(await screen.findByRole("button", { name: "Open full report" }));
    expect(api.reviewOpenReport).toHaveBeenCalledWith(ws.repo, ws.ref);
  });

  it("points back to a decision made in an older review", async () => {
    const old = "a41c9f2d0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";
    const reports: Record<string, ReviewReport> = {
      [head]: reviewReport({ comments: [reportComment({ id: "security:a-1", priority: 9 })] }),
      [old]: reviewReport({ ref: old, comments: [reportComment({ id: "security:a-1", priority: 9, decision: fixSent })] }),
    };
    const api = fakeApi({ reviewReport: vi.fn(async (_repo: string, ref: string) => reports[ref] ?? realReport) });
    const pr = reviewed({
      reviews: [reviewRun(), reviewRun({ workspaceId: "review-old", sha: old, createdAt: "2026-10-23T10:00:00Z" })],
    });
    renderReport(realReport, pr, api);
    expect(await screen.findByText("decided at a41c9f2")).toBeInTheDocument();
    expect(api.reviewReport).toHaveBeenCalledWith(ws.repo, old, false);
  });

  it("cross-links the requester to Agents", async () => {
    const { store } = renderReport(realReport, reviewed({ reviews: [reviewRun({ requester: "fix-login-rate" })] }));
    await userEvent.click(await screen.findByRole("button", { name: "Requested by fix-login-rate" }));
    expect(store.getState().ui.section).toBe("agents");
    expect(store.getState().ui.selectedKey).toBe("fix-login-rate");
  });

  it("keeps the agent card for a review ws review start didn't make", async () => {
    renderReport(realReport, reviewed({ reviews: [reviewRun({ recorded: false })] }));
    expect(screen.queryByRole("tab", { name: /Findings/ })).toBeNull();
    expect(screen.getByRole("tab", { name: "Review agent" })).toHaveAttribute("aria-selected", "true");
  });
});

describe("ReviewsList with reports", () => {
  const renderList = (pr: PullRequest, report: ReviewReport) => {
    const api = fakeApi({ reviewReport: vi.fn(async () => report) });
    const store = testStore({ api });
    store.dispatch(fetchReviews.fulfilled(reviewsView({ prs: [pr] }), "req"));
    renderWithStore(<ReviewsList />, store);
    return api;
  };

  it("summarizes a reviewed pull request from its report", async () => {
    renderList(reviewed(), realReport);
    expect(await screen.findByText("buff-bear · alice · 1 to fix · 2 undecided")).toBeInTheDocument();
  });

  it("reads a working review's report every 30 s, and again when it goes idle", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const report = reviewReport({ profiles: [...realReport.profiles, { name: "go", model: "", verdict: null, severity: null, missing: true }] });
    const working = pullRequest({ group: "reviewing", reviews: [reviewRun({ status: "working" })] });
    const api = fakeApi({ reviewReport: vi.fn(async () => report) });
    const store = testStore({ api });
    store.dispatch(fetchReviews.fulfilled(reviewsView({ prs: [working] }), "req"));
    renderWithStore(<ReviewsList />, store);
    expect(await screen.findByText("buff-bear · alice · 2 of 3 profiles in")).toBeInTheDocument();
    expect(api.reviewReport).toHaveBeenLastCalledWith(ws.repo, ws.ref, false);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(reportInterval);
    });
    expect(api.reviewReport).toHaveBeenCalledTimes(2);
    expect(api.reviewReport).toHaveBeenLastCalledWith(ws.repo, ws.ref, true);
    await act(async () => {
      store.dispatch(fetchReviews.fulfilled(reviewsView({ prs: [reviewed()] }), "req2"));
    });
    expect(api.reviewReport).toHaveBeenCalledTimes(3);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(reportInterval * 2);
    });
    expect(api.reviewReport).toHaveBeenCalledTimes(3);
  });
});
