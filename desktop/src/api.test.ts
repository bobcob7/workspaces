import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";

const invoke = vi.hoisted(() => vi.fn(async () => ({})));

vi.mock("./transport", () => ({ transport: () => ({ invoke }) }));

beforeEach(() => {
  invoke.mockClear();
});

describe("api environment commands", () => {
  it("environmentsList invokes environments_list without arguments", async () => {
    await api.environmentsList();
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environments_list", undefined);
  });

  it("environmentAdd passes the name", async () => {
    await api.environmentAdd("win-rig");
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environment_add", { name: "win-rig" });
  });

  it("environmentRotate passes the name", async () => {
    await api.environmentRotate("win-rig");
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environment_rotate", { name: "win-rig" });
  });

  it("environmentRemove passes the name", async () => {
    await api.environmentRemove("win-rig");
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environment_remove", { name: "win-rig" });
  });

  it("environmentsCaStatus invokes environments_ca_status without arguments", async () => {
    await api.environmentsCaStatus();
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environments_ca_status", undefined);
  });

  it("environmentsCaRotate passes finish and force", async () => {
    await api.environmentsCaRotate(true, false);
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environments_ca_rotate", { finish: true, force: false });
  });
});

describe("api commands that gained an environment argument", () => {
  it("removeEntry defaults the environment to local", async () => {
    await api.removeEntry("w1", "s1");
    expect(invoke).toHaveBeenCalledExactlyOnceWith("remove_entry", {
      workspaceId: "w1",
      sessionId: "s1",
      keepChildren: false,
      environment: "local",
    });
  });

  it("removeEntry passes a given environment", async () => {
    await api.removeEntry("w1", null, true, "win-rig");
    expect(invoke).toHaveBeenCalledExactlyOnceWith("remove_entry", {
      workspaceId: "w1",
      sessionId: null,
      keepChildren: true,
      environment: "win-rig",
    });
  });

  it("renameWorkspace passes the environment, local by default", async () => {
    await api.renameWorkspace("w1", "new");
    expect(invoke).toHaveBeenLastCalledWith("rename_workspace", { workspaceId: "w1", name: "new", environment: "local" });
    await api.renameWorkspace("w1", "new", "win-rig");
    expect(invoke).toHaveBeenLastCalledWith("rename_workspace", { workspaceId: "w1", name: "new", environment: "win-rig" });
  });

  it("setWorkspaceHidden passes the environment, local by default", async () => {
    await api.setWorkspaceHidden("w1", true);
    expect(invoke).toHaveBeenLastCalledWith("set_workspace_hidden", { workspaceId: "w1", hidden: true, environment: "local" });
    await api.setWorkspaceHidden("w1", false, "win-rig");
    expect(invoke).toHaveBeenLastCalledWith("set_workspace_hidden", { workspaceId: "w1", hidden: false, environment: "win-rig" });
  });

  it("stopAgent passes the environment, local by default", async () => {
    await api.stopAgent("s1");
    expect(invoke).toHaveBeenLastCalledWith("stop_agent", { sessionId: "s1", workspaceId: null, environment: "local" });
    await api.stopAgent("s1", "w1", "win-rig");
    expect(invoke).toHaveBeenLastCalledWith("stop_agent", { sessionId: "s1", workspaceId: "w1", environment: "win-rig" });
  });
});

describe("api review commands with an environment", () => {
  const decide = { findings: ["a:1"], decision: "skip" as const, suggestion: null, note: "" };

  it("reviewsSetWatch sends the environment, local by default", async () => {
    await api.reviewsSetWatch("r", true, false);
    expect(invoke).toHaveBeenLastCalledWith("reviews_set_watch", { repo: "r", watch: true, autoReview: false, environment: "local" });
    await api.reviewsSetWatch("r", true, false, "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("reviews_set_watch", { repo: "r", watch: true, autoReview: false, environment: "linux-server" });
  });

  it("reviewsPlan leaves the environment out unless given", async () => {
    await api.reviewsPlan("r", 4);
    expect(invoke).toHaveBeenLastCalledWith("reviews_plan", { repo: "r", number: 4, environment: undefined });
    await api.reviewsPlan("r", 4, "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("reviews_plan", { repo: "r", number: 4, environment: "linux-server" });
  });

  it("reviewsProfiles sends the environment, local by default", async () => {
    await api.reviewsProfiles();
    expect(invoke).toHaveBeenLastCalledWith("reviews_profiles", { environment: "local" });
    await api.reviewsProfiles("linux-server");
    expect(invoke).toHaveBeenLastCalledWith("reviews_profiles", { environment: "linux-server" });
  });

  it("reviewReport and reviewDecide send the environment and review id", async () => {
    await api.reviewReport("r", "abc", true);
    expect(invoke).toHaveBeenLastCalledWith("review_report", { repo: "r", gitRef: "abc", refresh: true, environment: "local", reviewId: undefined });
    await api.reviewReport("r", "abc", false, "linux-server", "review-1");
    expect(invoke).toHaveBeenLastCalledWith("review_report", { repo: "r", gitRef: "abc", refresh: false, environment: "linux-server", reviewId: "review-1" });
    await api.reviewDecide("r", "abc", decide, "linux-server", "review-1");
    expect(invoke).toHaveBeenLastCalledWith("review_decide", { repo: "r", gitRef: "abc", request: decide, environment: "linux-server", reviewId: "review-1" });
  });

  it("the other review wrappers send the same two trailing arguments", async () => {
    await api.reviewCommentPlan("r", "abc", 4, "linux-server", "review-1");
    expect(invoke).toHaveBeenLastCalledWith("review_comment_plan", { repo: "r", gitRef: "abc", number: 4, environment: "linux-server", reviewId: "review-1" });
    await api.reviewFixPlan("r", "abc", "review-1", [], "linux-server", "review-1");
    expect(invoke).toHaveBeenLastCalledWith("review_fix_plan", { repo: "r", gitRef: "abc", review: "review-1", findings: [], environment: "linux-server", reviewId: "review-1" });
    await api.reviewFixSent("r", "abc", ["a:1"], "fix-1", "linux-server", "review-1");
    expect(invoke).toHaveBeenLastCalledWith("review_fix_sent", { repo: "r", gitRef: "abc", findings: ["a:1"], agent: "fix-1", environment: "linux-server", reviewId: "review-1" });
  });
});

describe("api loop commands with an environment", () => {
  it("loopsList sends the environment, local by default", async () => {
    await api.loopsList();
    expect(invoke).toHaveBeenLastCalledWith("loops_list", { environment: "local" });
    await api.loopsList("linux-server");
    expect(invoke).toHaveBeenLastCalledWith("loops_list", { environment: "linux-server" });
  });

  it("loopsRemove and the clear commands send confirmed before the environment", async () => {
    await api.loopsRemove("deps");
    expect(invoke).toHaveBeenLastCalledWith("loops_remove", { name: "deps", confirmed: false, environment: "local" });
    await api.loopsRemove("deps", true, "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("loops_remove", { name: "deps", confirmed: true, environment: "linux-server" });
    await api.loopsHistoryClear("deps", true, "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("loops_history_clear", { name: "deps", confirmed: true, environment: "linux-server" });
    await api.loopsMemoryClear("deps", true, "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("loops_memory_clear", { name: "deps", confirmed: true, environment: "linux-server" });
  });

  it("the other loop wrappers take a trailing environment", async () => {
    await api.loopsSetPaused("deps", true, "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("loops_set_paused", { name: "deps", paused: true, environment: "linux-server" });
    await api.loopsRunNow("deps", "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("loops_run_now", { name: "deps", environment: "linux-server" });
    await api.loopsHistory("deps", 200, "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("loops_history", { name: "deps", limit: 200, environment: "linux-server" });
    await api.loopsHistoryRun("deps", 3, "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("loops_history_run", { name: "deps", seq: 3, environment: "linux-server" });
    await api.loopsMemory("deps", "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("loops_memory", { name: "deps", environment: "linux-server" });
    await api.loopsMemorySet("deps", "k", "v", 2, "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("loops_memory_set", { name: "deps", key: "k", value: "v", expectRev: 2, environment: "linux-server" });
    await api.loopsMemoryRemove("deps", "k", "linux-server");
    expect(invoke).toHaveBeenLastCalledWith("loops_memory_remove", { name: "deps", key: "k", environment: "linux-server" });
  });
});
