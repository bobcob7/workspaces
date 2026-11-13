import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { MergeInfo } from "../../api";
import { MergeBadge } from "./MergeBadge";

const checks = { total: 3, passed: 2, failed: 1, pending: 0 };

const merge = (overrides: Partial<MergeInfo> = {}): MergeInfo => ({
  status: "ready",
  reasons: [],
  checks: { total: 2, passed: 2, failed: 0, pending: 0 },
  ...overrides,
});

describe("MergeBadge", () => {
  it("shows the label for a ready pull request", () => {
    render(<MergeBadge merge={merge()} />);
    expect(screen.getByText("Ready to merge")).toBeInTheDocument();
  });

  it("shows the label for failing checks", () => {
    render(<MergeBadge merge={merge({ status: "failing", reasons: ["failing"], checks })} />);
    expect(screen.getByText("Checks failing")).toBeInTheDocument();
  });

  it("shows the label for a branch behind its base", () => {
    render(<MergeBadge merge={merge({ status: "behind", reasons: ["behind"] })} />);
    expect(screen.getByText("Needs update")).toBeInTheDocument();
  });

  it("lists every reason and the checks summary in its title", () => {
    render(<MergeBadge merge={merge({ status: "failing", reasons: ["failing", "awaitingApproval"], checks })} />);
    expect(screen.getByText("Checks failing")).toHaveAttribute(
      "title",
      "Checks failing · Awaiting approval · 2 passed, 1 failed",
    );
  });

  it("uses the status label as its title when there are no reasons", () => {
    render(<MergeBadge merge={merge()} />);
    expect(screen.getByText("Ready to merge")).toHaveAttribute("title", "Ready to merge · 2 passed");
  });

  it("is toned as an error for conflicts", () => {
    render(<MergeBadge merge={merge({ status: "conflicting", reasons: ["conflicting"] })} />);
    expect(screen.getByText("Conflicts")).toHaveAttribute("data-tone", "error");
  });
});
