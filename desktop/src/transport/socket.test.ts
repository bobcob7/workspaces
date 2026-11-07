import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fakeSocket, type FakeWebSocket } from "../test/fakeSocket";

const latest = (sockets: readonly FakeWebSocket[]): FakeWebSocket => {
  const ws = sockets[sockets.length - 1];
  if (ws === undefined) {
    throw new Error("no socket created");
  }
  return ws;
};

const connected = () => {
  const setup = fakeSocket();
  latest(setup.sockets).open();
  return setup;
};

describe("Socket", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("resolves the connection id from hello", async () => {
    const { socket, sockets } = fakeSocket();
    const id = socket.connectionId();
    latest(sockets).receive({ type: "hello", connection: "abc" });
    await expect(id).resolves.toBe("abc");
    await expect(socket.connectionId()).resolves.toBe("abc");
  });

  it("delivers channel frames to the handler with that id", () => {
    const { socket, sockets } = connected();
    const first = vi.fn();
    const second = vi.fn();
    const firstId = socket.openChannel(first);
    socket.openChannel(second);
    latest(sockets).receive({ type: "channel", id: firstId, payload: { event: "data", data: "hi" } });
    expect(first).toHaveBeenCalledWith({ event: "data", data: "hi" });
    expect(second).not.toHaveBeenCalled();
  });

  it("sends invoke frames and resolves from the result frame", async () => {
    const { socket, sockets } = connected();
    const ws = latest(sockets);
    const pending = socket.invoke("terminal_write", { id: 3, data: "ls" });
    expect(JSON.parse(ws.sent[0] ?? "null")).toEqual({
      type: "invoke",
      id: 1,
      command: "terminal_write",
      args: { id: 3, data: "ls" },
    });
    ws.receive({ type: "result", id: 1, ok: true, value: null });
    await expect(pending).resolves.toBeNull();
  });

  it("rejects an invoke with the error in its result frame", async () => {
    const { socket, sockets } = connected();
    const pending = socket.invoke("terminal_resize", { id: 3, cols: 80, rows: 24 });
    latest(sockets).receive({
      type: "result",
      id: 1,
      ok: false,
      error: { code: "terminal_closed", message: "terminal 3 isn't open" },
    });
    await expect(pending).rejects.toEqual({ code: "terminal_closed", message: "terminal 3 isn't open" });
  });

  it("rejects an invoke made while the socket is closed", async () => {
    const { socket } = fakeSocket();
    await expect(socket.invoke("terminal_write", {})).rejects.toEqual({
      code: "disconnected",
      message: "the connection is closed",
    });
  });

  it("delivers events to their listeners until unsubscribed", () => {
    const { socket, sockets } = connected();
    const handler = vi.fn();
    const unsubscribe = socket.on("notify", handler);
    latest(sockets).receive({ type: "event", event: "notify", payload: { title: "done" } });
    unsubscribe();
    latest(sockets).receive({ type: "event", event: "notify", payload: { title: "again" } });
    expect(handler).toHaveBeenCalledTimes(1);
    expect(handler).toHaveBeenCalledWith({ title: "done" });
  });

  it("on close, exits every channel and rejects pending invokes", async () => {
    const { socket, sockets } = connected();
    const onMessage = vi.fn();
    socket.openChannel(onMessage);
    const pending = socket.invoke("terminal_write", {});
    latest(sockets).drop();
    expect(onMessage).toHaveBeenCalledWith({ event: "exit", code: null });
    expect(socket.isOpen()).toBe(false);
    await expect(pending).rejects.toEqual({ code: "disconnected", message: "the connection closed" });
  });

  it("reconnects after one second, doubling the wait up to thirty", () => {
    const { sockets } = connected();
    latest(sockets).drop();
    vi.advanceTimersByTime(999);
    expect(sockets).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(sockets).toHaveLength(2);
    latest(sockets).drop();
    vi.advanceTimersByTime(1999);
    expect(sockets).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(sockets).toHaveLength(3);
  });

  it("resets the reconnect wait once hello arrives", () => {
    const { sockets } = connected();
    latest(sockets).drop();
    vi.advanceTimersByTime(1000);
    latest(sockets).receive({ type: "hello", connection: "again" });
    latest(sockets).drop();
    vi.advanceTimersByTime(1000);
    expect(sockets).toHaveLength(3);
  });
});
