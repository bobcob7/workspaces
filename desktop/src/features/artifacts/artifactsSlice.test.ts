import { describe, expect, it, vi } from "vitest";
import type { Artifact, ArtifactSummary } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { artifactFilterChosen, artifactSelected, loadArtifacts, removeArtifact, uploadArtifacts } from "./artifactsSlice";

const asArtifact = (item: ArtifactSummary): Artifact => ({ ...item, revisions: [] });

const summary = (id: string): ArtifactSummary => ({
  id,
  reference: `@artifact:${id}`,
  name: `${id}.txt`,
  title: null,
  kind: "text",
  contentType: "text/plain",
  latest: 1,
  size: 5,
  createdAt: "2026-11-23T18:00:00Z",
  updatedAt: "2026-11-23T18:00:00Z",
  createdBy: { kind: "person", workspace: null, name: null },
  threads: { open: 0, resolved: 0, detached: 0 },
});

describe("artifacts", () => {
  it("fills the list when a load succeeds", async () => {
    const store = testStore({ api: fakeApi({ artifactList: vi.fn(async () => [summary("art-00000001")]) }) });
    await store.dispatch(loadArtifacts());
    expect(store.getState().artifacts.items.map((item) => item.id)).toEqual(["art-00000001"]);
    expect(store.getState().artifacts.loading).toBe(false);
  });

  it("keeps the error when a load is rejected", async () => {
    const error = { code: "ws_failed", message: "ws is gone" };
    const store = testStore({
      api: fakeApi({
        artifactList: vi.fn(async () => {
          throw error;
        }),
      }),
    });
    await store.dispatch(loadArtifacts());
    expect(store.getState().artifacts.error).toEqual(error);
  });

  it("selects the last artifact an upload made, once the list reloads", async () => {
    const uploaded = [summary("art-00000001"), summary("art-00000002")];
    const store = testStore({
      api: fakeApi({
        artifactAdd: vi.fn(async (name: string) => asArtifact(uploaded.find((item) => item.name === name) ?? uploaded[0] as ArtifactSummary)),
        artifactList: vi.fn(async () => uploaded),
      }),
    });
    const files = [new File(["a"], "art-00000001.txt"), new File(["b"], "art-00000002.txt")];
    await store.dispatch(uploadArtifacts(files));
    expect(store.getState().artifacts.selected).toBe("art-00000002");
    expect(store.getState().artifacts.uploads).toEqual([]);
    expect(store.getState().artifacts.items).toHaveLength(2);
  });

  it("clears the upload row when an upload is refused", async () => {
    const big = new File(["x"], "big.bin");
    Object.defineProperty(big, "size", { value: 10 * 1024 * 1024 + 1 });
    const store = testStore({ api: fakeApi({}) });
    await store.dispatch(uploadArtifacts([big]));
    expect(store.getState().artifacts.uploads).toEqual([]);
  });

  it("drops a removed artifact and clears the selection on it", async () => {
    const store = testStore({
      api: fakeApi({
        artifactList: vi.fn(async () => [summary("art-00000001")]),
        artifactRemove: vi.fn(async () => undefined),
      }),
    });
    await store.dispatch(loadArtifacts());
    store.dispatch(artifactSelected("art-00000001"));
    await store.dispatch(removeArtifact("art-00000001"));
    expect(store.getState().artifacts.items).toEqual([]);
    expect(store.getState().artifacts.selected).toBeNull();
  });

  it("keeps the chosen filter", () => {
    const store = testStore({ api: fakeApi({}) });
    store.dispatch(artifactFilterChosen("markdown"));
    expect(store.getState().artifacts.filter).toBe("markdown");
  });
});
