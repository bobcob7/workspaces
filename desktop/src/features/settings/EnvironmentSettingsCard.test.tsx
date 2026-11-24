import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api, ConfigListing } from "../../api";
import { configListing, configSetChange, daemonStopped, fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { EnvironmentSettingsCard } from "./EnvironmentSettingsCard";

const withEnvironments = (): ConfigListing => {
  const base = configListing();
  return {
    ...base,
    settings: [
      ...base.settings,
      { key: "environments.public_name", type: "string", value: "ws.example.com", default: "", set: true },
      { key: "environments.listen", type: "string", value: "0.0.0.0:9000", default: "0.0.0.0:8883", set: true },
      { key: "environments.renew_before_days", type: "int", value: "30", default: "30", set: false },
    ],
  };
};

const open = async (listing: ConfigListing, overrides: Partial<Api> = {}): Promise<Api> => {
  const api = fakeApi({
    configList: vi.fn(async () => listing),
    configSet: vi.fn(async (key: string, values: readonly string[]) => {
      const change = configSetChange(key, values);
      return { ...change, setting: { ...change.setting, type: key.endsWith("days") ? ("int" as const) : change.setting.type } };
    }),
    ...overrides,
  });
  renderWithStore(<EnvironmentSettingsCard />, testStore({ api }));
  await screen.findByRole("textbox", { name: "Public name" });
  return api;
};

describe("EnvironmentSettingsCard", () => {
  it("shows defaults when unset", async () => {
    await open(configListing());
    expect(screen.getByRole("textbox", { name: "Public name" })).toHaveValue("");
    expect(screen.getByRole("textbox", { name: "Listen" })).toHaveValue("0.0.0.0:8883");
    expect(screen.getByRole("textbox", { name: "Enroll listen" })).toHaveValue("0.0.0.0:8884");
    expect(screen.getByRole("textbox", { name: "Renew before (days)" })).toHaveValue("30");
  });

  it("shows the values config.toml lists", async () => {
    await open(withEnvironments());
    expect(screen.getByRole("textbox", { name: "Public name" })).toHaveValue("ws.example.com");
    expect(screen.getByRole("textbox", { name: "Listen" })).toHaveValue("0.0.0.0:9000");
  });

  it("saves renew_before_days as an int", async () => {
    const api = await open(withEnvironments());
    const field = screen.getByRole("textbox", { name: "Renew before (days)" });
    await userEvent.clear(field);
    await userEvent.type(field, "14");
    expect(api.configSet).not.toHaveBeenCalled();
    await userEvent.tab();
    expect(api.configSet).toHaveBeenCalledWith("environments.renew_before_days", ["14"]);
  });

  it("saves nothing when a field is left unchanged", async () => {
    const api = await open(withEnvironments());
    await userEvent.click(screen.getByRole("textbox", { name: "Listen" }));
    await userEvent.tab();
    expect(api.configSet).not.toHaveBeenCalled();
    expect(api.configUnset).not.toHaveBeenCalled();
  });

  it("clearing a field unsets the key so the default applies", async () => {
    const api = await open(withEnvironments());
    await userEvent.clear(screen.getByRole("textbox", { name: "Public name" }));
    await userEvent.tab();
    expect(api.configUnset).toHaveBeenCalledWith("environments.public_name");
  });

  it("a whitespace-only field unsets the key and sets nothing", async () => {
    const api = await open(withEnvironments());
    const field = screen.getByRole("textbox", { name: "Public name" });
    await userEvent.clear(field);
    await userEvent.type(field, "   ");
    await userEvent.tab();
    expect(api.configUnset).toHaveBeenCalledWith("environments.public_name");
    expect(api.configSet).not.toHaveBeenCalled();
  });

  it("shows the error of a failed save under the field", async () => {
    await open(withEnvironments(), {
      configSet: vi.fn(async () => {
        throw { code: "config_invalid", message: "renew_before_days must be positive" };
      }),
    });
    const field = screen.getByRole("textbox", { name: "Renew before (days)" });
    await userEvent.clear(field);
    await userEvent.type(field, "0");
    await userEvent.tab();
    expect(await screen.findByText("renew_before_days must be positive")).toBeInTheDocument();
  });

  it("says when changes apply and restarts the daemon", async () => {
    const api = await open(configListing(), { daemonRestart: vi.fn(async () => daemonStopped()) });
    expect(screen.getByText("Changes apply when the ws daemon reloads; the desktop restarts it.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Restart daemon" }));
    await waitFor(() => expect(api.daemonRestart).toHaveBeenCalledTimes(1));
  });
});
