import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Artifact, ArtifactSummary } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { ArtifactsActions } from "./ArtifactsActions";
import { ArtifactsList, visibleArtifacts } from "./ArtifactsList";

const summary = (overrides: Partial<ArtifactSummary>): ArtifactSummary => ({
  id: "art-00000001",
  reference: "@artifact:art-00000001",
  name: "pitch.md",
  title: null,
  kind: "markdown",
  contentType: "text/markdown; charset=utf-8",
  latest: 1,
  size: 2311,
  createdAt: "2026-11-23T18:00:00Z",
  updatedAt: "2026-11-23T18:00:00Z",
  createdBy: { kind: "person", workspace: null, name: null },
  threads: { open: 0, resolved: 0, detached: 0 },
  ...overrides,
});

const asArtifact = (item: ArtifactSummary): Artifact => ({ ...item, revisions: [] });

const login = summary({ id: "art-00000002", name: "login-error.png", kind: "image", contentType: "image/png" });
const pitch = summary({
  id: "art-00000001",
  title: "Pitch: cache sharing",
  latest: 2,
  threads: { open: 2, resolved: 1, detached: 1 },
  createdBy: { kind: "agent", workspace: "pitch-ab12cd34", name: "pitch" },
});
const log = summary({ id: "art-00000003", name: "build.log", kind: "text", contentType: "text/plain" });

const renderList = (items: readonly ArtifactSummary[], extra: Parameters<typeof fakeApi>[0] = {}) => {
  const store = testStore({ api: fakeApi({ artifactList: vi.fn(async () => items), ...extra }) });
  renderWithStore(
    <>
      <ArtifactsActions />
      <ArtifactsList />
    </>,
    store,
  );
  return store;
};

describe("visibleArtifacts", () => {
  const items = [login, pitch, log];

  it("keeps every artifact under All and an empty search", () => {
    expect(visibleArtifacts(items, "all", "")).toEqual(items);
  });

  it("keeps one kind under its chip", () => {
    expect(visibleArtifacts(items, "markdown", "")).toEqual([pitch]);
  });

  it("matches the search against the name and the title", () => {
    expect(visibleArtifacts(items, "all", "CACHE")).toEqual([pitch]);
    expect(visibleArtifacts(items, "all", "build")).toEqual([log]);
    expect(visibleArtifacts(items, "all", "nothing")).toEqual([]);
  });
});

describe("ArtifactsList", () => {
  it("loads the list when it opens, with the store empty", async () => {
    const artifactList = vi.fn(async () => [pitch, login]);
    const store = testStore({ api: fakeApi({ artifactList }) });
    renderWithStore(<ArtifactsList />, store);
    expect(store.getState().artifacts.items).toEqual([]);
    expect(await screen.findByRole("button", { name: /Pitch: cache sharing/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /login-error.png/ })).toBeInTheDocument();
    expect(artifactList).toHaveBeenCalledWith(null, null);
  });

  it("shows each artifact by its title, with its kind, revision and threads", async () => {
    renderList([login, pitch]);
    expect(await screen.findByRole("button", { name: /Pitch: cache sharing/ })).toHaveTextContent("r2");
    expect(screen.getByRole("button", { name: /Pitch: cache sharing/ })).toHaveTextContent("2 open · 1 det.");
    expect(screen.getByRole("button", { name: /login-error.png/ })).toHaveTextContent("you");
  });

  it("filters by kind when a chip is chosen", async () => {
    const store = renderList([login, pitch, log]);
    await screen.findByRole("button", { name: /build.log/ });
    await userEvent.click(screen.getByRole("button", { name: "Text" }));
    expect(store.getState().artifacts.filter).toBe("text");
    expect(screen.getByRole("list", { name: "Artifacts" })).toHaveTextContent("build.log");
    expect(screen.queryByText("Pitch: cache sharing")).not.toBeInTheDocument();
  });

  it("gives the chips and the search field the class the phone layout sizes", async () => {
    renderList([pitch]);
    await screen.findByRole("button", { name: /Pitch: cache sharing/ });

    expect(screen.getByRole("button", { name: "Text" })).toHaveClass("chip");
    expect(screen.getByRole("searchbox", { name: "Search artifacts" })).toHaveClass("search");
  });

  it("gives the Upload button the class the phone layout sizes", () => {
    renderWithStore(<ArtifactsActions />, testStore());

    expect(screen.getByRole("button", { name: "Upload" })).toHaveClass("upload");
  });

  it("selects a row when it is clicked", async () => {
    const store = renderList([pitch]);
    await userEvent.click(await screen.findByRole("button", { name: /Pitch: cache sharing/ }));
    expect(store.getState().artifacts.selected).toBe("art-00000001");
  });

  it("uploads files dropped on the list and selects the one made last", async () => {
    const artifactAdd = vi.fn(async () => asArtifact(pitch));
    const store = renderList([], { artifactAdd, artifactList: vi.fn(async () => [pitch]) });
    await screen.findByRole("button", { name: /Pitch: cache sharing/ });
    const file = new File(["# Pitch"], "pitch.md", { type: "text/markdown" });
    fireEvent.drop(screen.getByRole("list", { name: "Artifacts" }).closest("div") as HTMLElement, {
      dataTransfer: { files: [file], types: ["Files"] },
    });
    await waitFor(() => expect(store.getState().artifacts.selected).toBe("art-00000001"));
    expect(artifactAdd).toHaveBeenCalledWith("pitch.md", null, expect.any(String));
  });

  it("uploads an image pasted anywhere in the section", async () => {
    const artifactAdd = vi.fn(async () => asArtifact(login));
    const store = renderList([], { artifactAdd, artifactList: vi.fn(async () => [login]) });
    await screen.findByRole("button", { name: /login-error.png/ });
    const paste = new Event("paste") as ClipboardEvent;
    const image = new File(["png"], "", { type: "image/png" });
    Object.defineProperty(paste, "clipboardData", {
      value: { items: [{ kind: "file", type: "image/png", getAsFile: () => image }] },
    });
    window.dispatchEvent(paste);
    await waitFor(() => expect(artifactAdd).toHaveBeenCalled());
    expect(artifactAdd).toHaveBeenCalledWith(expect.stringMatching(/^pasted-\d{8}-\d{6}\.png$/), null, expect.any(String));
    expect(store.getState().artifacts.selected).toBe("art-00000002");
  });

  it("uploads files chosen in the picker", async () => {
    const artifactAdd = vi.fn(async () => asArtifact(log));
    renderList([], { artifactAdd, artifactList: vi.fn(async () => [log]) });
    await screen.findByRole("button", { name: /build.log/ });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, new File(["ok"], "build.log", { type: "text/plain" }));
    await waitFor(() => expect(artifactAdd).toHaveBeenCalledWith("build.log", null, "b2s="));
  });
});
