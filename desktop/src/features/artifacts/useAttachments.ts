import type { ClipboardEvent, DragEvent } from "react";
import { useState } from "react";
import { api, toAppError } from "../../api";
import { useAppDispatch } from "../../store";
import { noticeShown } from "../ui/uiSlice";
import { loadArtifacts } from "./artifactsSlice";
import { failureMessage, imagesFromClipboard, uploadFiles } from "./upload";

/** `text` with the characters from `start` to `end` replaced by `inserted`. */
export const insertAt = (value: string, start: number, end: number, inserted: string): string =>
  value.slice(0, start) + inserted + value.slice(end);

const separatorFor = (before: string, inserted: string): string => {
  if (before === "" || /\s$/.test(before) || /^\s/.test(inserted)) {
    return "";
  }
  return " ";
};

/** A caret or selection, as offsets into the text. */
export interface Selection {
  readonly start: number;
  readonly end: number;
}

/** The element's selection now, or null when there is no element. */
export const readSelection = (element: HTMLTextAreaElement | null): Selection | null =>
  element ? { start: element.selectionStart, end: element.selectionEnd } : null;

/** `value` with `inserted` at `selection`, or at the end when there is none. */
export const insertAtSelection = (value: string, selection: Selection | null, inserted: string): string => {
  const start = Math.min(selection?.start ?? value.length, value.length);
  const end = Math.min(selection?.end ?? start, value.length);
  const separator = separatorFor(value.slice(0, start), inserted);
  return insertAt(value, start, end, separator + inserted);
};

/** `value` with `inserted` at the element's caret, or at the end when there is no caret. */
export const insertAtCaret = (value: string, element: HTMLTextAreaElement | null, inserted: string): string =>
  insertAtSelection(value, readSelection(element), inserted);

/** The text without its `@artifact:<id>` reference (and the `/rN` and space after it). */
export const removeReference = (text: string, id: string): string =>
  text.replace(new RegExp(`@artifact:${id}(?:/r[1-9]\\d{0,2})? ?`, "g"), "");

const hasFiles = (event: DragEvent): boolean => Array.from(event.dataTransfer.types).includes("Files");

export interface Attachments {
  /** An upload is running; Create and Send wait for it. */
  readonly busy: boolean;
  /** Uploads the files and inserts their references, in order, through `insert` once. */
  readonly attach: (files: readonly File[]) => Promise<void>;
  readonly onDragOver: (event: DragEvent) => void;
  readonly onDrop: (event: DragEvent) => void;
  readonly onPaste: (event: ClipboardEvent) => void;
}

/**
 * Attaching artifacts to a prompt: files from the picker, a drop or a paste
 * are stored with `ws artifact add`, and each gets an `@artifact:<id> `
 * reference inserted through `insert` (ADR 0078). The references go in as one
 * text, at the caret `caretOf` gave when the attach began.
 */
export const useAttachments = (
  insert: (text: string, selection: Selection | null) => void,
  caretOf: () => HTMLTextAreaElement | null = () => null,
): Attachments => {
  const dispatch = useAppDispatch();
  const [busy, setBusy] = useState(false);

  const attach = async (files: readonly File[]): Promise<void> => {
    if (files.length === 0) {
      return;
    }
    const selection = readSelection(caretOf());
    setBusy(true);
    try {
      const { stored, failures } = await uploadFiles(files, api);
      if (stored.length > 0) {
        insert(stored.map((artifact) => `@artifact:${artifact.id} `).join(""), selection);
      }
      if (failures.length > 0) {
        dispatch(noticeShown({ severity: "error", message: failureMessage(failures) }));
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
