import { describe, expect, it, vi } from "vitest";
import type { ArtifactSummary } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { citeArtifacts, fetchLinkedArtifacts } from "./conversationSlice";

const item = { id: "art-1a2b3c4d", name: "a.md" } as ArtifactSummary;

describe("fetchLinkedArtifacts", () => {
  it("keeps the last list when a read fails", async () => {
    const artifactList = vi.fn().mockResolvedValueOnce([item]).mockRejectedValueOnce({ code: "internal", message: "no ws" });
    const store = testStore({ api: fakeApi({ artifactList }) });
    await store.dispatch(fetchLinkedArtifacts("fix-1"));
    await store.dispatch(fetchLinkedArtifacts("fix-1"));
    expect(store.getState().conversations.artifactsByWorkspace["fix-1"]).toEqual({ items: [item], loaded: true, loading: false });
  });

  it("doesn't start a second read while one is in flight", async () => {
    let finish: (items: ArtifactSummary[]) => void = () => undefined;
    const artifactList = vi.fn(() => new Promise<ArtifactSummary[]>((resolve) => (finish = resolve)));
    const store = testStore({ api: fakeApi({ artifactList }) });
    const first = store.dispatch(fetchLinkedArtifacts("fix-1"));
    void store.dispatch(fetchLinkedArtifacts("fix-1"));
    finish([item]);
    await first;
    expect(artifactList).toHaveBeenCalledTimes(1);
  });
});

describe("citeArtifacts", () => {
  it("reads the strip again after recording", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(citeArtifacts({ workspaceId: "fix-1", refs: ["@artifact:art-1a2b3c4d"] }));
    expect(api.artifactList).toHaveBeenCalledWith(null, null, "fix-1");
  });

  it("drops a failure without reading the strip again", async () => {
    const api = fakeApi({ artifactCite: vi.fn().mockRejectedValue({ code: "internal", message: "no ws" }) });
    const store = testStore({ api });
    await expect(store.dispatch(citeArtifacts({ workspaceId: "fix-1", refs: ["@artifact:art-1a2b3c4d"] }))).resolves.toBeDefined();
    expect(api.artifactList).not.toHaveBeenCalled();
  });
});
