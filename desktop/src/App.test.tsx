import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { cachedRepo, fakeApi, testStore } from "./test/fixtures";
import { renderWithStore } from "./test/render";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(async () => []),
  Channel: class {},
}));

describe("App", () => {
  it("opens on Agents with its actions", () => {
    renderWithStore(<App />, testStore());
    expect(screen.getByRole("button", { name: "Restore stopped" })).toBeInTheDocument();
  });

  it("shows the Repos section's actions and list when Repos is chosen", async () => {
    const api = fakeApi({ cacheList: vi.fn(async () => ({ repos: [cachedRepo()], warnings: [] })) });
    renderWithStore(<App />, testStore({ api }));
    await userEvent.click(screen.getByRole("button", { name: "Repos" }));
    expect(await screen.findByText("buff-bear")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Fetch all" })).toBeInTheDocument();
  });

  it("keeps the selected repo when switching sections and back", async () => {
    const api = fakeApi({ cacheList: vi.fn(async () => ({ repos: [cachedRepo()], warnings: [] })) });
    const store = testStore({ api });
    renderWithStore(<App />, store);
    await userEvent.click(screen.getByRole("button", { name: "Repos" }));
    await userEvent.click(await screen.findByRole("button", { name: /buff-bear/ }));
    await userEvent.click(screen.getByRole("button", { name: "Agents" }));
    await userEvent.click(screen.getByRole("button", { name: "Repos" }));
    expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent("github.com/bobcob7/buff-bear");
  });

  it("shows the Reviews section's actions and pull requests when Reviews is chosen", async () => {
    renderWithStore(<App />, testStore());
    await userEvent.click(screen.getByRole("button", { name: /^Reviews/ }));
    expect(await screen.findByText("#42 Add rate limiting to login")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Watched repos" })).toBeInTheDocument();
  });
});
