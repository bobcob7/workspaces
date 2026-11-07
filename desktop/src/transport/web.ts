import { toAppError, type AppError } from "../api";
import type { ChannelHandle, Transport } from "./transport";
import type { Socket } from "./socket";

const SOCKET_COMMANDS: ReadonlySet<string> = new Set(["terminal_write", "terminal_resize"]);

const hasChannel = (args: Record<string, unknown>): boolean =>
  Object.values(args).some((value) => typeof value === "object" && value !== null && "__channel" in value);

const failure = async (res: Response): Promise<AppError> => {
  if (res.status === 401) {
    window.dispatchEvent(new Event("ws-online:unauthenticated"));
  }
  try {
    return toAppError(await res.json());
  } catch {
    return { code: `http_${res.status}`, message: res.statusText };
  }
};

const httpInvoke = async <T>(socket: Socket, command: string, args: Record<string, unknown>): Promise<T> => {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (hasChannel(args)) {
    headers["X-WS-Connection"] = await socket.connectionId();
  }
  const res = await fetch(`/api/invoke/${command}`, {
    method: "POST",
    credentials: "same-origin",
    headers,
    body: JSON.stringify(args),
  });
  if (!res.ok) {
    throw await failure(res);
  }
  return (await res.json()) as T;
};

/** The web runtime: commands over HTTP, with terminal input on the socket while it's open. */
export const webTransport = (socket: Socket): Transport => ({
  invoke: async <T>(command: string, args: Record<string, unknown> = {}): Promise<T> => {
    if (SOCKET_COMMANDS.has(command) && socket.isOpen()) {
      return (await socket.invoke(command, args)) as T;
    }
    return httpInvoke<T>(socket, command, args);
  },
  channel: <T>(onMessage: (message: T) => void): ChannelHandle => ({
    arg: { __channel: socket.openChannel((payload) => onMessage(payload as T)) },
  }),
  listen: async <T>(event: string, handler: (payload: T) => void): Promise<() => void> =>
    socket.on(event, handler as (payload: unknown) => void),
});
