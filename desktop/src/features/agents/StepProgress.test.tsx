import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { StepProgress } from "./StepProgress";

describe("StepProgress", () => {
  it("exposes its steps as a named progressbar", () => {
    render(<StepProgress label="fix-login progress" done={1} total={4} />);
    const bar = screen.getByRole("progressbar", { name: "fix-login progress" });
    expect(bar).toHaveAttribute("aria-valuenow", "1");
    expect(bar).toHaveAttribute("aria-valuemin", "0");
    expect(bar).toHaveAttribute("aria-valuemax", "4");
  });

  it("shows done over total beside the bar by default", () => {
    render(<StepProgress label="fix-login progress" done={3} total={7} />);
    expect(screen.getByText("3/7")).toBeVisible();
  });

  it("shows the caption it is given instead of the count", () => {
    render(<StepProgress label="fix-login progress" done={3} total={12} caption="3/12 steps" />);
    expect(screen.getByText("3/12 steps")).toBeVisible();
  });

  it("draws a failed bar in the error colour", () => {
    render(<StepProgress label="review progress" done={0} total={1} failed />);
    expect(screen.getByRole("progressbar", { name: "review progress" })).toHaveClass("MuiLinearProgress-colorError");
  });

  it("draws an unfailed bar in the primary colour", () => {
    render(<StepProgress label="build progress" done={1} total={2} />);
    expect(screen.getByRole("progressbar", { name: "build progress" })).toHaveClass("MuiLinearProgress-colorPrimary");
  });

  it("is empty rather than dividing by zero when there are no steps", () => {
    render(<StepProgress label="empty progress" done={0} total={0} />);
    const bar = screen.getByRole("progressbar", { name: "empty progress" });
    expect(bar).toHaveAttribute("aria-valuemax", "0");
    expect(screen.getByText("0/0")).toBeVisible();
  });
});
