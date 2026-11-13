import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { entry, fakeApi, fakeHost, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchEntries } from "./agentsSlice";
import { EntryPane } from "./EntryPane";

vi.mock("../terminals/TerminalView", () => ({
  TerminalView: ({ terminalKey }: { readonly terminalKey: string }) => <div data-testid="terminal">{terminalKey}</div>,
}));

const shellWorkspace = entry({
  key: "this-is-a-test-d8efb24f",
  kind: "shell",
  name: "This is a Test",
  workspaceId: "this-is-a-test-d8efb24f",
  sessionId: null,
  path: "/w/this-is-a-test-d8efb24f",
  status: "running",
});

describe("EntryPane", () => {
  it("shows the workspace's display name", () => {
    renderWithStore(<EntryPane entry={shellWorkspace} />, testStore());
    expect(screen.getByRole("heading", { name: "This is a Test" })).toBeInTheDocument();
  });

  it("renames the workspace without touching its directory", async () => {
    const api = fakeApi();
    renderWithStore(<EntryPane entry={shellWorkspace} />, testStore({ api }));
    await userEvent.click(screen.getByRole("button", { name: "Rename" }));
    const field = screen.getByLabelText("Name");
    expect(field).toHaveValue("This is a Test");
    expect(screen.getByText("The directory stays this-is-a-test-d8efb24f.")).toBeInTheDocument();
    await userEvent.clear(field);
    await userEvent.type(field, "  Login fix ");
    await userEvent.click(screen.getByRole("button", { name: "Rename" }));
    expect(api.renameWorkspace).toHaveBeenCalledWith("this-is-a-test-d8efb24f", "Login fix");
  });

  it("doesn't rename to an empty name", async () => {
    const api = fakeApi();
    renderWithStore(<EntryPane entry={shellWorkspace} />, testStore({ api }));
    await userEvent.click(screen.getByRole("button", { name: "Rename" }));
    await userEvent.clear(screen.getByLabelText("Name"));
    expect(screen.getByRole("button", { name: "Rename" })).toBeDisabled();
    expect(api.renameWorkspace).not.toHaveBeenCalled();
  });

  it("offers no rename for a directory ws doesn't know", () => {
    const unknown = entry({ key: "path:/w/stray", kind: "unknown", workspaceId: null, sessionId: null, name: "stray" });
    renderWithStore(<EntryPane entry={unknown} />, testStore());
    expect(screen.queryByRole("button", { name: "Rename" })).not.toBeInTheDocument();
  });

  it("hides a visible workspace with ws hide", async () => {
    const api = fakeApi();
    renderWithStore(<EntryPane entry={shellWorkspace} />, testStore({ api }));
    expect(screen.queryByText("Hidden")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Hide" }));
    expect(api.setWorkspaceHidden).toHaveBeenCalledWith("this-is-a-test-d8efb24f", true);
  });

  it("marks a hidden workspace and unhides it", async () => {
    const api = fakeApi();
    renderWithStore(<EntryPane entry={{ ...shellWorkspace, hidden: true }} />, testStore({ api }));
    expect(screen.getByText("Hidden")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Unhide" }));
    expect(api.setWorkspaceHidden).toHaveBeenCalledWith("this-is-a-test-d8efb24f", false);
  });

  it("offers no Hide outside a workspace", () => {
    const session = entry({ key: "session:abc", workspaceId: null, sessionId: "abc" });
    renderWithStore(<EntryPane entry={session} />, testStore());
    expect(screen.queryByRole("button", { name: "Hide" })).not.toBeInTheDocument();
  });

  it("shows a blocked loop run's state and no Hide button", () => {
    const run = entry({ ...shellWorkspace, hidden: false, loopRun: { loop: "deps-bump", attention: "blocked" } });
    renderWithStore(<EntryPane entry={run} />, testStore());
    expect(screen.getByText("Blocked")).toBeInTheDocument();
    expect(screen.getByText("Loop deps-bump")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Hide" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Unhide" })).not.toBeInTheDocument();
  });

  it("shows a loop run that needs attention without a Hide button", () => {
    const run = entry({ ...shellWorkspace, loopRun: { loop: "status-check", attention: "needs_attention" } });
    renderWithStore(<EntryPane entry={run} />, testStore());
    expect(screen.getByText("Needs attention")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Hide" })).not.toBeInTheDocument();
  });

  it("shows a loop run that needs no attention with the usual Hide button", () => {
    renderWithStore(<EntryPane entry={{ ...shellWorkspace, loopRun: { loop: "deps-bump", attention: null } }} />, testStore());
    expect(screen.getByText("Loop deps-bump")).toBeInTheDocument();
    expect(screen.queryByText("Blocked")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Hide" })).toBeInTheDocument();
  });

  describe("a workspace with children", () => {
    const root = entry({ key: "root", workspaceId: "root", name: "fix-login", path: "/w/root", status: "working" });
    const child = entry({ key: "c", workspaceId: "c", name: "login-tests", parentId: "root", status: "working" });
    const review = entry({
      key: "r",
      workspaceId: "r",
      name: "review-51",
      parentId: "root",
      hidden: true,
      status: "idle",
      review: { repo: "github.com/o/r", ref: "abc", requester: "root", profiles: [], pr: null },
    });
    const grandchild = entry({ key: "g", workspaceId: "g", name: "deep", parentId: "c" });
    const loaded = (api = fakeApi()): ReturnType<typeof testStore> => {
      const store = testStore({ api });
      store.dispatch(fetchEntries.fulfilled({ entries: [root, child, review, grandchild], warnings: [] }, "req"));
      return store;
    };

    it("lists its children, which open on click", async () => {
      const store = loaded();
      renderWithStore(<EntryPane entry={root} />, store);
      const card = screen.getByRole("region", { name: "Started from this workspace" });
      expect(within(card).getByText("login-tests")).toBeInTheDocument();
      expect(within(card).getByText("review-51")).toBeInTheDocument();
      expect(within(card).getByText("hidden")).toBeInTheDocument();
      expect(within(card).queryByText("deep")).not.toBeInTheDocument();
      await userEvent.click(within(card).getAllByRole("button", { name: "Open" })[0]!);
      expect(store.getState().ui.selectedKey).toBe("c");
    });

    it("links a child to its parent", async () => {
      const store = loaded();
      renderWithStore(<EntryPane entry={child} />, store);
      await userEvent.click(screen.getByRole("button", { name: "↳ fix-login" }));
      expect(store.getState().ui.selectedKey).toBe("root");
    });

    it("asks before removing everything it started, and removes them with it", async () => {
      const api = fakeApi();
      renderWithStore(<EntryPane entry={root} />, loaded(api));
      await userEvent.click(screen.getAllByRole("button", { name: "Remove" })[0]!);
      const dialog = screen.getByRole("dialog");
      expect(within(dialog).getByText("Remove fix-login and 3 workspaces it started?")).toBeInTheDocument();
      expect(within(dialog).getByText(/its findings are kept/)).toBeInTheDocument();
      await userEvent.click(within(dialog).getByRole("button", { name: "Remove 4 workspaces" }));
      expect(api.removeEntry).toHaveBeenCalledWith("root", "e8cdf5d8", false);
    });

    it("can keep what it started", async () => {
      const api = fakeApi();
      renderWithStore(<EntryPane entry={root} />, loaded(api));
      await userEvent.click(screen.getAllByRole("button", { name: "Remove" })[0]!);
      const dialog = screen.getByRole("dialog");
      await userEvent.click(within(dialog).getByRole("checkbox", { name: /Keep the workspaces it started/ }));
      await userEvent.click(within(dialog).getByRole("button", { name: "Remove 1 workspace" }));
      expect(api.removeEntry).toHaveBeenCalledWith("root", "e8cdf5d8", true);
    });

    it("has no children card without children", () => {
      renderWithStore(<EntryPane entry={grandchild} />, loaded());
      expect(screen.queryByRole("region", { name: "Started from this workspace" })).not.toBeInTheDocument();
    });
  });
});

describe("EntryPane opening a workspace", () => {
  const oneRepo = entry({
    key: "fix-login",
    repos: [{ id: "github.com/bobcob7/buff-bear", path: "buff-bear", ref: "main", refKind: "branch" }],
  });

  it("opens the workspace folder in the file manager", async () => {
    const api = fakeApi();
    renderWithStore(<EntryPane entry={entry()} />, testStore({ api }));
    await userEvent.click(screen.getByRole("button", { name: "Open..." }));
    await userEvent.click(screen.getByRole("menuitem", { name: "Open folder" }));
    expect(api.workspaceOpen).toHaveBeenCalledWith("fileManager", "/w/fix-login", []);
  });

  it("opens a one-repo workspace's repo in the editor", async () => {
    const api = fakeApi();
    renderWithStore(<EntryPane entry={oneRepo} />, testStore({ api }));
    await userEvent.click(screen.getByRole("button", { name: "Open..." }));
    const item = screen.getByRole("menuitem", { name: "Open in Editor" });
    expect(item.getAttribute("title")).toContain("/w/fix-login/buff-bear");
    await userEvent.click(item);
    expect(api.workspaceOpen).toHaveBeenCalledWith("editor", "/w/fix-login", ["buff-bear"]);
  });

  it("disables the open actions while the workspace is pending", async () => {
    renderWithStore(<EntryPane entry={entry({ kind: "pending" })} />, testStore());
    await userEvent.click(screen.getByRole("button", { name: "Open..." }));
    for (const name of ["Open in Editor", "Open folder", "Open terminal"]) {
      expect(screen.getByRole("menuitem", { name })).toHaveAttribute("aria-disabled", "true");
    }
  });

  it("the path link opens through the file manager setting", async () => {
    const api = fakeApi({ openExternal: vi.fn(async () => undefined) });
    renderWithStore(<EntryPane entry={entry()} />, testStore({ api }));
    await userEvent.click(screen.getByRole("button", { name: "/w/fix-login" }));
    expect(api.workspaceOpen).toHaveBeenCalledWith("fileManager", "/w/fix-login", []);
    expect(api.openExternal).not.toHaveBeenCalled();
  });

  describe("on a phone", () => {
    const original = window.matchMedia;
    beforeEach(() => {
      window.matchMedia = ((query: string) => ({
        matches: true,
        media: query,
        onchange: null,
        addEventListener: () => undefined,
        removeEventListener: () => undefined,
        addListener: () => undefined,
        removeListener: () => undefined,
        dispatchEvent: () => false,
      })) as typeof window.matchMedia;
    });
    afterEach(() => {
      window.matchMedia = original;
    });

    it("moves the actions into a menu", async () => {
      const api = fakeApi();
      renderWithStore(<EntryPane entry={shellWorkspace} />, testStore({ api }));
      expect(screen.queryByRole("button", { name: "Rename" })).not.toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "More actions" }));
      await userEvent.click(screen.getByRole("menuitem", { name: "Hide" }));
      expect(api.setWorkspaceHidden).toHaveBeenCalledWith("this-is-a-test-d8efb24f", true);
    });

    it("lists the open actions first in More actions", async () => {
      renderWithStore(<EntryPane entry={shellWorkspace} />, testStore());
      await userEvent.click(screen.getByRole("button", { name: "More actions" }));
      const items = screen.getAllByRole("menuitem").map((item) => item.textContent);
      expect(items.slice(0, 3)).toEqual(["Open in Editor", "Open folder", "Open terminal"]);
    });

    it("keeps the path behind a collapsed details toggle", async () => {
      renderWithStore(<EntryPane entry={shellWorkspace} />, testStore());
      const toggle = screen.getByRole("button", { name: "Details" });
      expect(toggle).toHaveAttribute("aria-expanded", "false");
      await userEvent.click(toggle);
      expect(toggle).toHaveAttribute("aria-expanded", "true");
    });

    it("names the status dot, which has no label on a phone", () => {
      renderWithStore(<EntryPane entry={{ ...shellWorkspace, status: "needsInput" }} />, testStore());
      expect(screen.getByRole("img", { name: "Needs input" })).toBeInTheDocument();
    });
  });
});

describe("EntryPane's Open menu", () => {
  it("lists the three open actions in order", async () => {
    renderWithStore(<EntryPane entry={entry()} />, testStore());
    await userEvent.click(screen.getByRole("button", { name: "Open..." }));
    expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
      "Open in Editor",
      "Open folder",
      "Open terminal",
    ]);
  });

  it("opens a shell in the workspace directory and shows it", async () => {
    const terminals = fakeHost();
    const store = testStore({ terminals });
    renderWithStore(<EntryPane entry={shellWorkspace} />, store);
    await userEvent.click(screen.getByRole("button", { name: "Open..." }));
    await userEvent.click(screen.getByRole("menuitem", { name: "Open terminal" }));
    expect(terminals.open).toHaveBeenCalledWith(
      "dir:this-is-a-test-d8efb24f",
      { kind: "dir", cwd: "/w/this-is-a-test-d8efb24f" },
      expect.any(Function),
    );
    expect(store.getState().ui.agentView).toBe("shell");
    expect(screen.getByTestId("terminal")).toHaveTextContent("dir:this-is-a-test-d8efb24f");
  });

  it("switches to an open shell instead of opening another", async () => {
    const terminals = fakeHost();
    renderWithStore(<EntryPane entry={shellWorkspace} />, testStore({ terminals }));
    await userEvent.click(screen.getByRole("button", { name: "Open..." }));
    await userEvent.click(screen.getByRole("menuitem", { name: "Open terminal" }));
    await userEvent.click(screen.getByRole("button", { name: "Open..." }));
    await userEvent.click(screen.getByRole("menuitem", { name: "Open terminal" }));
    expect(terminals.open).toHaveBeenCalledTimes(1);
  });

  it("offers only Open terminal in the web app", async () => {
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
    renderWithStore(<EntryPane entry={shellWorkspace} />, testStore());
    await userEvent.click(screen.getByRole("button", { name: "Open..." }));
    expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Open terminal"]);
  });
});
