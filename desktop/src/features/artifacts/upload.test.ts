import { describe, expect, it, vi } from "vitest";
import { failureMessage, fileToBase64, imagesFromClipboard, maxUploadBytes, pastedName, uploadFiles } from "./upload";

const artifact = { id: "art-00000001" };

describe("uploadFiles", () => {
  it("uploads each file with its name and base64 bytes", async () => {
    const artifactAdd = vi.fn(async () => artifact as never);
    const file = new File(["hello"], "a.txt", { type: "text/plain" });
    const uploaded = await uploadFiles([file], { artifactAdd });
    expect(artifactAdd).toHaveBeenCalledWith("a.txt", null, "aGVsbG8=");
    expect(uploaded).toEqual({ stored: [artifact], failures: [] });
  });

  it("keeps the files already stored and tries the rest when one fails", async () => {
    const artifactAdd = vi.fn(async (name: string) => {
      if (name === "b.txt") {
        throw { code: "ws_failed", message: "disk full" };
      }
      return { id: `art-${name}` } as never;
    });
    const files = ["a.txt", "b.txt", "c.txt"].map((name) => new File(["x"], name));
    const uploaded = await uploadFiles(files, { artifactAdd });
    expect(uploaded.stored).toEqual([{ id: "art-a.txt" }, { id: "art-c.txt" }]);
    expect(uploaded.failures).toEqual([{ name: "b.txt", error: { code: "ws_failed", message: "disk full" } }]);
    expect(failureMessage(uploaded.failures)).toBe("b.txt: disk full");
  });

  it("refuses a file over 10 MiB before uploading anything", async () => {
    const artifactAdd = vi.fn(async () => artifact as never);
    const big = new File(["x"], "big.bin");
    Object.defineProperty(big, "size", { value: maxUploadBytes + 1 });
    const small = new File(["hi"], "small.txt");
    await expect(uploadFiles([small, big], { artifactAdd })).rejects.toEqual({
      code: "artifact_invalid",
      message: "big.bin is over the 10 MiB limit",
    });
    expect(artifactAdd).not.toHaveBeenCalled();
  });
});

describe("fileToBase64", () => {
  it("returns the bytes without the data-URL prefix", async () => {
    expect(await fileToBase64(new File(["hello"], "a.txt"))).toBe("aGVsbG8=");
  });
});

describe("pastedName", () => {
  it("names a pasted image by its time", () => {
    expect(pastedName(new Date(2026, 10, 23, 9, 5, 7))).toBe("pasted-20261123-090507.png");
  });
});

describe("imagesFromClipboard", () => {
  const at = new Date(2026, 10, 23, 9, 5, 7);

  const paste = (items: readonly { kind: string; type: string; file: File | null }[]): ClipboardEvent =>
    ({
      clipboardData: {
        items: items.map((item) => ({ kind: item.kind, type: item.type, getAsFile: () => item.file })),
      },
    }) as unknown as ClipboardEvent;

  it("takes image files and skips text", () => {
    const named = new File(["png"], "shot.png", { type: "image/png" });
    const images = imagesFromClipboard(
      paste([
        { kind: "file", type: "image/png", file: named },
        { kind: "string", type: "text/plain", file: null },
      ]),
      at,
    );
    expect(images).toEqual([named]);
  });

  it("names a nameless pasted image", () => {
    const nameless = new File(["png"], "", { type: "image/png" });
    const [image] = imagesFromClipboard(paste([{ kind: "file", type: "image/png", file: nameless }]), at);
    expect(image?.name).toBe("pasted-20261123-090507.png");
    expect(image?.type).toBe("image/png");
  });
});
