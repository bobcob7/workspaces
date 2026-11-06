import { describe, expect, it, vi } from "vitest";
import { fakeApi, testStore } from "../../test/fixtures";
import { followTray } from "./tray";

describe("followTray", () => {
  it("shows the agent the tray menu selects, and resolves to an unsubscribe", async () => {
    let handler: (key: string) => void = () => undefined;
    const stop = vi.fn();
    const api = fakeApi({
      onTraySelect: vi.fn(async (h: (key: string) => void) => {
        handler = h;
        return stop;
      }),
    });
    const store = testStore({ api });
    const unlisten = await store.dispatch(followTray());
    expect(unlisten).toBe(stop);
    handler("fix-login");
    expect(store.getState().ui).toMatchObject({ section: "agents", selectedKey: "fix-login" });
  });
});
