import type { ClipboardEvent, DragEvent } from "react";
import { useState } from "react";
import { api, toAppError } from "../../api";
import { useAppDispatch } from "../../store";
import { noticeShown } from "../ui/uiSlice";
import { loadArtifacts } from "./artifactsSlice";
import { imagesFromClipboard, uploadFiles } from "./upload";

/** `text` with the characters from `start` to `end` replaced by `inserted`. */
export const insertAt = (value: string, start: number, end: number, inserted: string): string =>
  value.slice(0, start) + inserted + value.slice(end);

/** `value` with `inserted` at the element's caret, or at the end when there is no caret. */
export const insertAtCaret = (value: string, element: HTMLTextAreaElement | null, inserted: string): string => {
  const start = element?.selectionStart ?? value.length;
  const end = element?.selectionEnd ?? start;
  return insertAt(value, start, end, inserted);
};

/** The text without its `@artifact:<id>` reference (and the `/rN` and space after it). */
export const removeReference = (text: string, id: string): string =>
  text.replace(new RegExp(`@artifact:${id}(?:/r[1-9]\\d{0,2})? ?`, "g"), "");

const hasFiles = (event: DragEvent): boolean => Array.from(event.dataTransfer.types).includes("Files");

export interface Attachments {
  /** An upload is running; Create and Send wait for it. */
  readonly busy: boolean;
  /** Uploads the files and inserts a reference for each through `insert`. */
  readonly attach: (files: readonly File[]) => Promise<void>;
  readonly onDragOver: (event: DragEvent) => void;
  readonly onDrop: (event: DragEvent) => void;
  readonly onPaste: (event: ClipboardEvent) => void;
}

/**
 * Attaching artifacts to a prompt: files from the picker, a drop or a paste
 * are stored with `ws artifact add`, and each gets an `@artifact:<id> `
 * reference inserted through `insert` (ADR 0078).
 */
export const useAttachments = (insert: (text: string) => void): Attachments => {
  const dispatch = useAppDispatch();
  const [busy, setBusy] = useState(false);

  const attach = async (files: readonly File[]): Promise<void> => {
    if (files.length === 0) {
      return;
    }
    setBusy(true);
    try {
      const stored = await uploadFiles(files, api);
      for (const artifact of stored) {
        insert(`@artifact:${artifact.id} `);
      }
    } catch (err: unknown) {
      dispatch(noticeShown({ severity: "error", message: toAppError(err).message }));
    } finally {
      setBusy(false);
      void dispatch(loadArtifacts());
    }
  };

  return {
    busy,
    attach,
    onDragOver: (event) => {
      if (hasFiles(event)) {
        event.preventDefault();
      }
    },
    onDrop: (event) => {
      event.preventDefault();
      void attach(Array.from(event.dataTransfer.files));
    },
    onPaste: (event) => {
      const images = imagesFromClipboard(event.nativeEvent);
      if (images.length === 0) {
        return;
      }
      event.preventDefault();
      void attach(images);
    },
  };
};
