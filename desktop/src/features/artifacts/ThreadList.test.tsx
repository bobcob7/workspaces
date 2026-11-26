import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, type Thread } from "../../api";
import { testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { groupThreads, ThreadList, type ThreadListProps } from "./ThreadList";

const person = { kind: "person" as const, workspace: null, name: null };

const thread = (overrides: Partial<Thread> & Pick<Thread, "id">): Thread => ({
  anchor: null,
  resolved: false,
  resolvedAt: null,
  resolvedBy: null,
  comments: [{ id: `c-${overrides.id}`, author: person, body: `note on ${overrides.id}`, createdAt: "2026-11-23T18:00:00Z" }],
  ...overrides,
});

const anchorAt = (lineStart: number, quote: string, state: "anchored" | "detached" = "anchored", rev = 2) => ({
  rev,
  start: 0,
  end: quote.length,
  lineStart,
  lineEnd: lineStart,
  quote,
  prefix: "",
  suffix: "",
  state,
});

const threads: readonly Thread[] = [
  thread({ id: "t2", anchor: anchorAt(7, "share the object store") }),
  thread({ id: "t1", anchor: anchorAt(3, "the cache is shared") }),
  thread({ id: "t3", anchor: anchorAt(3, "fetches its own copy", "detached", 1) }),
  thread({ id: "t4", anchor: anchorAt(11, "Stale entries"), resolved: true }),
];

const renderList = (overrides: Partial<ThreadListProps> = {}) => {
  const props: ThreadListProps = {
    artifactId: "art-00000001",
    threads,
    activeThread: null,
    readOnly: false,
    onActivate: vi.fn(),
    onChanged: vi.fn(),
    ...overrides,
  };
  const store = testStore();
  return { ...renderWithStore(<ThreadList {...props} />, store), ...props, store };
};

const postedReply = {
  thread: threads[1] as Thread,
  comment: { id: "c9", author: person, body: "Done", createdAt: "2026-11-23T18:05:00Z" },
};

const itemOf = (id: string): HTMLElement => {
  const item = document.querySelector<HTMLElement>(`[data-thread-item="${id}"]`);
  if (item === null) {
    throw new Error(`no thread item ${id}`);
  }
  return item;
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe("groupThreads", () => {
  it("orders open threads by line, then detached, then resolved", () => {
    const groups = groupThreads(threads);

    expect(groups.open.map((t) => t.id)).toEqual(["t1", "t2"]);
    expect(groups.detached.map((t) => t.id)).toEqual(["t3"]);
    expect(groups.resolved.map((t) => t.id)).toEqual(["t4"]);
  });
});

describe("ThreadList", () => {
  it("lists the open threads in line order before the detached ones", () => {
    const { container } = renderList();

    const ids = Array.from(container.querySelectorAll<HTMLElement>("[data-thread-item]")).map((el) =>
      el.getAttribute("data-thread-item"),
    );
    expect(ids).toEqual(["t1", "t2", "t3", "t4"]);
  });

  it("keeps resolved threads collapsed", () => {
    const { container } = renderList();

    expect(screen.getByText("Resolved (1)")).toHaveClass("summary");
    expect(container.querySelector("details")).not.toHaveAttribute("open");
  });

  it("names a detached thread's quote with the revision it was anchored to", () => {
    renderList();

    expect(screen.getByRole("button", { name: '"fetches its own copy" on r1' })).toBeInTheDocument();
  });

  it("activates a thread when its quote is clicked", async () => {
    const { onActivate } = renderList();

    await userEvent.click(screen.getByRole("button", { name: '"the cache is shared"' }));

    expect(onActivate).toHaveBeenCalledWith("t1");
  });

  it("posts a reply to the thread and reloads", async () => {
    const reply = vi.spyOn(api, "artifactCommentAdd").mockResolvedValue({
      thread: threads[1] as Thread,
      comment: { id: "c9", author: person, body: "Done", createdAt: "2026-11-23T18:05:00Z" },
    });
    const { onChanged } = renderList();
    const item = within(itemOf("t1"));

    await userEvent.type(item.getByRole("textbox", { name: "Reply to t1" }), "Done");
    await userEvent.click(item.getByRole("button", { name: "Reply" }));

    expect(reply).toHaveBeenCalledWith("art-00000001", "Done", "t1", null);
    expect(onChanged).toHaveBeenCalled();
  });

  it("shows Sending... and disables Reply and Resolve while a reply is posting", async () => {
    let finish = (): void => undefined;
    vi.spyOn(api, "artifactCommentAdd").mockReturnValue(
      new Promise((resolve) => {
        finish = () => resolve(postedReply);
      }),
    );
    renderList();
    const item = within(itemOf("t1"));
    await userEvent.type(item.getByRole("textbox", { name: "Reply to t1" }), "Done");

    await userEvent.click(item.getByRole("button", { name: "Reply" }));

    expect(item.getByRole("button", { name: "Sending..." })).toBeDisabled();
    expect(item.getByRole("button", { name: "Resolve" })).toBeDisabled();
    await act(async () => {
      finish();
    });
  });

  it("posts one reply when Enter is pressed again while the first is sending", async () => {
    let finish = (): void => undefined;
    const reply = vi.spyOn(api, "artifactCommentAdd").mockReturnValue(
      new Promise((resolve) => {
        finish = () => resolve(postedReply);
      }),
    );
    renderList();
    const textbox = within(itemOf("t1")).getByRole("textbox", { name: "Reply to t1" });
    await userEvent.type(textbox, "Done");

    await userEvent.keyboard("{Enter}{Enter}");

    expect(reply).toHaveBeenCalledTimes(1);
    await act(async () => {
      finish();
    });
  });

  it("resolves an open thread", async () => {
    const resolve = vi.spyOn(api, "artifactCommentResolve").mockResolvedValue(threads[1] as Thread);
    renderList();

    await userEvent.click(within(itemOf("t1")).getByRole("button", { name: "Resolve" }));

    expect(resolve).toHaveBeenCalledWith("art-00000001", "t1", false);
  });

  it("says where a resolved thread went, counting it in Resolved", async () => {
    vi.spyOn(api, "artifactCommentResolve").mockResolvedValue(threads[1] as Thread);
    const { store } = renderList();

    await userEvent.click(within(itemOf("t1")).getByRole("button", { name: "Resolve" }));

    await vi.waitFor(() => {
      expect(store.getState().ui.notice).toMatchObject({
        severity: "success",
        message: "Thread resolved - see Resolved (2)",
      });
    });
  });

  it("reopens a resolved thread", async () => {
    const resolve = vi.spyOn(api, "artifactCommentResolve").mockResolvedValue(threads[3] as Thread);
    renderList();

    await userEvent.click(within(itemOf("t4")).getByRole("button", { name: "Reopen" }));

    expect(resolve).toHaveBeenCalledWith("art-00000001", "t4", true);
  });

  it("says a thread was reopened", async () => {
    vi.spyOn(api, "artifactCommentResolve").mockResolvedValue(threads[3] as Thread);
    const { store } = renderList();

    await userEvent.click(within(itemOf("t4")).getByRole("button", { name: "Reopen" }));

    await vi.waitFor(() => {
      expect(store.getState().ui.notice).toMatchObject({ severity: "success", message: "Thread reopened" });
    });
  });

  it("hides reply and resolve on an older revision", () => {
    renderList({ readOnly: true });

    expect(screen.queryByRole("textbox", { name: "Reply to t1" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Resolve" })).toBeNull();
  });
});
