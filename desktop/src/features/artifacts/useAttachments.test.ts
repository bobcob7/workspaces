import { act, renderHook } from "@testing-library/react";
import { createElement, useState, type ReactNode } from "react";
import { Provider } from "react-redux";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, type Artifact } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { insertAt, insertAtCaret, insertAtSelection, removeReference, useAttachments } from "./useAttachments";

afterEach(() => {
  vi.restoreAllMocks();
});

const stored = (id: string): Artifact => ({ id }) as Artifact;

describe("useAttachments", () => {
  const setup = (element: HTMLTextAreaElement, initial: string) =>
    renderHook(
      () => {
        const [text, setText] = useState(initial);
        const attachments = useAttachments(
          (reference, selection) => setText((current) => insertAtSelection(current, selection, reference)),
          () => element,
        );
        return { text, attachments };
      },
      { wrapper: ({ children }: { children: ReactNode }) => createElement(Provider, { store: testStore({ api: fakeApi() }), children }) },
    );

  it("inserts several files in order at the caret read when attach was called", async () => {
    vi.spyOn(api, "artifactAdd").mockImplementation(async (name: string) => stored(`art-${name}`));
    const element = document.createElement("textarea");
    element.value = "fix it";
    element.setSelectionRange(4, 4);
    const { result } = setup(element, "fix it");
    const files = ["a", "b", "c"].map((name) => new File(["x"], name));

    let attaching: Promise<void> = Promise.resolve();
    act(() => {
      attaching = result.current.attachments.attach(files);
    });
    element.setSelectionRange(0, 0);
    await act(async () => {
      await attaching;
    });

    expect(result.current.text).toBe("fix @artifact:art-a @artifact:art-b @artifact:art-c it");
  });

  it("inserts the references of the files stored when a later one fails", async () => {
    vi.spyOn(api, "artifactAdd").mockImplementation(async (name: string) => {
      if (name === "b") {
        throw { code: "ws_failed", message: "disk full" };
      }
      return stored(`art-${name}`);
    });
    const element = document.createElement("textarea");
    const { result } = setup(element, "");

    await act(async () => {
      await result.current.attachments.attach([new File(["x"], "a"), new File(["x"], "b")]);
    });

    expect(result.current.text).toBe("@artifact:art-a ");
  });
});

describe("insertAt", () => {
  it("replaces the selected characters with the inserted text", () => {
    expect(insertAt("see the log", 4, 7, "@artifact:art-00000001 ")).toBe("see @artifact:art-00000001  log");
  });
});

describe("insertAtCaret", () => {
  it("appends when there is no caret to read", () => {
    expect(insertAtCaret("fix it", null, " @artifact:art-00000001 ")).toBe("fix it @artifact:art-00000001 ");
  });

  it("inserts at the caret of the element", () => {
    const element = document.createElement("textarea");
    element.value = "fix it";
    element.setSelectionRange(4, 4);

    expect(insertAtCaret("fix it", element, "@artifact:art-00000001 ")).toBe("fix @artifact:art-00000001 it");
  });

  it("separates the reference from a word the caret follows", () => {
    const element = document.createElement("textarea");
    element.value = "login error";
    element.setSelectionRange(11, 11);

    expect(insertAtCaret("login error", element, "@artifact:art-00000004 ")).toBe(
      "login error @artifact:art-00000004 ",
    );
  });

  it("adds no separator after a space or at the start of the text", () => {
    const element = document.createElement("textarea");
    element.value = "fix ";
    element.setSelectionRange(4, 4);

    expect(insertAtCaret("fix ", element, "@artifact:art-00000001 ")).toBe("fix @artifact:art-00000001 ");
    expect(insertAtCaret("", null, "@artifact:art-00000001 ")).toBe("@artifact:art-00000001 ");
  });
});

describe("removeReference", () => {
  it("removes the reference and the space after it", () => {
    expect(removeReference("see @artifact:art-1a2b3c4d and fix it", "art-1a2b3c4d")).toBe("see and fix it");
  });

  it("removes a revision-pinned reference", () => {
    expect(removeReference("@artifact:art-1a2b3c4d/r2 check", "art-1a2b3c4d")).toBe("check");
  });

  it("leaves other references alone", () => {
    expect(removeReference("@artifact:art-00000001 @artifact:art-00000002", "art-00000001")).toBe("@artifact:art-00000002");
  });
});
