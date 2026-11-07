import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { testStore } from "../test/fixtures";
import { renderWithStore } from "../test/render";
import { BrowserPushRow } from "./BrowserPushRow";
import * as push from "./push";

vi.mock("./push", () => ({
  pushState: vi.fn(),
  enablePush: vi.fn(),
  disablePush: vi.fn(),
  sendTestPush: vi.fn(),
}));

const renderRow = async (state: push.PushState): Promise<HTMLElement> => {
  vi.mocked(push.pushState).mockResolvedValue(state);
  renderWithStore(<BrowserPushRow />, testStore());
  return screen.findByRole("group", { name: "This browser" });
};

describe("BrowserPushRow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it.each([
    ["subscribed", "Notifications reach this browser even when the app is closed."],
    ["granted", "Turn on to get notifications here when the app is closed."],
    ["default", "Turn on to get notifications here when the app is closed."],
    ["denied", "Blocked in the browser's site settings."],
    ["unsupported", "This browser can't receive push notifications."],
    ["insecure", "Needs HTTPS (or localhost). See docs/online.md."],
  ] as const)("explains the %s state", async (state, help) => {
    const row = await renderRow(state);
    expect(within(row).getByText(help)).toBeInTheDocument();
  });

  it("offers Turn on when the browser can still be turned on", async () => {
    const row = await renderRow("default");
    expect(within(row).getByRole("button", { name: "Turn on" })).toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: "Turn off" })).not.toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: "Send a test" })).not.toBeInTheDocument();
  });

  it("offers Turn off and Send a test when subscribed", async () => {
    const row = await renderRow("subscribed");
    expect(within(row).getByRole("button", { name: "Turn off" })).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: "Send a test" })).toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: "Turn on" })).not.toBeInTheDocument();
  });

  it.each(["denied", "unsupported", "insecure"] as const)("offers no buttons when %s", async (state) => {
    const row = await renderRow(state);
    expect(within(row).queryByRole("button")).not.toBeInTheDocument();
  });

  it("switches to subscribed after Turn on", async () => {
    const row = await renderRow("default");
    vi.mocked(push.enablePush).mockResolvedValue("subscribed");
    await userEvent.click(within(row).getByRole("button", { name: "Turn on" }));
    expect(await within(row).findByRole("button", { name: "Turn off" })).toBeInTheDocument();
  });

  it("shows the number of devices a test reached", async () => {
    const row = await renderRow("subscribed");
    vi.mocked(push.sendTestPush).mockResolvedValue({ sent: 2, failed: 0 });
    await userEvent.click(within(row).getByRole("button", { name: "Send a test" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Sent to 2 devices.");
  });

  it("shows an error in the row when turning on fails", async () => {
    const row = await renderRow("default");
    vi.mocked(push.enablePush).mockRejectedValue(new Error("boom"));
    await userEvent.click(within(row).getByRole("button", { name: "Turn on" }));
    expect(await within(row).findByRole("alert")).toHaveTextContent("Couldn't turn on notifications.");
  });
});
