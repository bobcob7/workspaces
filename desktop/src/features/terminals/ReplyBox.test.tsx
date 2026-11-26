import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, fakeHost, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { ReplyBox } from "./ReplyBox";
import { messageKeys } from "./terminalsSlice";

describe("messageKeys", () => {
  it("types a one-line message as is", () => {
    expect(messageKeys("yes")).toBe("yes");
  });

  it("pastes a multi-line message so its newlines don't submit it", () => {
    expect(messageKeys("a\nb")).toBe("\x1b[200~a\nb\x1b[201~");
  });
});

describe("ReplyBox", () => {
  it("sends the message to the terminal on Enter", async () => {
    const terminals = fakeHost();
    renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} />, testStore({ terminals }));
    await userEvent.type(screen.getByRole("textbox", { name: "Message" }), "go on{Enter}");
    expect(terminals.send).toHaveBeenCalledWith("attach:a", "go on");
  });

  it("presses Enter in its own write after the message", async () => {
    const terminals = fakeHost();
    renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} />, testStore({ terminals }));
    await userEvent.type(screen.getByRole("textbox", { name: "Message" }), "go on{Enter}");
    await vi.waitFor(() => expect(terminals.send).toHaveBeenLastCalledWith("attach:a", "\r"));
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

  it("uses a short placeholder and Enter adds a line on a phone", async () => {
    const terminals = fakeHost();
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: true,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
    }));
    try {
      renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} />, testStore({ terminals }));
      const box = screen.getByRole("textbox", { name: "Message" });
      expect(box).toHaveAttribute("placeholder", "Message");
      await userEvent.type(box, "a{Enter}b");
      expect(terminals.send).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("adds the artifact hint line to a message that refers to an artifact", async () => {
    const terminals = fakeHost();
    renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} />, testStore({ terminals }));
    await userEvent.type(screen.getByRole("textbox", { name: "Message" }), "see @artifact:art-1a2b3c4d{Enter}");
    await vi.waitFor(() =>
      expect(terminals.send).toHaveBeenCalledWith("attach:a", expect.stringContaining('run "ws machine artifact get <id>"')),
    );
  });

  it("offers Attach under the box", () => {
    renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} />, testStore());

    expect(screen.getByRole("button", { name: "Attach" })).toBeInTheDocument();
  });
});

describe("ReplyBox citations", () => {
  const type = async (text: string): Promise<void> => {
    await userEvent.type(screen.getByRole("textbox", { name: "Message" }), `${text}{Enter}`);
  };

  it("records the references a sent message cites for a local agent", async () => {
    const api = fakeApi();
    renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} citeWorkspaceId="fix-1" />, testStore({ api }));
    await type("see @artifact:art-1a2b3c4d/r2");
    await vi.waitFor(() => expect(api.artifactCite).toHaveBeenCalledWith("fix-1", ["@artifact:art-1a2b3c4d/r2"]));
  });

  it("records nothing for a message without references", async () => {
    const api = fakeApi();
    const terminals = fakeHost();
    renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} citeWorkspaceId="fix-1" />, testStore({ api, terminals }));
    await type("go on");
    await vi.waitFor(() => expect(terminals.send).toHaveBeenLastCalledWith("attach:a", "\r"));
    expect(api.artifactCite).not.toHaveBeenCalled();
  });

  it("records nothing without a workspace, as for a remote agent", async () => {
    const api = fakeApi();
    const terminals = fakeHost();
    renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} />, testStore({ api, terminals }));
    await type("see @artifact:art-1a2b3c4d");
    await vi.waitFor(() => expect(terminals.send).toHaveBeenLastCalledWith("attach:a", "\r"));
    expect(api.artifactCite).not.toHaveBeenCalled();
  });

  it("records nothing when the message couldn't be sent", async () => {
    const api = fakeApi();
    const terminals = fakeHost();
    vi.mocked(terminals.send).mockRejectedValue(new Error("closed"));
    const store = testStore({ api, terminals });
    renderWithStore(<ReplyBox terminalKey="attach:a" disabled={false} citeWorkspaceId="fix-1" />, store);
    await type("see @artifact:art-1a2b3c4d");
    await vi.waitFor(() => expect(store.getState().ui.notice?.severity).toBe("error"));
    expect(api.artifactCite).not.toHaveBeenCalled();
  });
});
