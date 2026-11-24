import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Loop } from "../../api";
import { fakeApi, loop, remoteEnvironment, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { LoopsList } from "./LoopsList";
import { fetchLoops } from "./loopsSlice";

const listing = (loops: Loop[], loopsUnknown = false) => ({ loops, warnings: [], environmentStatus: null, loopsUnknown });

const apiFor = (byEnv: Record<string, { loops: Loop[]; unknown?: boolean }>) =>
  fakeApi({ loopsList: async (environment = "local") => listing(byEnv[environment]?.loops ?? [], byEnv[environment]?.unknown ?? false) });

const storeWith = (envs: ReturnType<typeof remoteEnvironment>[], byEnv: Record<string, { loops: Loop[]; unknown?: boolean }>) => {
  const base = testStore();
  const store = testStore({ api: apiFor(byEnv), state: { environments: { ...base.getState().environments, items: envs } } });
  store.dispatch(
    fetchLoops.fulfilled(
      {
        results: Object.entries(byEnv).map(([environment, v]) => ({ environment, listing: listing(v.loops, v.unknown), error: null })),
        at: 1,
      },
      "req",
    ),
  );
  return store;
};

const render = (store: ReturnType<typeof testStore>) => renderWithStore(<LoopsList />, store);

describe("LoopsList", () => {
  it("loops are grouped by environment with local first", async () => {
    const store = storeWith([remoteEnvironment()], { "linux-server": { loops: [loop({ name: "far", environment: "linux-server" })] }, local: { loops: [loop({ name: "near" })] } });
    render(store);
    const groups = await screen.findAllByRole("group", { name: /loops$/ });
    expect(groups.map((g) => g.getAttribute("aria-label"))).toEqual(["local loops", "linux-server loops"]);
    expect(within(groups[1]!).getByText("far")).toBeInTheDocument();
  });

  it("an offline group is dimmed with its reason", async () => {
    const store = storeWith([remoteEnvironment({ status: "offline", lastSeen: "2026-11-23T14:02:00Z" })], {
      "linux-server": { loops: [loop({ name: "far", environment: "linux-server", stale: true })] },
    });
    render(store);
    const group = await screen.findByRole("group", { name: "linux-server loops" });
    expect(group).toHaveAttribute("data-offline", "true");
    expect(within(group).getByText(/linux-server is offline \(last seen/)).toBeInTheDocument();
  });

  it("a protocol 2 environment says loops can't run from here", async () => {
    const store = storeWith([remoteEnvironment()], { "linux-server": { loops: [], unknown: true } });
    render(store);
    expect(await screen.findByText(/loops can't run from here/)).toBeInTheDocument();
  });

  it("with no environment enrolled the list is unchanged", async () => {
    const store = storeWith([], { local: { loops: [loop({ name: "near" })] } });
    render(store);
    expect(await screen.findByRole("list", { name: "Loops" })).toBeInTheDocument();
    expect(screen.queryByRole("group")).not.toBeInTheDocument();
    expect(screen.queryByText("local")).not.toBeInTheDocument();
  });

  it("a chip filters the list to one environment", async () => {
    const store = storeWith([remoteEnvironment()], { "linux-server": { loops: [loop({ name: "far", environment: "linux-server" })] }, local: { loops: [loop({ name: "near" })] } });
    render(store);
    await userEvent.click(await screen.findByRole("button", { name: /linux-server/ }));
    expect(screen.queryByText("near")).not.toBeInTheDocument();
    expect(screen.getByText("far")).toBeInTheDocument();
  });

  it("selecting a remote loop records its environment", async () => {
    const store = storeWith([remoteEnvironment()], { "linux-server": { loops: [loop({ name: "far", environment: "linux-server" })] } });
    render(store);
    await userEvent.click(await screen.findByText("far"));
    expect(store.getState().loops.selected).toBe("far");
    expect(store.getState().loops.selectedEnvironment).toBe("linux-server");
  });
});
