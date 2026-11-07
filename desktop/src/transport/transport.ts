/** True inside the Tauri shell; false in a plain browser served by ws-online. */
export const isTauri = (): boolean => "__TAURI_INTERNALS__" in window;

/** What `Transport.channel` returns: pass `arg` as a command argument. */
export interface ChannelHandle {
  readonly arg: unknown;
}

/** Calls the backend's commands and events, whichever runtime the app is in. */
export interface Transport {
  invoke<T>(command: string, args?: Record<string, unknown>): Promise<T>;
  channel<T>(onMessage: (message: T) => void): ChannelHandle;
  listen<T>(event: string, handler: (payload: T) => void): Promise<() => void>;
}
