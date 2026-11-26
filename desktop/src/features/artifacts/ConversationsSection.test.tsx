import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ArtifactConversation } from "../../api";
import { entry, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { actionLabels, ConversationsSection } from "./ConversationsSection";

const row = (overrides: Partial<ArtifactConversation> = {}): ArtifactConversation => ({
  workspace: "fix-1",
  name: "fix",
  environment: null,
  removed: false,
  actions: ["read", "cited"],
  revs: [2],
  firstAt: "2026-11-23T18:00:00Z",
  lastAt: "2026-11-23T18:00:00Z",
  ...overrides,
});

const show = (rows: readonly ArtifactConversation[]) => {
  const store = testStore({ state: { agents: { ...testStore().getState().agents, entries: [entry({ key: "k-fix", workspaceId: "fix-1", name: "fix" })] } } });
  renderWithStore(<ConversationsSection rows={rows} />, store);
  return store;
};

describe("ConversationsSection", () => {
  it("lists each conversation with its actions", () => {
    show([row(), row({ workspace: "pitch-1", name: "pitch", actions: ["created", "revised"], revs: [1, 2, 3] })]);
    expect(screen.getByText("Conversations (2)")).toBeInTheDocument();
    expect(screen.getByText("cited")).toBeInTheDocument();
    expect(screen.getByText("revised r2 r3")).toBeInTheDocument();
  });

  it("opens the agent's conversation", async () => {
    const store = show([row()]);
    await userEvent.click(screen.getByRole("button", { name: "fix" }));
    expect(store.getState().ui.selectedKey).toBe("k-fix");
    expect(store.getState().ui.section).toBe("agents");
    expect(store.getState().ui.agentView).toBe("conversation");
  });

  it("greys out a removed workspace, with no link", () => {
    show([row({ removed: true })]);
    expect(screen.getByText("workspace removed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "fix" })).not.toBeInTheDocument();
  });

  it("shows the environment of a remote workspace", () => {
    show([row({ environment: "win-rig", removed: null })]);
    expect(screen.getByText("win-rig")).toBeInTheDocument();
  });

  it("starts collapsed past 5 rows, and is absent with none", () => {
    const { container } = renderWithStore(<ConversationsSection rows={Array.from({ length: 6 }, (_, i) => row({ workspace: `w${i}` }))} />, testStore());
    expect(container.querySelector("details")).not.toHaveAttribute("open");
    const empty = renderWithStore(<ConversationsSection rows={[]} />, testStore());
    expect(empty.container).toBeEmptyDOMElement();
  });

  it("labels revised with the revisions after the first", () => {
    expect(actionLabels(row({ actions: ["created", "revised"], revs: [1, 4] }))).toEqual(["created", "revised r4"]);
  });
});
