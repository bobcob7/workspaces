import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { LoopMemory } from "../../api";
import { fakeApi, loop, loopMemoryEntry, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { MemoryCard } from "./MemoryCard";

const memory = (overrides: Partial<LoopMemory> = {}): LoopMemory => ({
  loop: "deps",
  entries: [loopMemoryEntry({ key: "seen", value: "#412 #415", rev: 3 })],
  bytes: 10,
  warnings: [],
  ...overrides,
});

const apiWith = (overrides: Parameters<typeof fakeApi>[0] = {}) => fakeApi({ loopsMemory: vi.fn(async () => memory()), ...overrides });

describe("MemoryCard", () => {
  it("lists the remembered entries", async () => {
    renderWithStore(<MemoryCard loop={loop()} />, testStore({ api: apiWith() }));
    const table = await screen.findByRole("table", { name: "Memory" });
    expect(within(table).getByText("seen")).toBeInTheDocument();
    expect(within(table).getByText("#412 #415")).toBeInTheDocument();
  });

  it("adds a key with rev 0", async () => {
    const api = apiWith({ loopsMemory: vi.fn(async () => memory({ entries: [] })) });
    renderWithStore(<MemoryCard loop={loop()} />, testStore({ api }));
    await userEvent.click(await screen.findByRole("button", { name: "Add" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Key" }), "seen");
    await userEvent.type(screen.getByRole("textbox", { name: "Value" }), "x");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(api.loopsMemorySet).toHaveBeenCalledWith("deps", "seen", "x", 0);
  });

  it("edits an entry and sends its rev", async () => {
    const api = apiWith();
    renderWithStore(<MemoryCard loop={loop()} />, testStore({ api }));
    await userEvent.click(await screen.findByRole("button", { name: "Edit seen" }));
    expect(screen.getByRole("textbox", { name: "Key" })).toBeDisabled();
    await userEvent.type(screen.getByRole("textbox", { name: "Value" }), " #420");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(api.loopsMemorySet).toHaveBeenCalledWith("deps", "seen", "#412 #415 #420", 3);
  });

  it("shows a conflict, and Reload clears it and reloads the entry's value and rev", async () => {
    let stored = memory();
    const api = apiWith({
      loopsMemory: vi.fn(async () => stored),
      loopsMemorySet: vi.fn(async () => {
        throw { code: "memory_conflict", message: "changed" };
      }),
    });
    renderWithStore(<MemoryCard loop={loop()} />, testStore({ api }));
    await userEvent.click(await screen.findByRole("button", { name: "Edit seen" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("A run changed seen since you opened it.")).toBeInTheDocument();
    const callsBefore = vi.mocked(api.loopsMemory).mock.calls.length;
    stored = memory({ entries: [loopMemoryEntry({ key: "seen", value: "#999", rev: 7 })] });
    await userEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(screen.queryByText("A run changed seen since you opened it.")).not.toBeInTheDocument());
    expect(api.loopsMemory).toHaveBeenCalledTimes(callsBefore + 1);
    expect(screen.getByRole("textbox", { name: "Value" })).toHaveValue("#999");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(api.loopsMemorySet).toHaveBeenLastCalledWith("deps", "seen", "#999", 7);
  });

  it("does not show a stale conflict when the dialog is cancelled and reopened", async () => {
    const api = apiWith({
      loopsMemorySet: vi.fn(async () => {
        throw { code: "memory_conflict", message: "changed" };
      }),
    });
    renderWithStore(<MemoryCard loop={loop()} />, testStore({ api }));
    await userEvent.click(await screen.findByRole("button", { name: "Edit seen" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("A run changed seen since you opened it.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await userEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(screen.getByRole("textbox", { name: "Key" })).toHaveValue("");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("deletes an entry", async () => {
    const api = apiWith();
    renderWithStore(<MemoryCard loop={loop()} />, testStore({ api }));
    await userEvent.click(await screen.findByRole("button", { name: "Delete seen" }));
    expect(api.loopsMemoryRemove).toHaveBeenCalledWith("deps", "seen");
  });

  it("disables Save for a value over 65,536 bytes", async () => {
    renderWithStore(<MemoryCard loop={loop()} />, testStore({ api: apiWith({ loopsMemory: vi.fn(async () => memory({ entries: [] })) }) }));
    await userEvent.click(await screen.findByRole("button", { name: "Add" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Key" }), "seen");
    fireEvent.change(screen.getByRole("textbox", { name: "Value" }), { target: { value: "x".repeat(65_537) } });
    expect(screen.getByText("65,537 / 65,536 bytes")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("rejects a key that starts with a digit and disables Save", async () => {
    renderWithStore(<MemoryCard loop={loop()} />, testStore({ api: apiWith({ loopsMemory: vi.fn(async () => memory({ entries: [] })) }) }));
    await userEvent.click(await screen.findByRole("button", { name: "Add" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Key" }), "1x");
    expect(screen.getByText("Start with a letter or _, then letters, digits or _ (64 at most).")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });
});
