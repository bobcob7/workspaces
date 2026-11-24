import { Alert, Button } from "@mui/material";
import { useState, type JSX } from "react";
import type { EnrollmentCode as Code } from "../../api";
import styles from "./EnrollmentCode.module.css";

/** The clipboard note of D5: the code is a bearer secret until it is used. */
export const clipboardNote =
  "The code stays in your clipboard until you copy something else. Anyone who reads it first can enroll a machine; Rotate cancels an unused code.";

const canCopy = (): boolean => typeof navigator !== "undefined" && navigator.clipboard !== undefined;

interface LineProps {
  readonly label: string;
  readonly text: string;
  readonly onCopyFailed: () => void;
}

const Line = ({ label, text, onCopyFailed }: LineProps): JSX.Element => {
  const copy = async (): Promise<void> => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      onCopyFailed();
    }
  };
  return (
    <div className={styles.step}>
      <span className={styles.label}>{label}</span>
      <div className={styles.line}>
        <code className={styles.box} aria-label={label}>
          {text}
        </code>
        {canCopy() && (
          <Button size="small" aria-label={`Copy ${label}`} onClick={() => void copy()}>
            Copy
          </Button>
        )}
      </div>
    </div>
  );
};

interface EnrollmentCodeProps {
  readonly code: Code;
}

/** The one-time code and the two commands that use it. The caller keeps the code in component state only. */
export const EnrollmentCode = ({ code }: EnrollmentCodeProps): JSX.Element => {
  const [copyFailed, setCopyFailed] = useState(false);
  const failed = (): void => setCopyFailed(true);
  return (
    <div className={styles.root}>
      {code.warnings.map((warning) => (
        <Alert key={warning} severity="warning">
          {warning}
        </Alert>
      ))}
      <Line label="Enrollment code" text={code.code} onCopyFailed={failed} />
      <Line label="On the machine, enroll it" text={`ws env agent enroll ${code.code}`} onCopyFailed={failed} />
      <Line label="Then run the agent" text="ws env agent run" onCopyFailed={failed} />
      {(!canCopy() || copyFailed) && <p className={styles.note}>Select the text to copy it.</p>}
      <p className={styles.note}>{clipboardNote}</p>
    </div>
  );
};
