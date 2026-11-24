import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, remoteEnvironment, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { RemoveEnvironmentDialog } from "./RemoveEnvironmentDialog";

describe("RemoveEnvironmentDialog", () => {
  it("a second click while removing issues one request", async () => {
    const api = fakeApi({ environmentRemove: vi.fn(() => new Promise<never>(() => undefined)) });
    renderWithStore(<RemoveEnvironmentDialog environment={remoteEnvironment()} onClose={() => undefined} />, testStore({ api }));
    await userEvent.dblClick(screen.getByRole("button", { name: "Remove" }));
    expect(screen.getByRole("button", { name: "Remove" })).toBeDisabled();
    expect(api.environmentRemove).toHaveBeenCalledTimes(1);
  });
});
