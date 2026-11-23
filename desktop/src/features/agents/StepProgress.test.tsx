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

  it("clamps done above total to the full bar", () => {
    render(<StepProgress label="over progress" done={7} total={4} />);
    const bar = screen.getByRole("progressbar", { name: "over progress" });
    expect(bar).toHaveAttribute("aria-valuenow", "4");
    expect(bar).toHaveAttribute("aria-valuemax", "4");
  });

  it("clamps a negative done to an empty bar", () => {
    render(<StepProgress label="negative progress" done={-2} total={4} />);
    expect(screen.getByRole("progressbar", { name: "negative progress" })).toHaveAttribute("aria-valuenow", "0");
  });

  it("clamps a NaN done to an empty bar", () => {
    render(<StepProgress label="nan progress" done={Number.NaN} total={4} />);
    expect(screen.getByRole("progressbar", { name: "nan progress" })).toHaveAttribute("aria-valuenow", "0");
  });

  it("draws no bar for a NaN total", () => {
    render(<StepProgress label="nan total progress" done={1} total={Number.NaN} />);
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("draws no bar for an infinite or negative total", () => {
    const { rerender } = render(<StepProgress label="bad total progress" done={1} total={Number.POSITIVE_INFINITY} />);
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    rerender(<StepProgress label="bad total progress" done={0} total={-1} />);
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("draws no bar when total is 0", () => {
    render(<StepProgress label="empty progress" done={0} total={0} />);
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });
});
