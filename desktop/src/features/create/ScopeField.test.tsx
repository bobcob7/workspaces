import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ScopeField, type ScopePick } from "./ScopeField";

const pickWith = (overrides: Partial<ScopePick> = {}): ScopePick => ({
  visible: true,
  choices: [
    { name: "local", status: "online", own: true },
    { name: "win-rig", status: "offline", own: false },
    { name: "lab", status: "unknown", own: false },
  ],
  selected: ["local", "win-rig", "lab"],
  leftOut: [],
  tooMany: false,
  toggle: vi.fn(),
  ...overrides,
});

describe("ScopeField", () => {
  it("labels offline and unknown environments", () => {
    render(<ScopeField pick={pickWith()} />);

    expect(screen.getByRole("checkbox", { name: "local · its own environment" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "win-rig · offline" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "lab · status unknown" })).toBeChecked();
    expect(screen.getByText("win-rig is offline: a child can start there once it's back online.")).toBeInTheDocument();
  });

  it("renders nothing when not visible", () => {
    const { container } = render(<ScopeField pick={pickWith({ visible: false })} />);

    expect(container).toBeEmptyDOMElement();
  });

  it("checkbox labels are clickable", async () => {
    const toggle = vi.fn();
    render(<ScopeField pick={pickWith({ selected: ["local"], toggle })} />);

    await userEvent.click(screen.getByText("lab · status unknown"));

    expect(toggle).toHaveBeenCalledWith("lab");
  });
});
