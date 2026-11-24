import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { remoteEnvironment } from "../../test/fixtures";
import { EnvFilter } from "./EnvFilter";

const environments = [
  remoteEnvironment(),
  remoteEnvironment({ id: "env_2", name: "win-rig", status: "offline" }),
  remoteEnvironment({ id: "env_3", name: "mac-mini", status: "pending", state: "pending" }),
];

describe("EnvFilter", () => {
  it("lists local and each environment with its dot", () => {
    render(<EnvFilter environments={environments} value={null} onChange={vi.fn()} />);
    const group = screen.getByRole("group", { name: "Environment" });
    const chips = within(group).getAllByRole("button");
    expect(chips.map((c) => c.textContent)).toEqual(["All", "●local", "●linux-server", "○win-rig", "◌mac-mini"]);
    expect(screen.getByRole("button", { name: /win-rig/ })).toHaveAttribute("title", "offline");
  });

  it("marks the chosen chip pressed", () => {
    render(<EnvFilter environments={environments} value="win-rig" onChange={vi.fn()} />);
    expect(screen.getByRole("button", { name: /win-rig/ })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: /linux-server/ })).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "false");
  });

  it("reports the chosen environment, local, and All as null", async () => {
    const onChange = vi.fn();
    render(<EnvFilter environments={environments} value="local" onChange={onChange} />);
    await userEvent.click(screen.getByRole("button", { name: /linux-server/ }));
    expect(onChange).toHaveBeenLastCalledWith("linux-server");
    await userEvent.click(screen.getByRole("button", { name: /local/ }));
    expect(onChange).toHaveBeenLastCalledWith("local");
    await userEvent.click(screen.getByRole("button", { name: "All" }));
    expect(onChange).toHaveBeenLastCalledWith(null);
  });
});
