import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { remoteEnvironment, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { AgentsActions } from "./AgentsActions";

const storeWith = (items: ReturnType<typeof remoteEnvironment>[]): ReturnType<typeof testStore> =>
  testStore({
    state: { environments: { items, loadedAt: Date.now(), error: null, latestRequestId: null, warnings: [], lastChoice: "local" } },
  });

describe("AgentsActions", () => {
  it("labels Restore stopped plainly without environments", () => {
    renderWithStore(<AgentsActions />, testStore());
    expect(screen.getByRole("button", { name: "Restore stopped" })).toBeInTheDocument();
  });

  it("says Restore stopped is for this machine once an environment is enrolled", () => {
    renderWithStore(<AgentsActions />, storeWith([remoteEnvironment()]));
    expect(screen.getByRole("button", { name: "Restore stopped (this machine)" })).toBeInTheDocument();
  });

  it("keeps the plain label while the only environment is pending", () => {
    renderWithStore(<AgentsActions />, storeWith([remoteEnvironment({ state: "pending", status: "pending" })]));
    expect(screen.getByRole("button", { name: "Restore stopped" })).toBeInTheDocument();
  });
});
