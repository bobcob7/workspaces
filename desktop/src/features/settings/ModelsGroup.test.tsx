import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api, AppError } from "../../api";
import { daemonStatus, daemonStopped, fakeApi, runnerCheck, runnerSettings, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { ModelsGroup, checkSummary, daemonSummary, endpointWrites, roleOf, splitNames } from "./ModelsGroup";

const renderGroup = (api: Api = fakeApi()) => {
  const store = testStore({ api });
  renderWithStore(<ModelsGroup />, store);
  return store;
};

const endpointsTable = async (): Promise<HTMLElement> => screen.findByRole("table", { name: "Endpoints table" });

describe("daemonSummary", () => {
  it("names the origin, pid and runner count of a running service", () => {
    expect(daemonSummary(daemonStatus({ origin: "app", pid: 40211 }))).toBe("running · started by this app · pid 40211 · 2 runners");
  });

  it.each([
    ["transient", "a terminal"],
    ["managed", "a service manager"],
  ])("says where an %s service was started from", (origin, from) => {
    expect(daemonSummary(daemonStatus({ origin }))).toContain(`started by ${from}`);
  });

  it("says not running for a service that is down", () => {
    expect(daemonSummary(daemonStatus({ running: false, pid: null, origin: null, runners: [] }))).toBe("not running");
  });

  it("uses the singular for one runner", () => {
    expect(daemonSummary(daemonStatus({ runners: daemonStatus().runners.slice(0, 1) }))).toMatch(/· 1 runner$/);
  });
});

describe("checkSummary", () => {
  it("names the models, the model, its context and that no probe ran", () => {
    expect(checkSummary(runnerCheck())).toBe("✓ 2 models · qwen/qwen3.5-9b · context 65536 · no probe");
  });

  it("says supports tools when the probe reports the tools capability", () => {
    const check = runnerCheck({ probe: "ollama", models: ["qwen3.5:9b"], capabilities: ["completion", "tools"] });
    expect(checkSummary(check)).toBe("✓ 1 model · qwen/qwen3.5-9b · context 65536 · supports tools");
  });
});

describe("endpointWrites", () => {
  const before = runnerSettings().endpoints[0];

  it("sets only the keys that changed", () => {
    if (before === undefined) {
      throw new Error("the fixture has an endpoint");
    }
    const draft = { url: before.url, api: before.api, probe: before.probe, request_timeout: before.request_timeout, idle_timeout: before.idle_timeout };
    const writes = endpointWrites("gpu", before, { ...draft, url: "http://gpu-b:1234" });
    expect(writes).toEqual([{ key: "endpoints.gpu.url", values: ["http://gpu-b:1234"] }]);
  });

  it("sets every key of a new endpoint", () => {
    const writes = endpointWrites("gpu-b", null, { url: "http://b", api: "openai", probe: "auto", request_timeout: "10m", idle_timeout: "90s" });
    expect(writes.map((write) => write.key)).toEqual([
      "endpoints.gpu-b.url",
      "endpoints.gpu-b.api",
      "endpoints.gpu-b.probe",
      "endpoints.gpu-b.request_timeout",
      "endpoints.gpu-b.idle_timeout",
    ]);
  });
});

describe("splitNames", () => {
  it("keeps the names typed, trimmed, and none for an empty list", () => {
    expect(splitNames(" metrics, , files ")).toEqual(["metrics", "files"]);
    expect(splitNames("")).toEqual([]);
  });
});

describe("roleOf", () => {
  it("gives a role the config does not name the built-in defaults", () => {
    expect(roleOf([], "reviewer")).toMatchObject({ runner: "claude", max_steps: 40, mcp_servers: [] });
  });
});

describe("ModelsGroup", () => {
  it("shows the runner service line with where it was started from", async () => {
    renderGroup(fakeApi({ daemonStatus: vi.fn(async () => daemonStatus({ origin: "transient" })) }));
    expect(await screen.findByText("running · started by a terminal · pid 40211 · 2 runners")).toBeInTheDocument();
  });

  it("shows not running without a Restart button", async () => {
    renderGroup(fakeApi({ daemonStatus: vi.fn(async () => daemonStatus({ running: false, pid: null, origin: null, runners: [] })) }));
    expect(await screen.findByText("not running")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Restart" })).not.toBeInTheDocument();
  });

  it("calls daemon_restart once when Restart is clicked", async () => {
    const api = fakeApi({ daemonRestart: vi.fn(async () => daemonStopped()) });
    renderGroup(api);
    await userEvent.click(await screen.findByRole("button", { name: "Restart" }));
    expect(api.daemonRestart).toHaveBeenCalledTimes(1);
  });

  it("shows a passing test connection in one line", async () => {
    renderGroup();
    await userEvent.click(within(await endpointsTable()).getByRole("button", { name: "Test connection" }));
    expect(await screen.findByText("✓ 2 models · qwen/qwen3.5-9b · context 65536 · no probe")).toBeInTheDocument();
  });

  it.each([
    ["endpoint_unreachable", "The endpoint didn't answer"],
    ["model_not_served", "The endpoint doesn't serve that model"],
    ["model_without_tools", "That model can't call tools"],
  ])("shows the %s error banner", async (code, title) => {
    const error: AppError = { code, message: "gpu said so" };
    renderGroup(
      fakeApi({
        runnerCheck: vi.fn(async () => {
          throw error;
        }),
      }),
    );
    await userEvent.click(within(await endpointsTable()).getByRole("button", { name: "Test connection" }));
    expect(await screen.findByText(`${title}. gpu said so`)).toBeInTheDocument();
  });

  it("saves a changed reasoning level as its own config key", async () => {
    const api = fakeApi();
    renderGroup(api);
    await userEvent.click(await screen.findByLabelText("Reasoning for qwen9b"));
    await userEvent.click(screen.getByRole("option", { name: "low" }));
    expect(api.configSet).toHaveBeenCalledWith("models.qwen9b.reasoning", ["low"]);
  });

  it("saves a role's runner under runner.roles", async () => {
    const api = fakeApi();
    renderGroup(api);
    await userEvent.click(await screen.findByLabelText("Runner for reviewer"));
    await userEvent.click(screen.getByRole("option", { name: "ws runner" }));
    expect(api.configSet).toHaveBeenCalledWith("runner.roles.reviewer.runner", ["ws"]);
  });

  it("saves only the changed endpoint key, and writes the token without showing it", async () => {
    const api = fakeApi();
    renderGroup(api);
    await userEvent.click(within(await endpointsTable()).getByRole("button", { name: "Edit" }));
    const url = screen.getByLabelText("URL");
    await userEvent.clear(url);
    await userEvent.type(url, "http://gpu-b:1234");
    await userEvent.type(screen.getByLabelText("Token"), "tok-123");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(api.configSet).toHaveBeenCalledTimes(1);
    expect(api.configSet).toHaveBeenCalledWith("endpoints.gpu.url", ["http://gpu-b:1234"]);
    expect(api.endpointTokenSet).toHaveBeenCalledWith("gpu", "tok-123");
    expect(screen.queryByText("tok-123")).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("refuses a new endpoint whose name is not a config name", async () => {
    const api = fakeApi();
    renderGroup(api);
    await userEvent.click(await screen.findByRole("button", { name: "Add endpoint" }));
    await userEvent.type(screen.getByLabelText("Name"), "Bad Name");
    await userEvent.type(screen.getByLabelText("URL"), "http://x:1");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("A name is lowercase letters, digits and dashes, starting with a letter")).toBeInTheDocument();
    expect(api.configSet).not.toHaveBeenCalled();
  });

  it("writes every key of a new endpoint", async () => {
    const api = fakeApi();
    renderGroup(api);
    await userEvent.click(await screen.findByRole("button", { name: "Add endpoint" }));
    await userEvent.type(screen.getByLabelText("Name"), "gpu-b");
    await userEvent.type(screen.getByLabelText("URL"), "http://gpu-b:1234");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(api.configSet).toHaveBeenCalledTimes(5);
    expect(api.configSet).toHaveBeenCalledWith("endpoints.gpu-b.url", ["http://gpu-b:1234"]);
    expect(api.configSet).toHaveBeenCalledWith("endpoints.gpu-b.api", ["openai"]);
  });
});
