import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api, RemoteEnvironment } from "../../api";
import { fakeApi, remoteEnvironment, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { EnvironmentsGroup, statusText } from "./EnvironmentsGroup";

const open = async (environments: readonly RemoteEnvironment[], overrides: Partial<Api> = {}, warnings: readonly string[] = []): Promise<Api> => {
  const api = fakeApi({ environmentsList: vi.fn(async () => ({ environments, warnings })), ...overrides });
  renderWithStore(<EnvironmentsGroup client={api} />, testStore({ api }));
  await waitFor(() => expect(api.environmentsList).toHaveBeenCalled());
  return api;
};

const rowOf = async (name: string): Promise<HTMLElement> => within(await screen.findByRole("list", { name: "Environments list" })).findByRole("listitem", { name });

describe("EnvironmentsGroup", () => {
  it("lists local first and each environment with its status", async () => {
    await open([remoteEnvironment({ workspaces: 2 })]);
    const items = within(await screen.findByRole("list", { name: "Environments list" })).getAllByRole("listitem");
    expect(items[0]).toHaveTextContent("local");
    expect(items[0]).toHaveTextContent("this machine");
    const row = await rowOf("linux-server");
    expect(row).toHaveTextContent("online");
    expect(row).toHaveTextContent("ws 0.9.0");
    expect(row).toHaveTextContent("linux/amd64");
    expect(row).toHaveTextContent("claude 2.1.0");
    expect(row).toHaveTextContent("2 workspaces");
    expect(within(row).getByRole("button", { name: "Rotate linux-server" })).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: "Remove linux-server" })).toBeInTheDocument();
  });

  it("shows the claude missing hint", async () => {
    const base = remoteEnvironment();
    const hello = base.hello;
    if (hello === null) {
      throw new Error("fixture has no hello");
    }
    await open([remoteEnvironment({ hello: { ...hello, claude: "missing", claudeVersion: null } })]);
    const row = await rowOf("linux-server");
    expect(row).toHaveTextContent("claude: missing");
    expect(row).toHaveTextContent("Install Claude Code on this machine, then try again.");
  });

  it("an offline row says when it was last seen", async () => {
    await open([remoteEnvironment({ id: "b", name: "win-rig", status: "offline", lastSeen: "2026-11-23T14:02:00" })]);
    const row = await rowOf("win-rig");
    expect(row).toHaveTextContent("offline since 14:02 (last seen)");
    expect(row).not.toHaveTextContent("online");
  });

  it("a pending row offers Cancel", async () => {
    await open([remoteEnvironment({ id: "c", name: "fresh", state: "pending", status: "pending", hello: null, workspaces: null })]);
    const row = await rowOf("fresh");
    expect(row).toHaveTextContent("waiting for enrollment");
    expect(within(row).getByRole("button", { name: "Cancel fresh" })).toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: "Remove fresh" })).not.toBeInTheDocument();
  });

  it("shows the listing's warnings above the list", async () => {
    await open([remoteEnvironment()], {}, ["the CA expires in 12 days"]);
    expect(await screen.findByText("the CA expires in 12 days")).toBeInTheDocument();
  });

  it("removing asks first, naming the environment and its workspaces, then removes it", async () => {
    const api = await open([remoteEnvironment({ workspaces: 3 })]);
    await userEvent.click(within(await rowOf("linux-server")).getByRole("button", { name: "Remove linux-server" }));
    const dialog = screen.getByRole("dialog", { name: "Remove linux-server?" });
    expect(dialog).toHaveTextContent("3 workspaces");
    expect(api.environmentRemove).not.toHaveBeenCalled();
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove" }));
    expect(api.environmentRemove).toHaveBeenCalledWith("linux-server");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("rotating asks first, then shows the new code", async () => {
    const api = await open([remoteEnvironment()]);
    await userEvent.click(within(await rowOf("linux-server")).getByRole("button", { name: "Rotate linux-server" }));
    const dialog = screen.getByRole("dialog", { name: "Rotate linux-server?" });
    expect(dialog).toHaveTextContent("The old code or certificate stops working");
    expect(api.environmentRotate).not.toHaveBeenCalled();
    await userEvent.click(within(dialog).getByRole("button", { name: "Rotate" }));
    expect(api.environmentRotate).toHaveBeenCalledWith("linux-server");
    expect(await screen.findByText("ws-enroll-test-code")).toBeInTheDocument();
  });
});

describe("EnvironmentsGroup add dialog", () => {
  const closers: readonly [string, () => Promise<void>][] = [
    ["Done", () => userEvent.click(screen.getByRole("button", { name: "Done" }))],
    ["the backdrop", () => userEvent.click(document.querySelector(".MuiBackdrop-root") as HTMLElement)],
    ["Escape", async () => {
      await userEvent.tab();
      await userEvent.keyboard("{Escape}");
    }],
  ];
  it.each(closers)("closing through %s forgets the code", async (_name, close) => {
    await open([]);
    await userEvent.click(await screen.findByRole("button", { name: "Add environment" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "linux-server");
    await userEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(await screen.findByText("ws-enroll-test-code")).toBeInTheDocument();
    await close();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.queryByText("ws-enroll-test-code")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Add environment" }));
    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("");
    expect(screen.queryByText("ws-enroll-test-code")).not.toBeInTheDocument();
  });
});

describe("statusText", () => {
  it("names an unknown status's cause", () => {
    expect(statusText(remoteEnvironment({ status: "unknown" }))).toBe("offline (the ws daemon isn't running)");
  });

  it("drops the time of an offline environment never seen", () => {
    expect(statusText(remoteEnvironment({ status: "offline", lastSeen: null }))).toBe("offline");
  });
});
