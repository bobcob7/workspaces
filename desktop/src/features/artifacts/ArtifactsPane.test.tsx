import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Artifact, type ArtifactContent, type ArtifactSummary, type CommentList } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { ArtifactsPane } from "./ArtifactsPane";
import { artifactSelected, loadArtifacts } from "./artifactsSlice";

const summary = (overrides: Partial<ArtifactSummary>): ArtifactSummary => ({
  id: "art-00000002",
  reference: "@artifact:art-00000002",
  name: "login-error.png",
  title: null,
  kind: "image",
  contentType: "image/png",
  latest: 1,
  size: 126,
  createdAt: "2026-11-23T18:00:00Z",
  updatedAt: "2026-11-23T18:00:00Z",
  createdBy: { kind: "person", workspace: null, name: null },
  threads: { open: 0, resolved: 0, detached: 0 },
  ...overrides,
});

const image = summary({});
const log = summary({ id: "art-00000003", name: "build.log", kind: "text", contentType: "text/plain" });
const pitch = summary({
  id: "art-00000001",
  name: "pitch.md",
  title: "Pitch: cache sharing",
  kind: "markdown",
  contentType: "text/markdown; charset=utf-8",
  latest: 2,
});

const asArtifact = (item: ArtifactSummary, revisions: Artifact["revisions"]): Artifact => ({ ...item, revisions });

const revision = (rev: number, contentType: string) => ({
  rev,
  contentType,
  size: 126,
  sha256: "0".repeat(64),
  createdAt: "2026-11-23T18:00:00Z",
  createdBy: { kind: "person" as const, workspace: null, name: null },
  note: null,
});

const content = (overrides: Partial<ArtifactContent>): ArtifactContent => ({
  rev: 1,
  contentType: "image/png",
  size: 126,
  dataBase64: "iVBORw0KGgo=",
  ...overrides,
});

const noComments = (id: string): CommentList => ({ artifactId: id, latest: 1, threads: [] });

const renderPane = (items: readonly ArtifactSummary[], selected: string, overrides: Parameters<typeof fakeApi>[0] = {}) => {
  const store = testStore({ api: fakeApi(overrides) });
  store.dispatch(loadArtifacts.fulfilled(items, "req", undefined));
  store.dispatch(artifactSelected(selected));
  renderWithStore(<ArtifactsPane />, store);
  return store;
};

beforeEach(() => {
  vi.spyOn(api, "artifactComments").mockImplementation(async (id) => noComments(id));
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("ArtifactsPane", () => {
  it("asks for a selection when no artifact is selected", () => {
    renderWithStore(<ArtifactsPane />, testStore());

    expect(screen.getByText("Select an artifact")).toBeInTheDocument();
  });

  it("shows the latest revision of the selected image", async () => {
    const shown = asArtifact(image, [revision(1, "image/png")]);
    const read = vi.spyOn(api, "artifactRead").mockResolvedValue(content({ rev: 1 }));
    vi.spyOn(api, "artifactShow").mockResolvedValue(shown);
    renderPane([image], image.id);

    expect(await screen.findByRole("img", { name: "login-error.png" })).toBeInTheDocument();
    expect(read).toHaveBeenCalledWith(image.id, 1);
  });

  it("reads the revision chosen in the picker", async () => {
    const revisions = [revision(1, "image/png"), revision(2, "image/png")];
    vi.spyOn(api, "artifactShow").mockResolvedValue(asArtifact({ ...image, latest: 2 }, revisions));
    const read = vi.spyOn(api, "artifactRead").mockResolvedValue(content({ rev: 1 }));
    renderPane([image], image.id);
    await screen.findByRole("img", { name: "login-error.png" });

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Revision" }), "1");

    await waitFor(() => expect(read).toHaveBeenLastCalledWith(image.id, 1));
  });

  it("shows a text artifact as its text", async () => {
    vi.spyOn(api, "artifactShow").mockResolvedValue(asArtifact(log, [revision(1, "text/plain")]));
    vi.spyOn(api, "artifactRead").mockResolvedValue(
      content({ rev: 1, contentType: "text/plain", dataBase64: btoa("step 1 ok\nstep 2 failed") }),
    );
    renderPane([log], log.id);

    expect(await screen.findByText(/step 2 failed/)).toBeInTheDocument();
  });

  it("posts a comment on the whole image", async () => {
    vi.spyOn(api, "artifactShow").mockResolvedValue(asArtifact(image, [revision(1, "image/png")]));
    vi.spyOn(api, "artifactRead").mockResolvedValue(content({ rev: 1 }));
    const post = vi.spyOn(api, "artifactCommentAdd").mockResolvedValue({
      thread: { id: "t5", anchor: null, resolved: false, resolvedAt: null, resolvedBy: null, comments: [] },
      comment: { id: "c6", author: { kind: "person", workspace: null, name: null }, body: "Looks wrong", createdAt: "2026-11-23T18:10:00Z" },
    });
    renderPane([image], image.id);
    await screen.findByRole("img", { name: "login-error.png" });

    await userEvent.type(screen.getByRole("textbox", { name: "Comment" }), "Looks wrong");
    await userEvent.click(screen.getByRole("button", { name: "Post" }));

    await waitFor(() => expect(post).toHaveBeenCalledWith(image.id, "Looks wrong", null, null));
  });

  it("highlights an open thread's quote in a Markdown artifact", async () => {
    const text = "# Pitch\n\nToday the cache is shared.\n";
    vi.spyOn(api, "artifactShow").mockResolvedValue(
      asArtifact(pitch, [revision(1, "text/markdown; charset=utf-8"), revision(2, "text/markdown; charset=utf-8")]),
    );
    vi.spyOn(api, "artifactRead").mockResolvedValue(
      content({ rev: 2, contentType: "text/markdown; charset=utf-8", dataBase64: btoa(text) }),
    );
    vi.spyOn(api, "artifactComments").mockResolvedValue({
      artifactId: pitch.id,
      latest: 2,
      threads: [
        {
          id: "t1",
          anchor: { rev: 2, start: 0, end: 16, lineStart: 3, lineEnd: 3, quote: "cache is shared", prefix: "", suffix: ".", state: "anchored" },
          resolved: false,
          resolvedAt: null,
          resolvedBy: null,
          comments: [{ id: "c1", author: { kind: "person", workspace: null, name: null }, body: "Shared how?", createdAt: "2026-11-23T18:20:00Z" }],
        },
      ],
    });
    renderPane([pitch], pitch.id);

    expect(await screen.findByText("Shared how?")).toBeInTheDocument();
    expect(document.querySelector('mark[data-thread="t1"]')).toHaveTextContent("cache is shared");
  });

  it("offers the whole-artifact comment with the class the phone layout sizes", async () => {
    const markdown = "text/markdown; charset=utf-8";
    vi.spyOn(api, "artifactShow").mockResolvedValue(asArtifact(pitch, [revision(1, markdown), revision(2, markdown)]));
    vi.spyOn(api, "artifactRead").mockResolvedValue(content({ rev: 2, contentType: markdown, dataBase64: btoa("# Pitch\n") }));
    renderPane([pitch], pitch.id);

    expect(await screen.findByRole("button", { name: "Comment on the whole artifact" })).toHaveClass("wholeComment");
  });

  it("removes the artifact after the user confirms the delete", async () => {
    vi.spyOn(api, "artifactShow").mockResolvedValue(asArtifact(image, [revision(1, "image/png")]));
    vi.spyOn(api, "artifactRead").mockResolvedValue(content({ rev: 1 }));
    const remove = vi.fn(async () => undefined);
    const store = renderPane([image], image.id, { artifactRemove: remove });
    await screen.findByRole("img", { name: "login-error.png" });

    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(remove).toHaveBeenCalledWith(image.id));
    await waitFor(() => expect(store.getState().artifacts.selected).toBeNull());
  });

  it("shows the error when the artifact can't be read", async () => {
    vi.spyOn(api, "artifactShow").mockRejectedValue({ code: "artifact_not_found", message: "no such artifact" });
    renderPane([image], image.id);

    expect(await screen.findByRole("alert")).toHaveTextContent("no such artifact");
  });
});
