import type { Api, AppError, Artifact } from "../../api";

type ArtifactAdder = Pick<Api, "artifactAdd">;

/** The largest file the app uploads; `ws artifact add` refuses more (ADR 0078). */
export const maxUploadBytes = 10 * 1024 * 1024;

/** The file's bytes, base64 encoded without the data-URL prefix. */
export const fileToBase64 = (file: File): Promise<string> =>
  new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const url = typeof reader.result === "string" ? reader.result : "";
      resolve(url.slice(url.indexOf(",") + 1));
    };
    reader.onerror = () => reject(reader.error ?? new Error(`reading ${file.name}`));
    reader.readAsDataURL(file);
  });

const tooLarge = (name: string): AppError => ({
  code: "artifact_invalid",
  message: `${name} is over the 10 MiB limit`,
});

/** Uploads each file in order and returns the artifacts made. Refuses the batch before any upload when a file is too large. */
export const uploadFiles = async (files: readonly File[], adder: ArtifactAdder): Promise<Artifact[]> => {
  const oversized = files.find((file) => file.size > maxUploadBytes);
  if (oversized) {
    throw tooLarge(oversized.name);
  }
  const uploaded: Artifact[] = [];
  for (const file of files) {
    uploaded.push(await adder.artifactAdd(file.name, null, await fileToBase64(file)));
  }
  return uploaded;
};

const pad = (n: number): string => String(n).padStart(2, "0");

/** The name a pasted image without one is stored under: `pasted-yyyymmdd-hhmmss.png`. */
export const pastedName = (at: Date): string => {
  const date = `${at.getFullYear()}${pad(at.getMonth() + 1)}${pad(at.getDate())}`;
  const time = `${pad(at.getHours())}${pad(at.getMinutes())}${pad(at.getSeconds())}`;
  return `pasted-${date}-${time}.png`;
};

/** The images on a paste, named when the clipboard gave them no name. */
export const imagesFromClipboard = (event: ClipboardEvent, at: Date = new Date()): File[] => {
  const items = Array.from(event.clipboardData?.items ?? []);
  return items.flatMap((item) => {
    if (item.kind !== "file" || !item.type.startsWith("image/")) {
      return [];
    }
    const file = item.getAsFile();
    if (!file) {
      return [];
    }
    return [file.name === "" ? new File([file], pastedName(at), { type: file.type }) : file];
  });
};
