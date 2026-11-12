import { ThemeProvider } from "@mui/material";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { theme } from "../../theme";
import { FilterBar, type FilterBarProps } from "./FilterBar";

const renderBar = (overrides: Partial<FilterBarProps> = {}): Readonly<FilterBarProps> => {
  const props: FilterBarProps = {
    query: "",
    status: "all",
    showHidden: false,
    hiddenCount: 0,
    onQueryChange: vi.fn(),
    onStatusChange: vi.fn(),
    onShowHiddenChange: vi.fn(),
    ...overrides,
  };
  render(
    <ThemeProvider theme={theme}>
      <FilterBar {...props} />
    </ThemeProvider>,
  );
  return props;
};

describe("FilterBar", () => {
  it("reports the search text as it is typed", async () => {
    const props = renderBar();
    await userEvent.type(screen.getByLabelText("Search"), "a");
    expect(props.onQueryChange).toHaveBeenCalledWith("a");
  });

  it("reports the chosen status", async () => {
    const props = renderBar();
    await userEvent.selectOptions(screen.getByLabelText("Status"), "done");
    expect(props.onStatusChange).toHaveBeenCalledWith("done");
  });

  it("lists every status option in display order", () => {
    renderBar();
    const names = screen.getAllByRole("option").map((o) => o.textContent);
    expect(names).toEqual(["All statuses", "Needs input", "Working", "Done", "Stopped", "Shells", "Other"]);
  });

  it("turns Show hidden on when the switch is clicked while it is off", async () => {
    const props = renderBar({ showHidden: false });
    await userEvent.click(screen.getByLabelText(/Show hidden/));
    expect(props.onShowHiddenChange).toHaveBeenCalledWith(true);
  });

  it("shows the number of hidden entries in the switch's label", () => {
    renderBar({ hiddenCount: 3 });
    expect(screen.getByText("Show hidden (3)")).toBeInTheDocument();
  });

  it("is a search landmark named Filter workspaces", () => {
    renderBar();
    expect(screen.getByRole("search", { name: "Filter workspaces" })).toBeInTheDocument();
  });
});
