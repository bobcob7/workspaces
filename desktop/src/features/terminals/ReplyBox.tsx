import { Button, TextField } from "@mui/material";
import { useState, type FormEvent, type JSX } from "react";
import { useAppDispatch } from "../../store";
import { messageKeys, sendMessage } from "./terminalsSlice";
import styles from "./ReplyBox.module.css";

export interface ReplyBoxProps {
  readonly terminalKey: string;
  readonly disabled: boolean;
}

export const ReplyBox = ({ terminalKey, disabled }: ReplyBoxProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [message, setMessage] = useState("");
  const submit = (event: FormEvent): void => {
    event.preventDefault();
    if (message.trim() === "") {
      return;
    }
    void dispatch(sendMessage({ key: terminalKey, text: messageKeys(message) }));
    setMessage("");
  };
  return (
    <form className={styles.root} onSubmit={submit}>
      <TextField
        size="small"
        fullWidth
        multiline
        maxRows={6}
        placeholder="Message the agent (Enter to send, Shift+Enter for a new line)"
        value={message}
        disabled={disabled}
        onChange={(e) => setMessage(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
            submit(e);
          }
        }}
        slotProps={{ htmlInput: { "aria-label": "Message" } }}
      />
      <Button type="submit" variant="contained" disabled={disabled || message.trim() === ""}>
        Send
      </Button>
    </form>
  );
};
