import { Button } from "@mui/material";
import { useRef, type ChangeEvent, type JSX } from "react";
import { findRefs } from "./references";
import { useAppSelector } from "../../store";
import type { Attachments } from "./useAttachments";
import { removeReference } from "./useAttachments";
import { artifactAccept } from "./ArtifactsActions";
import styles from "./AttachButton.module.css";

export interface AttachButtonProps {
  readonly onFiles: (files: readonly File[]) => void;
}

/** Attach: the file picker for artifacts a prompt refers to. */
export const AttachButton = ({ onFiles }: AttachButtonProps): JSX.Element => {
  const input = useRef<HTMLInputElement>(null);
  const onChange = (event: ChangeEvent<HTMLInputElement>): void => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    onFiles(files);
  };
  return (
    <>
      <Button size="small" variant="outlined" onClick={() => input.current?.click()}>
        Attach
      </Button>
      <input ref={input} type="file" multiple accept={artifactAccept} hidden onChange={onChange} />
    </>
  );
};

interface AttachmentChipsProps {
  readonly text: string;
  readonly onTextChange: (text: string) => void;
}

/** One chip per artifact the text refers to; ✕ removes the reference text. */
const AttachmentChips = ({ text, onTextChange }: AttachmentChipsProps): JSX.Element | null => {
  const items = useAppSelector((s) => s.artifacts.items);
  const ids = findRefs(text);
  if (ids.length === 0) {
    return null;
  }
  return (
    <div className={styles.chips}>
      {ids.map((id) => {
        const name = items.find((item) => item.id === id)?.name ?? id;
        return (
          <span key={id} className={styles.chip}>
            {name}
            <button
              type="button"
              className={styles.remove}
              aria-label={`Remove ${name}`}
              onClick={() => onTextChange(removeReference(text, id))}
            >
              ✕
            </button>
          </span>
        );
      })}
    </div>
  );
};

export interface AttachmentBarProps {
  readonly text: string;
  readonly onTextChange: (text: string) => void;
  readonly attachments: Attachments;
}

/** Attach, the upload state and the chips of the references in `text`. */
export const AttachmentBar = ({ text, onTextChange, attachments }: AttachmentBarProps): JSX.Element => (
  <div className={styles.bar}>
    <div className={styles.row}>
      <AttachButton onFiles={(files) => void attachments.attach(files)} />
      {attachments.busy && <span role="status">Uploading…</span>}
    </div>
    <AttachmentChips text={text} onTextChange={onTextChange} />
  </div>
);
