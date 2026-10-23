import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { fakeHost, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { messageKeys, ReplyBox } from "./ReplyBox";

describe("messageKeys", () => {
  it("submits a one-line message with Enter", () => {
    expect(messageKeys("yes")).toBe("yes\r");
  });

  it("pastes a multi-line message so its newlines don't submit it", () => {
    expect(messageKeys("a\nb")).toBe("\x1b[200~a\nb\x1b[201~\r");
  });
});

describe("ReplyBox", () => {
  it("sends the message to the terminal on Enter", async () => {
    const terminals = fakeHost();
    renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} />, testStore({ terminals }));
    await userEvent.type(screen.getByRole("textbox", { name: "Message" }), "go on{Enter}");
    expect(terminals.send).toHaveBeenCalledWith("attach:a", "go on\r");
  });

  it("clears the box after sending", async () => {
    renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} />, testStore());
    const box = screen.getByRole("textbox", { name: "Message" });
    await userEvent.type(box, "go on{Enter}");
    expect(box).toHaveValue("");
  });

  it("does not send an empty message", async () => {
    const terminals = fakeHost();
    renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} />, testStore({ terminals }));
    await userEvent.type(screen.getByRole("textbox", { name: "Message" }), "  {Enter}");
    expect(terminals.send).not.toHaveBeenCalled();
  });
});
