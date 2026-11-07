import { Channel, invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import type { Transport } from "./transport";

/** The Tauri runtime: commands, channels and events go through the desktop shell. */
export const tauriTransport: Transport = {
  invoke: async <T>(command: string, args?: Record<string, unknown>): Promise<T> => invoke<T>(command, args),
  channel: <T>(onMessage: (message: T) => void) => {
    const channel = new Channel<T>();
    channel.onmessage = onMessage;
    return { arg: channel };
  },
  listen: async <T>(event: string, handler: (payload: T) => void): Promise<() => void> =>
    listen<T>(event, (event) => handler(event.payload)),
};
