import { TextField } from "@mui/material";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState, type JSX } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, type Artifact, type ArtifactSummary } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import type { AppStore } from "../../store";
import { AttachmentBar } from "./AttachButton";
import { loadArtifacts } from "./artifactsSlice";
import { useAttachments } from "./useAttachments";

const person = { kind: "person" as const, workspace: null, name: null };

const stored = (id: string, name: string): Artifact => ({
  id,
  reference: `@artifact:${id}`,
  name,
  title: null,
  kind: "image",
  contentType: "image/png",
  latest: 1,
  size: 3,
  createdAt: "2026-11-23T18:00:00Z",
  updatedAt: "2026-11-23T18:00:00Z",
  createdBy: person,
  threads: { open: 0, resolved: 0, detached: 0 },
  revisions: [],
});

const login: ArtifactSummary = {
  id: "art-00000002",
  reference: "@artifact:art-00000002",
  name: "login-error.png",
  title: null,
  kind: "image",
  contentType: "image/png",
  latest: 1,
  size: 126,
  createdAt: "2026-11-23T18:00:00Z",
  updatedAt: "2026-11-23T18:00:00Z",
  createdBy: person,
  threads: { open: 0, resolved: 0, detached: 0 },
};

const Prompt = ({ initial }: { readonly initial: string }): JSX.Element => {
  const [text, setText] = useState(initial);
  const attachments = useAttachments((reference) => setText((current) => `${current}${reference}`));
  return (
    <>
      <TextField
        label="Prompt"
        multiline
        value={text}
        onChange={(event) => setText(event.target.value)}
        onPaste={attachments.onPaste}
        onDrop={attachments.onDrop}
        onDragOver={attachments.onDragOver}
      />
      <AttachmentBar text={text} onTextChange={setText} attachments={attachments} />
    </>
  );
};

const renderPrompt = (store: AppStore, initial = ""): void => {
  renderWithStore(<Prompt initial={initial} />, store);
};

const pngFile = (name: string): File => new File(["png"], name, { type: "image/png" });

afterEach(() => {
  vi.restoreAllMocks();
});

describe("AttachmentBar", () => {
  it("stores a picked file and inserts its reference at the end of the prompt", async () => {
    const add = vi.spyOn(api, "artifactAdd").mockResolvedValue(stored("art-00000009", "shot.png"));
    renderPrompt(testStore({ api: fakeApi() }), "fix it ");
    const picker = document.querySelector<HTMLInputElement>('input[type="file"]') as HTMLInputElement;

    await userEvent.upload(picker, pngFile("shot.png"));

    await waitFor(() => expect(screen.getByRole("textbox")).toHaveValue("fix it @artifact:art-00000009 "));
    expect(add).toHaveBeenCalledWith("shot.png", null, expect.any(String));
  });

  it("shows that an upload is running", async () => {
    let finish: (value: Artifact) => void = () => undefined;
    vi.spyOn(api, "artifactAdd").mockImplementation(
      () => new Promise<Artifact>((resolve) => (finish = resolve)),
    );
    renderPrompt(testStore({ api: fakeApi() }));
    const picker = document.querySelector<HTMLInputElement>('input[type="file"]') as HTMLInputElement;

    await userEvent.upload(picker, pngFile("shot.png"));

    expect(screen.getByRole("status")).toHaveTextContent("Uploading…");
    finish(stored("art-00000009", "shot.png"));
    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
  });

  it("stores a pasted image under a generated name", async () => {
    const add = vi.spyOn(api, "artifactAdd").mockResolvedValue(stored("art-00000010", "pasted.png"));
    renderPrompt(testStore({ api: fakeApi() }));

    fireEvent.paste(screen.getByRole("textbox"), {
      clipboardData: { items: [{ kind: "file", type: "image/png", getAsFile: () => new File(["png"], "", { type: "image/png" }) }] },
    });

    await waitFor(() => expect(add).toHaveBeenCalledWith(expect.stringMatching(/^pasted-\d{8}-\d{6}\.png$/), null, expect.any(String)));
  });

  it("stores files dropped on the prompt", async () => {
    const add = vi.spyOn(api, "artifactAdd").mockResolvedValue(stored("art-00000011", "build.log"));
    renderPrompt(testStore({ api: fakeApi() }));

    fireEvent.drop(screen.getByRole("textbox"), { dataTransfer: { files: [new File(["log"], "build.log")], types: ["Files"] } });

    await waitFor(() => expect(add).toHaveBeenCalledWith("build.log", null, expect.any(String)));
  });

  it("removes a reference from the prompt with its chip", async () => {
    const store = testStore({ api: fakeApi({ artifactList: vi.fn(async () => [login]) }) });
    store.dispatch(loadArtifacts.fulfilled([login], "req", undefined));
    renderPrompt(store, "fix @artifact:art-00000002 now");

    await userEvent.click(screen.getByRole("button", { name: "Remove login-error.png" }));

    expect(screen.getByRole("textbox")).toHaveValue("fix now");
  });
});
