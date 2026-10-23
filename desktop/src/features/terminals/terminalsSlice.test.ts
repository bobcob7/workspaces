import { describe, expect, it } from "vitest";
import type { TermSpec } from "../../api";
import { fakeHost, testStore } from "../../test/fixtures";
import {
  attachedToEvict,
  closeTerminal,
  maxAttached,
  openTerminal,
  terminalsReducer,
  terminalShown,
  type TerminalInfo,
} from "./terminalsSlice";

const attach = (id: string): TermSpec => ({ kind: "attach", sessionId: id, cwd: "/w" });

const info = (key: string, lastUsed: number, overrides: Partial<TerminalInfo> = {}): TerminalInfo => ({
  key,
  title: key,
  spec: attach(key),
  state: "open",
  exitCode: null,
  lastUsed,
  generation: lastUsed,
  ...overrides,
});

describe("attachedToEvict", () => {
  it("evicts the least recently shown attach terminals beyond the limit", () => {
    const items = Array.from({ length: maxAttached + 1 }, (_, i) => info(`t${i}`, i));
    expect(attachedToEvict(items, "t4")).toEqual(["t0"]);
  });

  it("never evicts the terminal being kept", () => {
    const items = Array.from({ length: maxAttached + 1 }, (_, i) => info(`t${i}`, i));
    expect(attachedToEvict(items, "t0")).toEqual([]);
  });

  it("does not count shells or exited terminals", () => {
    const shell: TermSpec = { kind: "shell", name: "s", specs: [] };
    const items = [
      ...Array.from({ length: maxAttached }, (_, i) => info(`t${i}`, i + 10)),
      info("shell", 0, { spec: shell }),
      info("gone", 1, { state: "exited" }),
    ];
    expect(attachedToEvict(items, "t0")).toEqual([]);
  });
});

describe("terminalsReducer", () => {
  it("bumps lastUsed when a terminal is shown", () => {
    const state = { items: [info("a", 1), info("b", 2)], clock: 2 };
    const next = terminalsReducer(state, terminalShown("a"));
    expect(next.items.find((t) => t.key === "a")?.lastUsed).toBe(3);
  });
});

describe("openTerminal", () => {
  it("marks the terminal open once the host has opened it", async () => {
    const store = testStore();
    await store.dispatch(openTerminal({ key: "attach:a", title: "a", spec: attach("a") }));
    expect(store.getState().terminals.items[0]?.state).toBe("open");
  });

  it("does not open a terminal that is already open", async () => {
    const terminals = fakeHost();
    const store = testStore({ terminals });
    await store.dispatch(openTerminal({ key: "attach:a", title: "a", spec: attach("a") }));
    await store.dispatch(openTerminal({ key: "attach:a", title: "a", spec: attach("a") }));
    expect(terminals.open).toHaveBeenCalledTimes(1);
  });

  it("records the exit status the host reports", async () => {
    const terminals = fakeHost();
    const store = testStore({ terminals });
    await store.dispatch(openTerminal({ key: "attach:a", title: "a", spec: attach("a") }));
    terminals.exits.get("attach:a")?.(0);
    expect(store.getState().terminals.items[0]).toMatchObject({ state: "exited", exitCode: 0 });
  });

  it("reopens an exited terminal with a new generation", async () => {
    const terminals = fakeHost();
    const store = testStore({ terminals });
    await store.dispatch(openTerminal({ key: "attach:a", title: "a", spec: attach("a") }));
    const first = store.getState().terminals.items[0]?.generation;
    terminals.exits.get("attach:a")?.(0);
    await store.dispatch(openTerminal({ key: "attach:a", title: "a", spec: attach("a") }));
    expect(store.getState().terminals.items[0]?.generation).not.toBe(first);
  });

  it("closes the least recently shown attach terminal beyond the limit", async () => {
    const terminals = fakeHost();
    const store = testStore({ terminals });
    for (let i = 0; i <= maxAttached; i++) {
      await store.dispatch(openTerminal({ key: `attach:${i}`, title: `${i}`, spec: attach(`${i}`) }));
    }
    expect(terminals.close).toHaveBeenCalledWith("attach:0");
  });

  it("shows a notice when the host fails to open", async () => {
    const terminals = fakeHost();
    terminals.open = async () => Promise.reject({ code: "claude_missing", message: "no claude" });
    const store = testStore({ terminals });
    await store.dispatch(openTerminal({ key: "attach:a", title: "a", spec: attach("a") }));
    expect(store.getState().ui.notice?.message).toBe("no claude");
  });
});

describe("closeTerminal", () => {
  it("forgets the terminal", async () => {
    const store = testStore();
    await store.dispatch(openTerminal({ key: "attach:a", title: "a", spec: attach("a") }));
    await store.dispatch(closeTerminal("attach:a"));
    expect(store.getState().terminals.items).toEqual([]);
  });
});
