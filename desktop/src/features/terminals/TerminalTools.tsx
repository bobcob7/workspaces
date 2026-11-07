import { Button, Dialog, DialogActions, DialogContent, DialogTitle, Link, TextField, useMediaQuery } from "@mui/material";
import { useEffect, useRef, useState, type JSX } from "react";
import { api } from "../../api";
import { useAppDispatch } from "../../store";
import { noticeShown } from "../ui/uiSlice";
import { copyText, extractUrls, readClipboard } from "./clipboard";
import { useTerminalRegistry } from "./context";
import styles from "./TerminalTools.module.css";

const PHONE_QUERY = "(max-width:720px)";

type Panel = "paste" | "text" | null;

interface PasteDialogProps {
  readonly terminalKey: string;
  readonly onClose: () => void;
}

const PasteDialog = ({ terminalKey, onClose }: PasteDialogProps): JSX.Element => {
  const registry = useTerminalRegistry();
  const fullScreen = useMediaQuery(PHONE_QUERY);
  const [value, setValue] = useState("");
  const send = (): void => {
    registry.paste(terminalKey, value);
    onClose();
  };
  return (
    <Dialog open fullScreen={fullScreen} fullWidth maxWidth="sm" onClose={onClose}>
      <DialogTitle>Paste into the terminal</DialogTitle>
      <DialogContent>
        <TextField
          autoFocus
          multiline
          fullWidth
          minRows={4}
          margin="dense"
          placeholder="Paste here (long-press, or Cmd/Ctrl+V)"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          slotProps={{ htmlInput: { "aria-label": "Text to paste", className: styles.mono } }}
        />
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="contained" disabled={value === ""} onClick={send}>
          Send
        </Button>
      </DialogActions>
    </Dialog>
  );
};

interface TextDialogProps {
  readonly terminalKey: string;
  readonly selected: string;
  readonly onClose: () => void;
  readonly onCopied: () => void;
}

const TextDialog = ({ terminalKey, selected, onClose, onCopied }: TextDialogProps): JSX.Element => {
  const registry = useTerminalRegistry();
  const fullScreen = useMediaQuery(PHONE_QUERY);
  const [text] = useState(() => registry.text(terminalKey));
  const urls = extractUrls(text);
  const box = useRef<HTMLTextAreaElement | null>(null);
  const attach = (element: HTMLTextAreaElement | null): void => {
    box.current = element;
    if (element === null) {
      return;
    }
    element.scrollTop = element.scrollHeight;
    const start = selected === "" ? -1 : text.indexOf(selected);
    if (start >= 0) {
      element.setSelectionRange(start, start + selected.length);
    }
  };
  const copyUrl = async (url: string): Promise<void> => {
    if (await copyText(url)) {
      onCopied();
    }
  };
  return (
    <Dialog open fullScreen={fullScreen} fullWidth maxWidth="md" onClose={onClose}>
      <DialogTitle>Terminal text</DialogTitle>
      <DialogContent>
        {urls.length > 0 && (
          <ul className={styles.links} aria-label="Links">
            {urls.map((url) => (
              <li key={url} className={styles.link}>
                <Link component="button" type="button" className={styles.url} onClick={() => void api.openExternal(url)}>
                  {url}
                </Link>
                <Button size="small" className={styles.tool} aria-label={`Copy ${url}`} onClick={() => void copyUrl(url)}>
                  Copy
                </Button>
              </li>
            ))}
          </ul>
        )}
        <TextField
          fullWidth
          multiline
          minRows={8}
          maxRows={24}
          margin="dense"
          inputRef={attach}
          value={text}
          slotProps={{ input: { readOnly: true }, htmlInput: { "aria-label": "Terminal text", className: styles.mono } }}
        />
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Close</Button>
      </DialogActions>
    </Dialog>
  );
};

export interface TerminalToolsProps {
  readonly terminalKey: string;
  readonly generation: number;
}

/** Copy, paste and select-text buttons for a terminal, for browsers and phones where xterm's own selection falls short. */
export const TerminalTools = ({ terminalKey, generation }: TerminalToolsProps): JSX.Element => {
  const registry = useTerminalRegistry();
  const dispatch = useAppDispatch();
  const [selection, setSelection] = useState("");
  const [panel, setPanel] = useState<Panel>(null);
  const [panelSelection, setPanelSelection] = useState("");
  useEffect(() => {
    setSelection("");
    return registry.onSelectionChange(terminalKey, () => setSelection(registry.selection(terminalKey)));
  }, [registry, terminalKey, generation]);
  const copied = (): void => {
    dispatch(noticeShown({ severity: "success", message: "Copied" }));
  };
  const copy = async (): Promise<void> => {
    if (await copyText(selection)) {
      copied();
      return;
    }
    setPanelSelection(selection);
    setPanel("text");
  };
  const paste = async (): Promise<void> => {
    const text = await readClipboard();
    if (text === null) {
      setPanel("paste");
      return;
    }
    registry.paste(terminalKey, text);
  };
  const close = (): void => setPanel(null);
  return (
    <div className={styles.root} role="toolbar" aria-label="Terminal tools">
      <Button size="small" className={styles.tool} disabled={selection === ""} onClick={() => void copy()}>
        Copy
      </Button>
      <Button size="small" className={styles.tool} onClick={() => void paste()}>
        Paste
      </Button>
      <Button
        size="small"
        className={styles.tool}
        onClick={() => {
          setPanelSelection(selection);
          setPanel("text");
        }}
      >
        Select text
      </Button>
      {panel === "paste" && <PasteDialog terminalKey={terminalKey} onClose={close} />}
      {panel === "text" && <TextDialog terminalKey={terminalKey} selected={panelSelection} onClose={close} onCopied={copied} />}
    </div>
  );
};
