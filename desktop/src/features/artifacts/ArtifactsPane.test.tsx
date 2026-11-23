import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, type Artifact, type ArtifactContent, type ArtifactSummary } from "../../api";
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

const renderPane = (items: readonly ArtifactSummary[], selected: string, overrides: Parameters<typeof fakeApi>[0] = {}) => {
  const store = testStore({ api: fakeApi(overrides) });
  store.dispatch(loadArtifacts.fulfilled(items, "req", undefined));
  store.dispatch(artifactSelected(selected));
  renderWithStore(<ArtifactsPane />, store);
  return store;
};

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
