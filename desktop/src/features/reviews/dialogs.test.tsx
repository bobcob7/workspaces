import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api, RemoteEnvironment } from "../../api";
import { fakeApi, listedProfile, pullRequest, remoteEnvironment, reviewPlan, reviewsView, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchReviews, openStartDialog, watchedOpened } from "./reviewsSlice";
import { StartReviewDialog } from "./StartReviewDialog";
import { WatchedReposDialog } from "./WatchedReposDialog";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(async () => undefined),
  Channel: class {},
}));

const seen = "2026-11-23T14:02:00Z";

const withEnvironments = (items: readonly RemoteEnvironment[], api: Api = fakeApi()): ReturnType<typeof testStore> =>
  testStore({ api, state: { environments: { items, loadedAt: Date.now(), error: null, latestRequestId: null, warnings: [], lastChoice: "local" } } });

const offlineEnv = (): RemoteEnvironment => remoteEnvironment({ id: "b", name: "win-rig", status: "offline", lastSeen: seen });

describe("WatchedReposDialog", () => {
  const openDialog = (api = fakeApi()): ReturnType<typeof testStore> => {
    const store = testStore({ api });
    store.dispatch(fetchReviews.fulfilled(reviewsView(), "req"));
    store.dispatch(watchedOpened());
    renderWithStore(<WatchedReposDialog />, store);
    return store;
  };

  it("re-reads the cached repos when opened", () => {
    const api = fakeApi();
    openDialog(api);
    expect(api.reviewsLoadRepos).toHaveBeenCalled();
  });

  it("turns on auto-review for a watched repo", async () => {
    const api = fakeApi();
    openDialog(api);
    await userEvent.click(screen.getByLabelText("Auto-review github.com/bobcob7/buff-bear"));
    expect(api.reviewsSetWatch).toHaveBeenCalledWith("github.com/bobcob7/buff-bear", true, true, "local");
  });

  it("stops watching a repo", async () => {
    const api = fakeApi();
    openDialog(api);
    await userEvent.click(screen.getByLabelText("Watch github.com/bobcob7/buff-bear"));
    expect(api.reviewsSetWatch).toHaveBeenCalledWith("github.com/bobcob7/buff-bear", false, false, "local");
  });

  it("says to choose the forge for a repo whose host has none", () => {
    openDialog();
    expect(screen.getByText("Choose its forge in Settings › Forges")).toBeInTheDocument();
  });

  it("sets an environment", async () => {
    const api = fakeApi();
    const store = withEnvironments([remoteEnvironment(), offlineEnv()], api);
    store.dispatch(fetchReviews.fulfilled(reviewsView(), "req"));
    store.dispatch(watchedOpened());
    renderWithStore(<WatchedReposDialog />, store);
    await userEvent.click(screen.getByRole("combobox", { name: "Environment for github.com/bobcob7/buff-bear" }));
    expect(screen.getByRole("option", { name: "win-rig (offline)" })).not.toHaveAttribute("aria-disabled", "true");
    await userEvent.click(screen.getByRole("option", { name: "linux-server" }));
    expect(api.reviewsSetWatch).toHaveBeenCalledWith("github.com/bobcob7/buff-bear", true, false, "linux-server");
  });

  it("keeps the environment when a switch is toggled", async () => {
    const api = fakeApi();
    const view = reviewsView();
    const [first, ...rest] = view.repos;
    const remote = { ...view, repos: [{ ...first!, environment: "linux-server" }, ...rest] };
    const store = withEnvironments([remoteEnvironment()], Object.assign(api, { reviewsLoadRepos: vi.fn(async () => remote) }));
    store.dispatch(fetchReviews.fulfilled(remote, "req"));
    store.dispatch(watchedOpened());
    renderWithStore(<WatchedReposDialog />, store);
    await userEvent.click(screen.getByLabelText("Auto-review github.com/bobcob7/buff-bear"));
    expect(api.reviewsSetWatch).toHaveBeenCalledWith("github.com/bobcob7/buff-bear", true, true, "linux-server");
  });

  it("offers only enrolled environments", async () => {
    const pending = remoteEnvironment({ id: "p", name: "pending-box", state: "pending", status: "pending" });
    const store = withEnvironments([remoteEnvironment(), pending]);
    store.dispatch(fetchReviews.fulfilled(reviewsView(), "req"));
    store.dispatch(watchedOpened());
    renderWithStore(<WatchedReposDialog />, store);
    await userEvent.click(screen.getByRole("combobox", { name: "Environment for github.com/bobcob7/buff-bear" }));
    expect(screen.getByRole("option", { name: "linux-server" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /pending-box/ })).not.toBeInTheDocument();
  });

  it("hides the Environment column when no environment is enrolled", () => {
    const pending = remoteEnvironment({ id: "p", name: "pending-box", state: "pending", status: "pending" });
    const store = withEnvironments([pending]);
    store.dispatch(fetchReviews.fulfilled(reviewsView(), "req"));
    store.dispatch(watchedOpened());
    renderWithStore(<WatchedReposDialog />, store);
    expect(screen.queryByRole("columnheader", { name: "Environment" })).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });

  it("has no environment select when none is enrolled", () => {
    openDialog();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });

  it("goes to the review rules in Settings", async () => {
    const store = openDialog();
    await userEvent.click(screen.getByRole("button", { name: "Review rules…" }));
    expect(store.getState().ui.section).toBe("settings");
  });
});

describe("StartReviewDialog", () => {
  const openDialog = async (api = fakeApi()): Promise<ReturnType<typeof testStore>> => {
    const store = testStore({ api });
    store.dispatch(fetchReviews.fulfilled(reviewsView(), "req"));
    renderWithStore(<StartReviewDialog />, store);
    await store.dispatch(openStartDialog(pullRequest()));
    return store;
  };

  it("starts the review with the focus typed in", async () => {
    const api = fakeApi();
    await openDialog(api);
    await userEvent.type(await screen.findByLabelText("Focus"), "Only check the SQL");
    await userEvent.click(screen.getByRole("button", { name: "Start review" }));
    expect(api.reviewsStart).toHaveBeenCalledWith(reviewPlan({ focus: "Only check the SQL" }));
  });

  it("starts with an empty focus and the plan's profiles when nothing is edited", async () => {
    const profiles = [{ name: "security", model: "opus", auto: false }];
    const api = fakeApi({ reviewsPlan: vi.fn(async () => reviewPlan({ profiles })) });
    await openDialog(api);
    expect(await screen.findByLabelText("Focus")).toHaveValue("");
    expect(screen.queryByLabelText("Task")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Start review" }));
    expect(api.reviewsStart).toHaveBeenCalledWith(reviewPlan({ profiles }));
  });

  it("sets the review agent's model", async () => {
    const api = fakeApi();
    await openDialog(api);
    await userEvent.click(await screen.findByLabelText("Review agent model"));
    await userEvent.click(screen.getByRole("option", { name: "opus" }));
    await userEvent.click(screen.getByRole("button", { name: "Start review" }));
    expect(api.reviewsStart).toHaveBeenCalledWith(reviewPlan({ model: "opus" }));
  });

  it("warns that a different name won't be connected to the pull request", async () => {
    await openDialog();
    await userEvent.type(await screen.findByLabelText("Workspace name"), "-x");
    expect(screen.getByText(/won't connect the agent to this pull request/)).toBeInTheDocument();
  });

  it("shows the repo at the head commit, read-only", async () => {
    await openDialog();
    expect(await screen.findByText("github.com/bobcob7/buff-bear@3f2a9c1")).toBeInTheDocument();
  });

  describe("on environments", () => {
    const second = (): RemoteEnvironment => remoteEnvironment({ id: "c", name: "mac-mini" });
    const openRemote = async (
      items: readonly RemoteEnvironment[],
      api: Api,
      repoEnvironment: string | null = "linux-server",
    ): Promise<void> => {
      const store = withEnvironments(items, api);
      const view = reviewsView();
      const [first, ...rest] = view.repos;
      store.dispatch(fetchReviews.fulfilled({ ...view, repos: [{ ...first!, environment: repoEnvironment ?? undefined }, ...rest] }, "req"));
      renderWithStore(<StartReviewDialog />, store);
      await store.dispatch(openStartDialog(pullRequest()));
    };
    const remoteApi = (overrides: Partial<Api> = {}): Api =>
      fakeApi({ reviewsPlan: vi.fn(async () => reviewPlan({ environment: "linux-server" })), ...overrides });

    it("preselects the repo's environment", async () => {
      const api = remoteApi();
      await openRemote([remoteEnvironment(), second()], api);
      expect(await screen.findByRole("combobox", { name: "Environment" })).toHaveTextContent("linux-server");
      expect(api.reviewsPlan).toHaveBeenCalledWith("github.com/bobcob7/buff-bear", 42, "linux-server");
    });

    it("reloads the profiles when the environment changes", async () => {
      const api = remoteApi();
      await openRemote([remoteEnvironment(), second()], api);
      await waitFor(() => expect(api.reviewsProfiles).toHaveBeenCalledWith("linux-server"));
      await userEvent.click(await screen.findByRole("combobox", { name: "Environment" }));
      await userEvent.click(screen.getByRole("option", { name: "mac-mini" }));
      await waitFor(() => expect(api.reviewsProfiles).toHaveBeenCalledWith("mac-mini"));
    });

    it("starts on the chosen environment without the plan's profiles", async () => {
      const profiles = [{ name: "security", model: "opus", auto: false }];
      const api = fakeApi({ reviewsPlan: vi.fn(async () => reviewPlan({ profiles })) });
      await openRemote([remoteEnvironment(), second()], api, null);
      await userEvent.click(await screen.findByRole("combobox", { name: "Environment" }));
      await userEvent.click(screen.getByRole("option", { name: "mac-mini" }));
      await userEvent.click(screen.getByRole("button", { name: "Start review" }));
      expect(api.reviewsStart).toHaveBeenCalledWith(reviewPlan({ environment: "mac-mini", profiles: [] }));
      expect(vi.mocked(api.reviewsStart).mock.calls[0]?.[0]).not.toHaveProperty("confirmed");
    });

    it("disables start with the reason when the environment is offline", async () => {
      const api = fakeApi({ reviewsPlan: vi.fn(async () => reviewPlan({ environment: "win-rig" })) });
      await openRemote([offlineEnv()], api, "win-rig");
      expect(await screen.findByRole("button", { name: "Start review" })).toBeDisabled();
      expect(screen.getByText(/win-rig is offline/)).toBeInTheDocument();
    });

    it("disables start when the environment has no claude", async () => {
      const api = remoteApi();
      const hello = { ...remoteEnvironment().hello!, claude: "missing" as const };
      await openRemote([remoteEnvironment({ hello })], api);
      expect(await screen.findByRole("button", { name: "Start review" })).toBeDisabled();
      expect(screen.getByText(/claude isn't installed on linux-server/)).toBeInTheDocument();
    });

    const submitForm = (): void => {
      const form = screen.getByLabelText("Focus").closest("form");
      expect(form).not.toBeNull();
      fireEvent.submit(form as HTMLFormElement);
    };

    it("does not start from Enter in the Focus field while the environment is offline", async () => {
      const api = fakeApi({ reviewsPlan: vi.fn(async () => reviewPlan({ environment: "win-rig" })) });
      await openRemote([offlineEnv()], api, "win-rig");
      await screen.findByText(/win-rig is offline/);
      submitForm();
      expect(api.reviewsStart).not.toHaveBeenCalled();
    });

    it("does not start from a form submit while claude is missing on the environment", async () => {
      const api = remoteApi();
      const hello = { ...remoteEnvironment().hello!, claude: "missing" as const };
      await openRemote([remoteEnvironment({ hello })], api);
      await screen.findByText(/claude isn't installed on linux-server/);
      submitForm();
      expect(api.reviewsStart).not.toHaveBeenCalled();
    });

    it("does not show the previous environment's reviewers under the new environment", async () => {
      let release: (items: ReturnType<typeof listedProfile>[]) => void = () => undefined;
      const deferred = new Promise<ReturnType<typeof listedProfile>[]>((resolve) => {
        release = resolve;
      });
      const reviewsProfiles = vi.fn(async (environment?: string) => (environment === "mac-mini" ? deferred : [listedProfile({ name: "alpha-reviewer" })]));
      const api = remoteApi({ reviewsProfiles });
      await openRemote([remoteEnvironment(), second()], api);
      expect(await screen.findByText(/Reviewers on linux-server: alpha-reviewer/)).toBeInTheDocument();
      await userEvent.click(screen.getByRole("combobox", { name: "Environment" }));
      await userEvent.click(screen.getByRole("option", { name: "mac-mini" }));
      await waitFor(() => expect(reviewsProfiles).toHaveBeenCalledWith("mac-mini"));
      expect(screen.queryByText(/Reviewers on mac-mini/)).not.toBeInTheDocument();
      expect(screen.queryByText(/alpha-reviewer/)).not.toBeInTheDocument();
      release([listedProfile({ name: "beta-reviewer" })]);
      expect(await screen.findByText(/Reviewers on mac-mini: beta-reviewer/)).toBeInTheDocument();
    });

    it("does not offer bypassPermissions on a remote review", async () => {
      const api = remoteApi();
      await openRemote([remoteEnvironment()], api);
      await userEvent.click(await screen.findByLabelText("Permission mode"));
      expect(screen.queryByRole("option", { name: "bypassPermissions" })).not.toBeInTheDocument();
      expect(screen.getByRole("option", { name: "plan" })).toBeInTheDocument();
    });

    it("drops bypassPermissions when moving a review to an environment", async () => {
      const api = fakeApi({ reviewsPlan: vi.fn(async () => reviewPlan({ permissionMode: "bypassPermissions" })) });
      await openRemote([remoteEnvironment()], api, null);
      await userEvent.click(await screen.findByRole("combobox", { name: "Environment" }));
      await userEvent.click(screen.getByRole("option", { name: "linux-server" }));
      await userEvent.click(screen.getByRole("button", { name: "Start review" }));
      expect(api.reviewsStart).toHaveBeenCalledWith(reviewPlan({ environment: "linux-server", permissionMode: "" }));
    });

    it("offers bypassPermissions locally", async () => {
      await openRemote([remoteEnvironment()], fakeApi(), null);
      await userEvent.click(await screen.findByLabelText("Permission mode"));
      expect(screen.getByRole("option", { name: "bypassPermissions" })).toBeInTheDocument();
    });
  });
});
