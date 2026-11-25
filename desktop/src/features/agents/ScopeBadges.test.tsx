import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ScopeEnvironment } from "../../api";
import { ScopeBadges } from "./ScopeBadges";

const environments: readonly ScopeEnvironment[] = [
  { id: "local", name: "local", state: "online", current: true },
  { id: "env_7f3a", name: "linux-server", state: "online", current: false },
  { id: "env_b20d", name: "win-rig", state: "offline", current: false },
  { id: "env_old1", name: "old-box", state: "removed", current: false },
  { id: "env_unk1", name: "mac", state: "unknown", current: false },
];

const renderScope = (scope: readonly ScopeEnvironment[]) => {
  render(<ScopeBadges scope={scope} />);
  return screen.getByRole("list", { name: "Environments it may start children on" });
};

describe("ScopeBadges", () => {
  it("labels an online environment by its name and the others with their state", () => {
    renderScope(environments);

    expect(screen.getByText("linux-server")).toBeInTheDocument();
    expect(screen.getByText("win-rig · offline")).toBeInTheDocument();
    expect(screen.getByText("old-box · removed")).toBeInTheDocument();
    expect(screen.getByText("mac · unknown")).toBeInTheDocument();
  });

  it("titles each state in words", () => {
    renderScope(environments);

    expect(screen.getByText("linux-server")).toHaveAttribute("title", "linux-server is online");
    expect(screen.getByText("win-rig · offline")).toHaveAttribute(
      "title",
      "win-rig is offline; a child can't start there now",
    );
    expect(screen.getByText("old-box · removed")).toHaveAttribute(
      "title",
      "old-box was removed with ws env rm; a child can't start there",
    );
    expect(screen.getByText("mac · unknown")).toHaveAttribute("title", "mac's state is unknown");
  });

  it("says the current environment is its own", () => {
    renderScope(environments);

    expect(screen.getByText("local")).toHaveAttribute("title", "local is online (its own environment)");
  });

  it("keeps the order it is given", () => {
    const list = renderScope([environments[2]!, environments[0]!, environments[1]!]);

    const labels = within(list)
      .getAllByRole("listitem")
      .map((item) => item.textContent);
    expect(labels).toEqual(["win-rig · offline", "local", "linux-server"]);
  });
});
