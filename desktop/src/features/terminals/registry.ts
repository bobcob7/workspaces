import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { Terminal } from "@xterm/xterm";
import type { Api, TermSpec } from "../../api";
import { copyText } from "./clipboard";
import type { TerminalHost } from "./host";

interface Handle {
  readonly term: Terminal;
  readonly fit: FitAddon;
  readonly element: HTMLDivElement;
  id: number | null;
  closed: boolean;
  opened: boolean;
}

/** Lines of scrollback `text` returns by default. */
const DEFAULT_TEXT_LINES = 200;

const isClipboardKey = (event: KeyboardEvent, hasSelection: boolean): boolean => {
  const key = event.key.toLowerCase();
  const mac = event.metaKey && !event.ctrlKey && !event.altKey;
  const shifted = event.ctrlKey && event.shiftKey && !event.metaKey && !event.altKey;
  if (key === "v") {
    return mac || shifted;
  }
  return key === "c" && hasSelection && (mac || shifted);
};

/**
 * The xterm key handler that leaves paste, and copy of a selection, to the
 * browser: it returns false for those keydowns, true for everything else.
 */
export const clipboardKeyHandler =
  (hasSelection: () => boolean, copy: () => void) =>
  (event: KeyboardEvent): boolean => {
    if (event.type !== "keydown" || !isClipboardKey(event, hasSelection())) {
      return true;
    }
    if (event.key.toLowerCase() === "c") {
      copy();
    }
    return false;
  };

const theme = {
  background: "#14161c",
  foreground: "#d8dbe2",
  cursor: "#d8dbe2",
  selectionBackground: "#3a4152",
};

/**
 * Owns each terminal's xterm.js instance and its pty. A terminal keeps its
 * element, and its scrollback, while it is hidden: views move the element
 * in and out of the page.
 */
export class TerminalRegistry implements TerminalHost {
  private readonly handles = new Map<string, Handle>();

  constructor(private readonly api: Api) {}

  async open(key: string, spec: TermSpec, onExit: (code: number | null) => void): Promise<void> {
    this.dispose(key);
    const term = new Terminal({
      fontFamily: '"SF Mono", Menlo, Monaco, "Cascadia Mono", monospace',
      fontSize: 13,
      cursorBlink: true,
      allowProposedApi: false,
      scrollback: 5000,
      theme,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon((_event, url) => void this.api.openExternal(url)));
    term.attachCustomKeyEventHandler(
      clipboardKeyHandler(
        () => term.hasSelection(),
        () => void copyText(term.getSelection()),
      ),
    );
    const element = document.createElement("div");
    element.style.width = "100%";
    element.style.height = "100%";
    const handle: Handle = { term, fit, element, id: null, closed: false, opened: false };
    this.handles.set(key, handle);
    term.onData((data) => {
      if (handle.id !== null && !handle.closed) {
        void this.api.terminalWrite(handle.id, data).catch(() => undefined);
      }
    });
    term.onResize(({ cols, rows }) => {
      if (handle.id !== null && !handle.closed) {
        void this.api.terminalResize(handle.id, cols, rows).catch(() => undefined);
      }
    });
    handle.id = await this.api.terminalOpen(spec, term.cols, term.rows, (event) => {
      if (event.event === "data") {
        term.write(event.data);
        return;
      }
      handle.closed = true;
      term.write(`\r\n\x1b[2m[exited${event.code === null ? "" : ` with status ${event.code}`}]\x1b[0m\r\n`);
      onExit(event.code);
    });
  }

  async close(key: string): Promise<void> {
    const handle = this.handles.get(key);
    this.dispose(key);
    if (handle !== undefined && handle.id !== null && !handle.closed) {
      await this.api.terminalClose(handle.id);
    }
  }

  async send(key: string, text: string): Promise<void> {
    const handle = this.handles.get(key);
    if (handle === undefined || handle.id === null || handle.closed) {
      throw new Error("The terminal isn't open");
    }
    await this.api.terminalWrite(handle.id, text);
  }

  /** The selected text, or "" when nothing is selected. */
  selection(key: string): string {
    return this.handles.get(key)?.term.getSelection() ?? "";
  }

  /** The last `lines` lines of the buffer: wrapped lines joined, trailing spaces trimmed. */
  text(key: string, lines = DEFAULT_TEXT_LINES): string {
    const term = this.handles.get(key)?.term;
    if (term === undefined) {
      return "";
    }
    const buffer = term.buffer.active;
    const rows: string[] = [];
    for (let i = 0; i < buffer.length; i += 1) {
      const line = buffer.getLine(i);
      if (line === undefined) {
        continue;
      }
      const text = line.translateToString(true);
      if (line.isWrapped && rows.length > 0) {
        rows[rows.length - 1] += text;
        continue;
      }
      rows.push(text);
    }
    const trimmed = rows.map((row) => row.trimEnd());
    while (trimmed.length > 0 && trimmed[trimmed.length - 1] === "") {
      trimmed.pop();
    }
    return trimmed.slice(-lines).join("\n");
  }

  /** Sends text to the pty as xterm's own paste does, in bracketed paste when the app asked for it. */
  paste(key: string, text: string): void {
    this.handles.get(key)?.term.paste(text);
  }

  /** Calls onChange when the selection changes; returns the unsubscribe. */
  onSelectionChange(key: string, onChange: () => void): () => void {
    const term = this.handles.get(key)?.term;
    if (term === undefined) {
      return () => undefined;
    }
    const subscription = term.onSelectionChange(onChange);
    return () => subscription.dispose();
  }

  /** Shows the terminal in container, sized to fit it. */
  mount(key: string, container: HTMLElement): void {
    const handle = this.handles.get(key);
    if (handle === undefined) {
      return;
    }
    container.appendChild(handle.element);
    if (!handle.opened) {
      // xterm measures its font when opened, so it opens once it's on the page.
      handle.term.open(handle.element);
      handle.opened = true;
    }
    this.fit(key);
    handle.term.focus();
  }

  unmount(key: string): void {
    this.handles.get(key)?.element.remove();
  }

  fit(key: string): void {
    const handle = this.handles.get(key);
    if (handle?.opened === true && handle.element.isConnected) {
      handle.fit.fit();
    }
  }

  has(key: string): boolean {
    return this.handles.has(key);
  }

  private dispose(key: string): void {
    const handle = this.handles.get(key);
    if (handle === undefined) {
      return;
    }
    handle.element.remove();
    handle.term.dispose();
    this.handles.delete(key);
  }
}
