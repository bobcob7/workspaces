import { screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Stats } from "../../api";
import { fakeApi, statsSnapshot, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { StatsPane } from "./StatsPane";

const renderPaneWith = (stats: Stats): void => {
  renderWithStore(<StatsPane />, testStore({ api: fakeApi({ statsGet: vi.fn(async () => stats) }) }));
};

const cardOf = async (heading: string): Promise<HTMLElement> => {
  const section = (await screen.findByRole("heading", { name: heading })).closest("section");
  if (section === null) {
    throw new Error(`${heading} is not in a card`);
  }
  return section;
};

describe("stats cards", () => {
  it("shows a heading for each card", async () => {
    renderWithStore(<StatsPane />, testStore());
    const headings = [
      "System",
      "Usage",
      "Usage by model",
      "Tokens by kind",
      "Lines by language",
      "Productivity by model and language",
    ];
    for (const heading of headings) {
      expect(await screen.findByRole("heading", { name: heading })).toBeInTheDocument();
    }
  });

  it("has one row per model, after the header, labelled with the model", async () => {
    renderWithStore(<StatsPane />, testStore());
    const table = within(await cardOf("Usage by model")).getByRole("table");
    expect(within(table).getAllByRole("row")).toHaveLength(3);
    expect(within(table).getByRole("rowheader", { name: "Sonnet 5.5" })).toBeInTheDocument();
  });

  it("names the leading model's share, speed and cost for Go in the productivity card", async () => {
    renderWithStore(<StatsPane />, testStore());
    const card = await cardOf("Productivity by model and language");
    expect(
      within(card).getByText(
        "Go: Haiku 5.5 wrote 67% of lines at 10.4 lines/min ($0.0035/line); Sonnet 5.5 takes 11 min per 100 lines.",
      ),
    ).toBeInTheDocument();
  });

  it("groups the productivity table under a Go row", async () => {
    renderWithStore(<StatsPane />, testStore());
    const card = await cardOf("Productivity by model and language");
    expect(within(card).getByText("Go", { selector: "th" })).toBeInTheDocument();
  });

  it("shows the empty states when there are no requests or edits", async () => {
    const snapshot = statsSnapshot();
    renderPaneWith(
      statsSnapshot({
        usage: { ...snapshot.usage, byModel: [], byLanguage: [] },
        productivity: [],
      }),
    );
    expect(await screen.findByText("No requests in this window.")).toBeInTheDocument();
    expect(screen.getAllByText("No edits in this window.")).toHaveLength(2);
  });

  it("explains the estimate for a model with no known price", async () => {
    const snapshot = statsSnapshot();
    renderPaneWith(
      statsSnapshot({
        usage: { ...snapshot.usage, byModel: snapshot.usage.byModel.map((model) => ({ ...model, priced: false })) },
      }),
    );
    expect(await screen.findByText(/No price known/)).toBeInTheDocument();
  });
});
