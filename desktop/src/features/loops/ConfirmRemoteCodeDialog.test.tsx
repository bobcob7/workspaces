import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithStore } from "../../test/render";
import { testStore } from "../../test/fixtures";
import { ConfirmRemoteCodeDialog } from "./ConfirmRemoteCodeDialog";

const renderDialog = (props: Partial<Parameters<typeof ConfirmRemoteCodeDialog>[0]> = {}) => {
  const onConfirm = vi.fn();
  const onCancel = vi.fn();
  renderWithStore(
    <ConfirmRemoteCodeDialog environment="linux-server" every="1h" script="make check" bypass={false} onConfirm={onConfirm} onCancel={onCancel} {...props} />,
    testStore(),
  );
  return { onConfirm, onCancel };
};

describe("ConfirmRemoteCodeDialog", () => {
  it("names the environment and shows the script read-only", () => {
    renderDialog();
    expect(screen.getByText("Run code on linux-server?")).toBeInTheDocument();
    expect(
      screen.getByText(
        "This script runs as the agent's user on linux-server every 1h, outside Claude Code's permissions. Anyone who can use this app can run code there.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Script")).toHaveTextContent("make check");
  });

  it("says every permission for a bypass mode", () => {
    renderDialog({ bypass: true, script: "" });
    expect(screen.getByText("This loop's agent runs on linux-server with every permission (bypassPermissions)")).toBeInTheDocument();
  });

  it("focuses Cancel and confirms only from the Run button", async () => {
    const { onConfirm, onCancel } = renderDialog();
    expect(screen.getByRole("button", { name: "Cancel" })).toHaveFocus();
    expect(onConfirm).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Run on linux-server" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });
});
