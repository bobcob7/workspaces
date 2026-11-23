import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";

const invoke = vi.hoisted(() => vi.fn(async () => ({})));

vi.mock("./transport", () => ({ transport: () => ({ invoke }) }));

beforeEach(() => {
  invoke.mockClear();
});

describe("api environment commands", () => {
  it("environmentsList invokes environments_list without arguments", async () => {
    await api.environmentsList();
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environments_list", undefined);
  });

  it("environmentAdd passes the name", async () => {
    await api.environmentAdd("win-rig");
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environment_add", { name: "win-rig" });
  });

  it("environmentRotate passes the name", async () => {
    await api.environmentRotate("win-rig");
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environment_rotate", { name: "win-rig" });
  });

  it("environmentRemove passes the name", async () => {
    await api.environmentRemove("win-rig");
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environment_remove", { name: "win-rig" });
  });

  it("environmentsCaStatus invokes environments_ca_status without arguments", async () => {
    await api.environmentsCaStatus();
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environments_ca_status", undefined);
  });

  it("environmentsCaRotate passes finish and force", async () => {
    await api.environmentsCaRotate(true, false);
    expect(invoke).toHaveBeenCalledExactlyOnceWith("environments_ca_rotate", { finish: true, force: false });
  });
});

describe("api commands that gained an environment argument", () => {
  it("removeEntry defaults the environment to local", async () => {
    await api.removeEntry("w1", "s1");
    expect(invoke).toHaveBeenCalledExactlyOnceWith("remove_entry", {
      workspaceId: "w1",
      sessionId: "s1",
      keepChildren: false,
      environment: "local",
    });
  });

  it("removeEntry passes a given environment", async () => {
    await api.removeEntry("w1", null, true, "win-rig");
    expect(invoke).toHaveBeenCalledExactlyOnceWith("remove_entry", {
      workspaceId: "w1",
      sessionId: null,
      keepChildren: true,
      environment: "win-rig",
    });
  });

  it("renameWorkspace passes the environment, local by default", async () => {
    await api.renameWorkspace("w1", "new");
    expect(invoke).toHaveBeenLastCalledWith("rename_workspace", { workspaceId: "w1", name: "new", environment: "local" });
    await api.renameWorkspace("w1", "new", "win-rig");
    expect(invoke).toHaveBeenLastCalledWith("rename_workspace", { workspaceId: "w1", name: "new", environment: "win-rig" });
  });

  it("setWorkspaceHidden passes the environment, local by default", async () => {
    await api.setWorkspaceHidden("w1", true);
    expect(invoke).toHaveBeenLastCalledWith("set_workspace_hidden", { workspaceId: "w1", hidden: true, environment: "local" });
    await api.setWorkspaceHidden("w1", false, "win-rig");
    expect(invoke).toHaveBeenLastCalledWith("set_workspace_hidden", { workspaceId: "w1", hidden: false, environment: "win-rig" });
  });

  it("stopAgent passes the environment, local by default", async () => {
    await api.stopAgent("s1");
    expect(invoke).toHaveBeenLastCalledWith("stop_agent", { sessionId: "s1", workspaceId: null, environment: "local" });
    await api.stopAgent("s1", "w1", "win-rig");
    expect(invoke).toHaveBeenLastCalledWith("stop_agent", { sessionId: "s1", workspaceId: "w1", environment: "win-rig" });
  });
});
