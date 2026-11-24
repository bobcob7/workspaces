import type { JSX } from "react";
import styles from "./TextViewer.module.css";

export interface TextViewerProps {
  readonly text: string;
}

/** A text artifact as plain text, long lines wrapped. */
export const TextViewer = ({ text }: TextViewerProps): JSX.Element => <pre className={styles.text}>{text}</pre>;
