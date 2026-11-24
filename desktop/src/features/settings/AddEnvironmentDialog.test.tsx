import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Api } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { AddEnvironmentDialog, nameProblem } from "./AddEnvironmentDialog";

const code = "ws-enroll-SECRET-1234";

let copied = "";

const setClipboard = (value: unknown): void => {
  Object.defineProperty(navigator, "clipboard", { value, configurable: true });
};

beforeEach(() => {
  copied = "";
  setClipboard({ writeText: vi.fn(async (text: string) => void (copied = text)) });
});

afterEach(() => {
  Reflect.deleteProperty(navigator, "clipboard");
});

const issued = (name: string): Awaited<ReturnType<Api["environmentAdd"]>> => ({
  id: "env_1",
  name,
  state: "pending",
  code,
  caCreated: null,
  revokedSerials: [],
  warnings: [],
});

const setup = (overrides: Partial<Api> = {}, onClose: () => void = () => undefined) => {
  const api = fakeApi({ environmentAdd: vi.fn(async (name: string) => issued(name)), ...overrides });
  const store = testStore({ api });
  const view = renderWithStore(<AddEnvironmentDialog client={api} onClose={onClose} />, store);
  return { api, store, view };
};

const addNamed = async (name: string): Promise<void> => {
  await userEvent.type(screen.getByRole("textbox", { name: "Name" }), name);
  await userEvent.click(screen.getByRole("button", { name: "Add" }));
};

describe("AddEnvironmentDialog", () => {
  it("shows the code and the two commands once", async () => {
    const { api } = setup();
    await addNamed("linux-server");
    expect(api.environmentAdd).toHaveBeenCalledWith("linux-server");
    expect(await screen.findByText(code)).toBeInTheDocument();
    expect(screen.getByText(`ws env agent enroll ${code}`)).toBeInTheDocument();
    expect(screen.getByText("ws env agent run")).toBeInTheDocument();
    expect(screen.getByText(/stays in your clipboard until you copy something else/)).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /^Copy / })).toHaveLength(3);
    expect(screen.queryByRole("textbox", { name: "Name" })).not.toBeInTheDocument();
  });

  it("copies a command to the clipboard", async () => {
    setup();
    await addNamed("linux-server");
    await screen.findByText(code);
    await userEvent.click(screen.getByRole("button", { name: "Copy Then run the agent" }));
    expect(copied).toBe("ws env agent run");
  });

  it("hides Copy and says to select the text when there is no clipboard", async () => {
    setClipboard(undefined);
    setup();
    await addNamed("linux-server");
    await screen.findByText(code);
    expect(screen.queryByRole("button", { name: /^Copy / })).not.toBeInTheDocument();
    expect(screen.getByText("Select the text to copy it.")).toBeInTheDocument();
  });

  it("closing forgets the code", async () => {
    let closed = false;
    const api = fakeApi({ environmentAdd: vi.fn(async (name: string) => issued(name)) });
    const store = testStore({ api });
    const Host = (): ReturnType<typeof AddEnvironmentDialog> | null => {
      return closed ? null : <AddEnvironmentDialog client={api} onClose={() => (closed = true)} />;
    };
    const view = renderWithStore(<Host />, store);
    await addNamed("linux-server");
    await screen.findByText(code);
    await userEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(closed).toBe(true);
    view.unmount();
    renderWithStore(<AddEnvironmentDialog client={api} onClose={() => undefined} />, store);
    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("");
    expect(screen.queryByText(code)).not.toBeInTheDocument();
  });

  it("says to select the text when the clipboard write is rejected", async () => {
    setClipboard({ writeText: vi.fn(async () => Promise.reject(new Error("denied"))) });
    setup();
    await addNamed("linux-server");
    await screen.findByText(code);
    expect(screen.queryByText("Select the text to copy it.")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Copy Enrollment code" }));
    expect(await screen.findByText("Select the text to copy it.")).toBeInTheDocument();
  });

  it("a second click while adding issues one request", async () => {
    const { api } = setup({ environmentAdd: vi.fn(() => new Promise<never>(() => undefined)) });
    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "linux-server");
    await userEvent.dblClick(screen.getByRole("button", { name: "Add" }));
    expect(screen.getByRole("button", { name: "Add" })).toBeDisabled();
    expect(api.environmentAdd).toHaveBeenCalledTimes(1);
  });

  it("a second click while rotating issues one rotation", async () => {
    const api = fakeApi({ environmentRotate: vi.fn(() => new Promise<never>(() => undefined)) });
    renderWithStore(<AddEnvironmentDialog client={api} rotate="linux-server" onClose={() => undefined} />, testStore({ api }));
    await userEvent.dblClick(screen.getByRole("button", { name: "Rotate" }));
    expect(screen.getByRole("button", { name: "Rotate" })).toBeDisabled();
    expect(api.environmentRotate).toHaveBeenCalledTimes(1);
  });

  it("cannot be closed while a request is in flight", async () => {
    const onClose = vi.fn();
    setup({ environmentAdd: vi.fn(() => new Promise<never>(() => undefined)) }, onClose);
    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "linux-server");
    await userEvent.click(screen.getByRole("button", { name: "Add" }));
    await userEvent.keyboard("{Escape}");
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("the code is not in the store", async () => {
    const { store } = setup();
    await addNamed("linux-server");
    await screen.findByText(code);
    expect(JSON.stringify(store.getState())).not.toContain(code);
  });

  it("the code is not in localStorage", async () => {
    setup();
    await addNamed("linux-server");
    await screen.findByText(code);
    expect(JSON.stringify({ ...window.localStorage })).not.toContain(code);
  });

  it("a missing public name points to the setting", async () => {
    setup({
      environmentAdd: vi.fn(async () => {
        throw { code: "usage", message: "environments.public_name is not set" };
      }),
    });
    await addNamed("linux-server");
    expect(await screen.findByText("Set Public name below first.")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Name" })).toBeInTheDocument();
  });

  it("shows the server's name_invalid under the field", async () => {
    setup({
      environmentAdd: vi.fn(async () => {
        throw { code: "name_invalid", message: "name taken by a workspace" };
      }),
    });
    await addNamed("linux-server");
    expect(await screen.findByText("name taken by a workspace")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("refuses a bad name before asking the server", async () => {
    const { api } = setup();
    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "Bad_Name");
    expect(screen.getByRole("button", { name: "Add" })).toBeDisabled();
    await userEvent.clear(screen.getByRole("textbox", { name: "Name" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "local");
    expect(screen.getByRole("button", { name: "Add" })).toBeDisabled();
    expect(screen.getByText(/local is this machine/)).toBeInTheDocument();
    expect(api.environmentAdd).not.toHaveBeenCalled();
  });

  it("refreshes the environment list after an add", async () => {
    const { api } = setup();
    await addNamed("linux-server");
    await screen.findByText(code);
    await waitFor(() => expect(api.environmentsList).toHaveBeenCalled());
  });

  it("Cancel closes without asking the server", async () => {
    const onClose = vi.fn();
    const { api } = setup({}, onClose);
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalled();
    expect(api.environmentAdd).not.toHaveBeenCalled();
  });
});

describe("nameProblem", () => {
  it.each([
    ["linux-server", null],
    ["a", null],
    ["a".repeat(32), null],
    ["a".repeat(33), "bad"],
    ["1abc", "bad"],
    ["Abc", "bad"],
    ["a_b", "bad"],
    ["-a", "bad"],
    ["local", "bad"],
  ])("%s", (name, want) => {
    expect(nameProblem(name) === null).toBe(want === null);
  });
});
