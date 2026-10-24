import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ConversationItem, Status } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { ConversationView } from "./ConversationView";

const items: readonly ConversationItem[] = [
  { kind: "user", id: "u", at: null, text: "Fix the login" },
  { kind: "assistant", id: "a", at: null, text: "Running the **tests**." },
  { kind: "toolUse", id: "t", at: null, toolUseId: "t1", name: "Bash", summary: "Run tests", input: "go test ./..." },
  { kind: "toolResult", id: "r", at: null, toolUseId: "t1", text: "ok", isError: false },
];

const show = (status: Status = "idle", onShowTerminal = vi.fn()): ReturnType<typeof testStore> => {
  const readTranscript = vi.fn(async () => ({ items, next: 100, reset: false, dropped: 0, found: true }));
  const store = testStore({ api: fakeApi({ readTranscript }) });
  renderWithStore(
    <ConversationView sessionId="s1" status={status} detail="Running tests" onShowTerminal={onShowTerminal} />,
    store,
  );
  return store;
};

describe("ConversationView", () => {
  it("shows the user's message", async () => {
    show();
    expect(await screen.findByText("Fix the login")).toBeInTheDocument();
  });

  it("renders the agent's reply as markdown", async () => {
    show();
    expect(await screen.findByText("tests", { selector: "strong" })).toBeInTheDocument();
  });

  it("shows a tool call with its result", async () => {
    show();
    await userEvent.click(await screen.findByText("Run tests"));
    expect(screen.getByText("ok")).toBeVisible();
  });

  it("says what a working agent is doing", async () => {
    show("working");
    expect(await screen.findByText("Running tests")).toBeInTheDocument();
  });

  it("points to the terminal when the agent needs input", async () => {
    const onShowTerminal = vi.fn();
    show("needsInput", onShowTerminal);
    await userEvent.click(await screen.findByRole("button", { name: "Terminal" }));
    expect(onShowTerminal).toHaveBeenCalled();
  });
});
