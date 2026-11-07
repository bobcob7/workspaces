import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { TerminalRegistryContext } from "./context";
import { TerminalRegistry } from "./registry";
import { TerminalTools } from "./TerminalTools";

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

const setup = async (): Promise<{ write: ReturnType<typeof vi.fn> }> => {
  const write = vi.fn(async () => undefined);
  const registry = new TerminalRegistry(fakeApi({ terminalWrite: write }));
  await registry.open("t", { kind: "shell", name: "sh", specs: [] }, () => undefined);
  registry.mount("t", document.body);
  renderWithStore(
    <TerminalRegistryContext.Provider value={registry}>
      <TerminalTools terminalKey="t" generation={0} />
    </TerminalRegistryContext.Provider>,
    testStore(),
  );
  return { write };
};

const clipboard = (readText: () => Promise<string>): void => {
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { readText, writeText: vi.fn(async () => undefined) } });
};

afterEach(() => {
  Reflect.deleteProperty(navigator, "clipboard");
});

describe("TerminalTools", () => {
  it("disables Copy without a selection", async () => {
    await setup();
    expect(screen.getByRole("button", { name: "Copy" })).toBeDisabled();
  });

  it("pastes the clipboard into the terminal", async () => {
    clipboard(async () => "from clipboard");
    const { write } = await setup();
    await userEvent.click(screen.getByRole("button", { name: "Paste" }));
    await vi.waitFor(() => expect(write).toHaveBeenCalledWith(1, "from clipboard"));
  });

  it("opens the paste dialog when the clipboard can't be read, and Send writes the text as typed", async () => {
    clipboard(async () => {
      throw new Error("denied");
    });
    const { write } = await setup();
    await userEvent.click(screen.getByRole("button", { name: "Paste" }));
    await userEvent.type(await screen.findByRole("textbox", { name: "Text to paste" }), "hello");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    await vi.waitFor(() => expect(write).toHaveBeenCalledWith(1, "hello"));
  });

  it("opens the paste dialog when there is no clipboard API", async () => {
    await setup();
    await userEvent.click(screen.getByRole("button", { name: "Paste" }));
    expect(await screen.findByRole("textbox", { name: "Text to paste" })).toHaveFocus();
  });

  it("shows the terminal text with its links in the text dialog", async () => {
    const write = vi.fn();
    let emit: (data: string) => void = () => undefined;
    const registry = new TerminalRegistry(
      fakeApi({
        terminalWrite: write,
        terminalOpen: vi.fn(async (_s, _c, _r, handler) => {
          emit = (data) => handler({ event: "data", data });
          return 1;
        }),
      }),
    );
    await registry.open("t", { kind: "shell", name: "sh", specs: [] }, () => undefined);
  registry.mount("t", document.body);
    emit("Visit https://claude.ai/oauth/authorize?code=true&client_id=abc now\r\n");
    renderWithStore(
      <TerminalRegistryContext.Provider value={registry}>
        <TerminalTools terminalKey="t" generation={0} />
      </TerminalRegistryContext.Provider>,
      testStore(),
    );
    await vi.waitFor(() => expect(registry.text("t")).toContain("https://"));
    await userEvent.click(screen.getByRole("button", { name: "Select text" }));
    expect(await screen.findByRole("button", { name: "https://claude.ai/oauth/authorize?code=true&client_id=abc" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Terminal text" })).toHaveValue("Visit https://claude.ai/oauth/authorize?code=true&client_id=abc now");
  });
});
