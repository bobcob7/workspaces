import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api, AppError } from "../../api";
import { configUnsetChange, daemonStatus, daemonStopped, fakeApi, runnerCheck, runnerSettings, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { ModelsGroup, checkSummary, daemonSummary, endpointWrites, modelAliasFor, roleOf, splitNames } from "./ModelsGroup";

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
  ])("says a %s service was started from %s", (origin, from) => {
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

describe("checkSummary without a model", () => {
  it("counts the models and says no model was checked", () => {
    const check = runnerCheck({ model: null, context_tokens: null, models: ["a", "b", "c"] });
    expect(checkSummary(check)).toBe("✓ 3 models · no model checked · no probe");
  });
});

describe("modelAliasFor", () => {
  it("makes a config name from a model id", () => {
    expect(modelAliasFor("qwen/qwen3.5-9b")).toBe("qwen-qwen3-5-9b");
    expect(modelAliasFor("Gemma3:12b")).toBe("gemma3-12b");
  });

  it("starts with a letter and stays within 32 characters", () => {
    expect(modelAliasFor("9b-model")).toBe("m-9b-model");
    expect(modelAliasFor("a".repeat(60))).toHaveLength(32);
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

  it("removes an endpoint's whole table and clears its token", async () => {
    const api = fakeApi();
    renderGroup(api);
    await userEvent.click(within(await endpointsTable()).getByRole("button", { name: "Remove" }));
    expect(api.configUnset).toHaveBeenCalledTimes(1);
    expect(api.configUnset).toHaveBeenCalledWith("endpoints.gpu");
    expect(api.configSet).not.toHaveBeenCalled();
    expect(api.endpointTokenSet).toHaveBeenCalledWith("gpu", "");
  });

  it("refreshes the table when the config writes land but the token write fails", async () => {
    const api = fakeApi({
      endpointTokenSet: vi.fn(async () => {
        throw { code: "state_write_failed", message: "disk full" } satisfies AppError;
      }),
    });
    renderGroup(api);
    await userEvent.click(within(await endpointsTable()).getByRole("button", { name: "Edit" }));
    const url = screen.getByLabelText("URL");
    await userEvent.clear(url);
    await userEvent.type(url, "http://gpu-b:1234");
    await userEvent.type(screen.getByLabelText("Token"), "tok-123");
    const loads = vi.mocked(api.runnerSettingsGet).mock.calls.length;
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("disk full")).toBeInTheDocument();
    expect(vi.mocked(api.runnerSettingsGet).mock.calls.length).toBeGreaterThan(loads);
  });

  it("refreshes the table when removing an endpoint clears the config but not the token", async () => {
    const api = fakeApi({
      endpointTokenSet: vi.fn(async () => {
        throw { code: "state_write_failed", message: "disk full" } satisfies AppError;
      }),
    });
    renderGroup(api);
    await userEvent.click(within(await endpointsTable()).getByRole("button", { name: "Remove" }));
    expect(await screen.findByText("disk full")).toBeInTheDocument();
    expect(vi.mocked(api.runnerSettingsGet).mock.calls.length).toBeGreaterThan(1);
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

  it("keeps the models table wide enough that its inputs aren't squeezed", async () => {
    renderGroup();
    const table = await screen.findByRole("table", { name: "Models table" });
    expect(table).toHaveClass("modelsTable");
  });

  it("gives the model ID input room for a whole model id", async () => {
    renderGroup();
    const id = await screen.findByLabelText("ID for qwen9b");
    expect(id.closest(".wide")).not.toBeNull();
  });

  describe("with no models", () => {
    const noModels = () => fakeApi({ runnerSettingsGet: vi.fn(async () => runnerSettings({ models: [], roles: [], runner: { model: "" } })) });

    it("says there are no models yet, with a hint to test an endpoint", async () => {
      renderGroup(noModels());
      expect(await screen.findByText("No models yet")).toBeInTheDocument();
      expect(screen.getByText(/Test an endpoint above/)).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Add model" })).toBeInTheDocument();
    });

    it("lists the served models of a tested endpoint, each with Add as model", async () => {
      renderGroup(noModels());
      await userEvent.click(within(await endpointsTable()).getByRole("button", { name: "Test connection" }));
      const served = await screen.findByRole("list", { name: "Models served by gpu" });
      expect(within(served).getAllByRole("listitem")).toHaveLength(2);
      expect(within(served).getByText("qwen/qwen3.5-27b")).toBeInTheDocument();
    });

    it("pre-fills the add form from a served model and creates the model in one write", async () => {
      const api = noModels();
      renderGroup(api);
      await userEvent.click(within(await endpointsTable()).getByRole("button", { name: "Test connection" }));
      const served = await screen.findByRole("list", { name: "Models served by gpu" });
      await userEvent.click(within(served).getAllByRole("button", { name: "Add as model" })[1] as HTMLElement);
      expect(screen.getByLabelText("Alias")).toHaveValue("qwen-qwen3-5-27b");
      expect(screen.getByLabelText("Model ID")).toHaveTextContent("qwen/qwen3.5-27b");
      await userEvent.click(screen.getByRole("button", { name: "Save" }));
      expect(api.configSet).toHaveBeenCalledTimes(1);
      expect(api.configSet).toHaveBeenCalledWith("models.qwen-qwen3-5-27b", ["endpoint=gpu", "id=qwen/qwen3.5-27b", "context_tokens=65536"]);
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    it("takes a typed model id when nothing has been checked", async () => {
      const api = noModels();
      renderGroup(api);
      await userEvent.click(await screen.findByRole("button", { name: "Add model" }));
      await userEvent.type(screen.getByLabelText("Alias"), "mine");
      await userEvent.type(screen.getByLabelText("Model ID"), "llama3:8b");
      const context = screen.getByLabelText("Context tokens");
      await userEvent.clear(context);
      await userEvent.type(context, "32768");
      await userEvent.click(screen.getByRole("button", { name: "Save" }));
      expect(api.configSet).toHaveBeenCalledWith("models.mine", ["endpoint=gpu", "id=llama3:8b", "context_tokens=32768"]);
    });

    it("offers the served models in a select once the endpoint was tested, and still allows another id", async () => {
      const api = noModels();
      renderGroup(api);
      await userEvent.click(within(await endpointsTable()).getByRole("button", { name: "Test connection" }));
      await screen.findByRole("list", { name: "Models served by gpu" });
      await userEvent.click(screen.getByRole("button", { name: "Add model" }));
      await userEvent.click(screen.getByLabelText("Model ID"));
      expect(screen.getByRole("option", { name: "qwen/qwen3.5-9b" })).toBeInTheDocument();
      await userEvent.click(screen.getByRole("option", { name: "Other…" }));
      await userEvent.type(screen.getByLabelText("Model ID"), "custom");
      expect(screen.getByLabelText("Model ID")).toHaveValue("custom");
    });

    it("refuses an alias that is not a config name and a context below the floor", async () => {
      const api = noModels();
      renderGroup(api);
      await userEvent.click(await screen.findByRole("button", { name: "Add model" }));
      await userEvent.type(screen.getByLabelText("Alias"), "Bad Alias");
      await userEvent.type(screen.getByLabelText("Model ID"), "m");
      await userEvent.click(screen.getByRole("button", { name: "Save" }));
      expect(await screen.findByText("An alias is lowercase letters, digits and dashes, starting with a letter")).toBeInTheDocument();
      await userEvent.clear(screen.getByLabelText("Alias"));
      await userEvent.type(screen.getByLabelText("Alias"), "ok");
      await userEvent.clear(screen.getByLabelText("Context tokens"));
      await userEvent.type(screen.getByLabelText("Context tokens"), "100");
      await userEvent.click(screen.getByRole("button", { name: "Save" }));
      expect(await screen.findByText("Context tokens must be at least 16384")).toBeInTheDocument();
      expect(api.configSet).not.toHaveBeenCalled();
    });

    it("shows the error ws gives and keeps the form open", async () => {
      const api = fakeApi({
        runnerSettingsGet: vi.fn(async () => runnerSettings({ models: [], roles: [], runner: { model: "" } })),
        configSet: vi.fn(async () => {
          throw { code: "config_value_invalid", message: "models.x.endpoint isn't a defined endpoint" } satisfies AppError;
        }),
      });
      renderGroup(api);
      await userEvent.click(await screen.findByRole("button", { name: "Add model" }));
      await userEvent.type(screen.getByLabelText("Alias"), "x");
      await userEvent.type(screen.getByLabelText("Model ID"), "m");
      await userEvent.click(screen.getByRole("button", { name: "Save" }));
      expect(await screen.findByText("models.x.endpoint isn't a defined endpoint")).toBeInTheDocument();
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });
  });

  it("refuses an alias that already exists", async () => {
    const api = fakeApi();
    renderGroup(api);
    await userEvent.click(await screen.findByRole("button", { name: "Add model" }));
    await userEvent.type(screen.getByLabelText("Alias"), "qwen9b");
    await userEvent.type(screen.getByLabelText("Model ID"), "m");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("A model named qwen9b already exists")).toBeInTheDocument();
    expect(api.configSet).not.toHaveBeenCalled();
  });

  it("removes a model's whole table", async () => {
    const api = fakeApi();
    renderGroup(api);
    await userEvent.click(await screen.findByRole("button", { name: "Remove qwen27b" }));
    expect(api.configUnset).toHaveBeenCalledTimes(1);
    expect(api.configUnset).toHaveBeenCalledWith("models.qwen27b");
  });

  it("clears the default model before removing it", async () => {
    const api = fakeApi();
    renderGroup(api);
    await userEvent.click(await screen.findByRole("button", { name: "Remove qwen9b" }));
    expect(vi.mocked(api.configUnset).mock.calls.map((call) => call[0])).toEqual(["runner.model", "models.qwen9b"]);
  });

  it("puts the default back when removing it fails", async () => {
    const api = fakeApi({
      configUnset: vi.fn(async (key: string) => {
        if (key === "models.qwen9b") {
          throw { code: "config_value_invalid", message: "runner.roles.implementer.model uses it" } satisfies AppError;
        }
        return configUnsetChange(key);
      }),
    });
    renderGroup(api);
    await userEvent.click(await screen.findByRole("button", { name: "Remove qwen9b" }));
    expect(await screen.findByText("runner.roles.implementer.model uses it")).toBeInTheDocument();
    expect(api.configSet).toHaveBeenCalledWith("runner.model", ["qwen9b"]);
  });

  it("sets the default model from a select", async () => {
    const api = fakeApi();
    renderGroup(api);
    await userEvent.click(await screen.findByLabelText("Default model"));
    await userEvent.click(screen.getByRole("option", { name: "qwen27b" }));
    expect(api.configSet).toHaveBeenCalledWith("runner.model", ["qwen27b"]);
  });

  it("says a default is needed when there are models and none is the default", async () => {
    renderGroup(fakeApi({ runnerSettingsGet: vi.fn(async () => runnerSettings({ runner: { model: "" } })) }));
    expect(await screen.findByText(/No default model/)).toBeInTheDocument();
  });

  it("shows an endpoint-only check without a model line", async () => {
    renderGroup(fakeApi({ runnerCheck: vi.fn(async () => runnerCheck({ model: null, context_tokens: null })) }));
    await userEvent.click(within(await endpointsTable()).getByRole("button", { name: "Test connection" }));
    expect(await screen.findByText("✓ 2 models · no model checked · no probe")).toBeInTheDocument();
  });
});
