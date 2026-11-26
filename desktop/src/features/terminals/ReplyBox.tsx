import { Button, TextField, useMediaQuery } from "@mui/material";
import { useRef, useState, type FormEvent, type JSX } from "react";
import { AttachmentBar } from "../artifacts/AttachButton";
import { appendHint, citationsIn } from "../artifacts/references";
import { citeArtifacts } from "../conversation/conversationSlice";
import { insertAtSelection, useAttachments } from "../artifacts/useAttachments";
import { useAppDispatch } from "../../store";
import { messageKeys, sendMessage } from "./terminalsSlice";
import styles from "./ReplyBox.module.css";

export interface ReplyBoxProps {
  readonly terminalKey: string;
  readonly disabled: boolean;
  /** The local workspace a sent message's artifact references are recorded for; null records nothing (ADR 0084). */
  readonly citeWorkspaceId?: string | null;
}

export const ReplyBox = ({ terminalKey, disabled, citeWorkspaceId = null }: ReplyBoxProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const phone = useMediaQuery("(max-width:720px)");
  const [message, setMessage] = useState("");
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const attachments = useAttachments(
    (reference, selection) => setMessage((current) => insertAtSelection(current, selection, reference)),
    () => inputRef.current,
  );
  const cannotSend = disabled || attachments.busy || message.trim() === "";
  const submit = (event: FormEvent): void => {
    event.preventDefault();
    if (cannotSend) {
      return;
    }
    const refs = citationsIn(message);
    void dispatch(sendMessage({ key: terminalKey, text: messageKeys(appendHint(message)) })).then((result) => {
      if (citeWorkspaceId === null || refs.length === 0 || !sendMessage.fulfilled.match(result)) {
        return;
      }
      void dispatch(citeArtifacts({ workspaceId: citeWorkspaceId, refs }));
    });
    setMessage("");
  };
  return (
    <>
      <form className={styles.root} onSubmit={submit}>
        <TextField
          size="small"
          fullWidth
          multiline
          maxRows={6}
          placeholder={phone ? "Message" : "Message the agent (Enter to send, Shift+Enter for a new line)"}
          value={message}
          disabled={disabled}
          inputRef={inputRef}
          onChange={(e) => setMessage(e.target.value)}
          onPaste={attachments.onPaste}
          onDrop={attachments.onDrop}
          onDragOver={attachments.onDragOver}
          onKeyDown={(e) => {
            if (phone) {
              return;
            }
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              submit(e);
            }
          }}
          slotProps={{ htmlInput: { "aria-label": "Message" } }}
        />
        <Button
          type="submit"
          size={phone ? "small" : "medium"}
          className={styles.send}
          variant="contained"
          disabled={cannotSend}
        >
          Send
        </Button>
      </form>
      {!disabled && <AttachmentBar text={message} onTextChange={setMessage} attachments={attachments} />}
    </>
  );
};
