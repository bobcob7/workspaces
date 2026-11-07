import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fakeSocket, type FakeWebSocket } from "../test/fakeSocket";
import { webTransport } from "./web";

const json = (status: number, body: unknown): Response =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const latestSocket = (sockets: readonly FakeWebSocket[]): FakeWebSocket => {
  const ws = sockets[sockets.length - 1];
  if (ws === undefined) {
    throw new Error("no socket created");
  }
  return ws;
};

describe("webTransport", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
    fetchMock = vi.fn(async () => json(200, null));
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("posts the arguments as JSON to the command's route", async () => {
    const transport = webTransport(fakeSocket().socket);
    await transport.invoke("rename_workspace", { workspaceId: "w1", name: "api" });
    expect(fetchMock).toHaveBeenCalledWith("/api/invoke/rename_workspace", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ workspaceId: "w1", name: "api" }),
    });
  });

  it("posts an empty object when there are no arguments", async () => {
    const transport = webTransport(fakeSocket().socket);
    await transport.invoke("list_entries");
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ body: "{}" });
  });

  it("resolves with the parsed result", async () => {
    fetchMock.mockResolvedValueOnce(json(200, { id: 4 }));
    const transport = webTransport(fakeSocket().socket);
    await expect(transport.invoke("terminal_open")).resolves.toEqual({ id: 4 });
  });

  it("rejects a non-2xx response with its AppError", async () => {
    fetchMock.mockResolvedValueOnce(json(422, { code: "invalid", message: "bad name" }));
    const transport = webTransport(fakeSocket().socket);
    await expect(transport.invoke("rename_workspace")).rejects.toEqual({ code: "invalid", message: "bad name" });
  });

  it("rejects a non-JSON error with its HTTP status", async () => {
    fetchMock.mockResolvedValueOnce(new Response("oops", { status: 500, statusText: "Internal Server Error" }));
    const transport = webTransport(fakeSocket().socket);
    await expect(transport.invoke("list_entries")).rejects.toEqual({
      code: "http_500",
      message: "Internal Server Error",
    });
  });

  it("dispatches unauthenticated on a 401", async () => {
    fetchMock.mockResolvedValueOnce(json(401, { code: "unauthenticated", message: "log in first" }));
    const listener = vi.fn();
    window.addEventListener("ws-online:unauthenticated", listener);
    const transport = webTransport(fakeSocket().socket);
    await expect(transport.invoke("list_entries")).rejects.toEqual({
      code: "unauthenticated",
      message: "log in first",
    });
    window.removeEventListener("ws-online:unauthenticated", listener);
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("sends the connection id with a channel argument", async () => {
    const { socket, sockets } = fakeSocket();
    const ws = latestSocket(sockets);
    ws.open();
    ws.receive({ type: "hello", connection: "conn-1" });
    const transport = webTransport(socket);
    const channel = transport.channel(vi.fn());
    await transport.invoke("terminal_open", { spec: { kind: "shell" }, onEvent: channel.arg });
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({
      headers: { "Content-Type": "application/json", "X-WS-Connection": "conn-1" },
    });
  });

  it("writes terminal input over the socket while it's open", async () => {
    const { socket, sockets } = fakeSocket();
    const ws = latestSocket(sockets);
    ws.open();
    const transport = webTransport(socket);
    const pending = transport.invoke("terminal_write", { id: 3, data: "ls\r" });
    expect(fetchMock).not.toHaveBeenCalled();
    expect(JSON.parse(ws.sent[0] ?? "null")).toMatchObject({ type: "invoke", command: "terminal_write" });
    ws.receive({ type: "result", id: 1, ok: true, value: null });
    await expect(pending).resolves.toBeNull();
  });

  it("writes terminal input over HTTP while the socket is closed", async () => {
    const transport = webTransport(fakeSocket().socket);
    await transport.invoke("terminal_resize", { id: 3, cols: 80, rows: 24 });
    expect(fetchMock).toHaveBeenCalledWith("/api/invoke/terminal_resize", expect.any(Object));
  });

  it("listens to socket events", () => {
    const { socket, sockets } = fakeSocket();
    const handler = vi.fn();
    const transport = webTransport(socket);
    void transport.listen("tray-select", handler).then(() => undefined);
    latestSocket(sockets).receive({ type: "event", event: "tray-select", payload: { key: "a1" } });
    expect(handler).toHaveBeenCalledWith({ key: "a1" });
  });
});
