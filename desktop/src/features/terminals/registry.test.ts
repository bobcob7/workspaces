import { beforeAll, describe, expect, it, vi } from "vitest";
import type { TermEvent } from "../../api";
import { fakeApi } from "../../test/fixtures";
import { clipboardKeyHandler, TerminalRegistry } from "./registry";

beforeAll(() => {
  // jsdom has no matchMedia, which xterm reads when it opens.
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    addListener: () => undefined,
    removeListener: () => undefined,
    onchange: null,
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});

const keydown = (init: KeyboardEventInit & { key: string }): KeyboardEvent => new KeyboardEvent("keydown", init);

const opened = async (): Promise<{ registry: TerminalRegistry; emit: (data: string) => Promise<void>; write: ReturnType<typeof vi.fn> }> => {
  const write = vi.fn(async () => undefined);
  let onEvent: (event: TermEvent) => void = () => undefined;
  const terminalOpen = vi.fn(async (_spec, _cols, _rows, handler: (event: TermEvent) => void) => {
    onEvent = handler;
    return 1;
  });
  const registry = new TerminalRegistry(fakeApi({ terminalWrite: write, terminalOpen }));
  await registry.open("t", { kind: "shell", name: "sh", specs: [] }, () => undefined);
  registry.mount("t", document.body);
  const emit = async (data: string): Promise<void> => {
    onEvent({ event: "data", data });
    await vi.waitFor(() => expect(registry.text("t")).not.toBe(""));
  };
  return { registry, emit, write };
};

describe("TerminalRegistry text", () => {
  it("joins wrapped lines and trims trailing spaces", async () => {
    const { registry, emit } = await opened();
    const long = "x".repeat(100);
    await emit(`first   \r\n${long}\r\nlast  \r\n`);
    await vi.waitFor(() => expect(registry.text("t")).toBe(`first\n${long}\nlast`));
  });

  it("returns only the last lines asked for", async () => {
    const { registry, emit } = await opened();
    await emit("a\r\nb\r\nc\r\n");
    await vi.waitFor(() => expect(registry.text("t", 2)).toBe("b\nc"));
  });

  it("is empty for an unknown terminal", async () => {
    const { registry } = await opened();
    expect(registry.text("nope")).toBe("");
  });
});

describe("TerminalRegistry paste", () => {
  it("writes the text to the pty", async () => {
    const { registry, write } = await opened();
    registry.paste("t", "hello");
    await vi.waitFor(() => expect(write).toHaveBeenCalledWith(1, "hello"));
  });
});

describe("clipboardKeyHandler", () => {
  const handler = (selected: boolean, copy = vi.fn()) => ({ copy, handle: clipboardKeyHandler(() => selected, copy) });

  it("lets the browser paste on Cmd+V and Ctrl+Shift+V", () => {
    const { handle } = handler(false);
    expect(handle(keydown({ key: "v", metaKey: true }))).toBe(false);
    expect(handle(keydown({ key: "V", ctrlKey: true, shiftKey: true }))).toBe(false);
  });

  it("copies the selection on Cmd+C", () => {
    const { handle, copy } = handler(true);
    expect(handle(keydown({ key: "c", metaKey: true }))).toBe(false);
    expect(copy).toHaveBeenCalledTimes(1);
  });

  it("sends Cmd+C on to the pty without a selection", () => {
    const { handle, copy } = handler(false);
    expect(handle(keydown({ key: "c", metaKey: true }))).toBe(true);
    expect(copy).not.toHaveBeenCalled();
  });

  it("sends plain Ctrl+C to the pty", () => {
    const { handle } = handler(true);
    expect(handle(keydown({ key: "c", ctrlKey: true }))).toBe(true);
  });
});
