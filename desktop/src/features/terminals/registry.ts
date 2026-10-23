import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import type { Api, TermSpec } from "../../api";
import type { TerminalHost } from "./host";

interface Handle {
  readonly term: Terminal;
  readonly fit: FitAddon;
  readonly element: HTMLDivElement;
  id: number | null;
  closed: boolean;
  opened: boolean;
}

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
