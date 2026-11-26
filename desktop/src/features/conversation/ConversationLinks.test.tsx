import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ArtifactSummary, ConversationItem } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { appendHint } from "../artifacts/references";
import { ConversationView } from "./ConversationView";

const summary = (id: string, overrides: Partial<ArtifactSummary> = {}): ArtifactSummary => ({
  id,
  reference: `@artifact:${id}`,
  name: `${id}.md`,
  title: null,
  kind: "markdown",
  contentType: "text/markdown; charset=utf-8",
  latest: 2,
  size: 10,
  createdAt: "2026-11-23T18:00:00Z",
  updatedAt: "2026-11-23T18:00:00Z",
  createdBy: { kind: "person", workspace: null, name: null },
  threads: { open: 0, resolved: 0, detached: 0 },
  ...overrides,
});

const pitch = summary("art-1a2b3c4d", { title: "Pitch: cache sharing" });

const show = (items: readonly ConversationItem[], overrides: Parameters<typeof fakeApi>[0] = {}, linkWorkspaceId: string | null = null) => {
  const readTranscript = vi
    .fn()
    .mockResolvedValueOnce({ items, next: 100, reset: false, dropped: 0, found: true })
    .mockResolvedValue({ items: [], next: 100, reset: false, dropped: 0, found: true });
  const api = fakeApi({ readTranscript, artifactList: vi.fn(async () => [pitch]), ...overrides });
  const store = testStore({ api });
  renderWithStore(
    <ConversationView sessionId="s1" workspaceId={null} status="idle" detail={null} onShowTerminal={vi.fn()} linkWorkspaceId={linkWorkspaceId} />,
    store,
  );
  return { store, api };
};

const reply = (text: string): ConversationItem => ({ kind: "assistant", id: "a", at: null, text });

describe("ConversationView artifact links", () => {
  it("links a reference in a reply to the artifact, by title", async () => {
    show([reply("Read @artifact:art-1a2b3c4d/r2 first.")]);
    expect(await screen.findByRole("link", { name: "Pitch: cache sharing (r2)" })).toBeInTheDocument();
  });

  it("opens the artifact and revision in the app, not the browser", async () => {
    const { store, api } = show([reply("Read @artifact:art-1a2b3c4d/r2 first.")]);
    await userEvent.click(await screen.findByRole("link", { name: "Pitch: cache sharing (r2)" }));
    expect(store.getState().artifacts.selected).toBe("art-1a2b3c4d");
    expect(store.getState().artifacts.requestedRev).toBe(2);
    expect(store.getState().ui.section).toBe("artifacts");
    expect(api.openExternal).not.toHaveBeenCalled();
  });

  it("doesn't link a reference in code", async () => {
    show([reply("Run `ws machine artifact get @artifact:art-1a2b3c4d`.")]);
    expect(await screen.findByText("ws machine artifact get @artifact:art-1a2b3c4d", { selector: "code" })).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("shows a link written as ws-artifact: in the text as plain text", async () => {
    show([reply("[click](ws-artifact:art-1a2b3c4d)")]);
    expect(await screen.findByText("click")).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("strikes through an id the store doesn't have", async () => {
    show([reply("Gone: @artifact:art-00c0ffee.")]);
    const missing = await screen.findByTitle("not in the store");
    expect(missing).toHaveTextContent("@artifact:art-00c0ffee");
  });

  it("collapses the hint line into a row of artifacts", async () => {
    show([{ kind: "user", id: "u", at: null, text: appendHint("Fix @artifact:art-1a2b3c4d") }]);
    expect(await screen.findByText("Artifacts:")).toBeInTheDocument();
    expect(screen.queryByText(/ws machine artifact get <id>/)).not.toBeInTheDocument();
    expect(await screen.findAllByRole("link", { name: "Pitch: cache sharing" })).toHaveLength(2);
  });

  it("links references in a tool result", async () => {
    show([
      { kind: "toolUse", id: "t", at: null, toolUseId: "t1", name: "Bash", summary: "add", input: "ws machine artifact add" },
      { kind: "toolResult", id: "r", at: null, toolUseId: "t1", text: '{"reference": "@artifact:art-1a2b3c4d"}', isError: false },
    ]);
    await userEvent.click(await screen.findByText("add"));
    expect(screen.getByRole("link", { name: "Pitch: cache sharing" })).toBeInTheDocument();
  });
});

describe("Artifacts in this conversation", () => {
  const linked = [
    summary("art-1a2b3c4d", { title: "Pitch", conversation: { actions: ["read", "cited"], revs: [2], firstAt: "t", lastAt: "t" } }),
    ...Array.from({ length: 9 }, (_, i) => summary(`art-0000000${i}`)),
  ];

  it("lists the workspace's artifacts, up to 8, then +n more", async () => {
    const artifactList = vi.fn(async (_k: unknown, _w: unknown, conversation: string | null = null) => (conversation === null ? [pitch] : linked));
    show([], { artifactList }, "fix-1");
    const strip = await screen.findByRole("navigation", { name: "Artifacts in this conversation" });
    expect(strip).toBeInTheDocument();
    expect(artifactList).toHaveBeenCalledWith(null, null, "fix-1");
    expect(screen.getByRole("button", { name: /Pitch/ })).toHaveAttribute("title", "read, cited (r2)");
    await userEvent.click(screen.getByRole("button", { name: "+2 more" }));
    expect(screen.getByRole("button", { name: /art-00000008/ })).toBeInTheDocument();
  });

  it("opens the artifact at the revision the conversation touched", async () => {
    const artifactList = vi.fn(async (_k: unknown, _w: unknown, conversation: string | null = null) => (conversation === null ? [pitch] : linked));
    const { store } = show([], { artifactList }, "fix-1");
    await userEvent.click(await screen.findByRole("button", { name: /Pitch/ }));
    expect(store.getState().artifacts.selected).toBe("art-1a2b3c4d");
    expect(store.getState().artifacts.requestedRev).toBe(2);
  });

  it("is hidden when nothing is linked", async () => {
    const artifactList = vi.fn(async () => []);
    show([reply("hi")], { artifactList }, "fix-1");
    await screen.findByText("hi");
    await waitFor(() => expect(artifactList).toHaveBeenCalled());
    expect(screen.queryByRole("navigation", { name: "Artifacts in this conversation" })).not.toBeInTheDocument();
  });

  it("isn't shown without a workspace", async () => {
    const { api } = show([reply("hi")]);
    await screen.findByText("hi");
    expect(api.artifactList).not.toHaveBeenCalledWith(null, null, expect.anything());
  });
});
