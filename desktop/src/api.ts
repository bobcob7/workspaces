import { Channel, invoke } from "@tauri-apps/api/core";

export type Status =
  | "working"
  | "needsInput"
  | "idle"
  | "done"
  | "starting"
  | "stopped"
  | "running"
  | "pending"
  | "unknown";

export type EntryKind = "agent" | "shell" | "pending" | "unknown";

export interface Repo {
  readonly id: string;
  readonly path: string;
  readonly ref: string | null;
  readonly refKind: string | null;
}

export interface Link {
  readonly href: string;
  readonly kind: string;
}

/** A workspace, or a background session outside any workspace. */
export interface Entry {
  readonly key: string;
  readonly kind: EntryKind;
  readonly name: string;
  readonly workspaceId: string | null;
  readonly sessionId: string | null;
  readonly path: string;
  readonly status: Status;
  readonly stale: boolean | null;
  readonly createdAt: string | null;
  readonly updatedAt: string | null;
  readonly detail: string | null;
  readonly intent: string | null;
  readonly result: string | null;
  readonly links: readonly Link[];
  readonly repos: readonly Repo[];
}

export interface Listing {
  readonly entries: readonly Entry[];
  readonly warnings: readonly string[];
}

export interface AppError {
  readonly code: string;
  readonly message: string;
  readonly details?: Readonly<Record<string, unknown>>;
}

export interface NewAgent {
  readonly name: string;
  readonly specs: readonly string[];
  readonly prompt: string;
  readonly model: string;
  readonly permissionMode: string;
  readonly offline: boolean;
}

export interface Created {
  readonly id: string;
  readonly session: string;
  readonly path: string;
}

export type TermSpec =
  | { readonly kind: "attach"; readonly sessionId: string; readonly cwd: string }
  | { readonly kind: "shell"; readonly name: string; readonly specs: readonly string[] };

export type TermEvent =
  | { readonly event: "data"; readonly data: string }
  | { readonly event: "exit"; readonly code: number | null };

const isAppError = (value: unknown): value is AppError =>
  typeof value === "object" && value !== null && "code" in value && "message" in value;

/** Normalizes whatever a failed invoke rejected with into an AppError. */
export const toAppError = (value: unknown): AppError => {
  if (isAppError(value)) {
    return value;
  }
  if (value instanceof Error) {
    return { code: "internal", message: value.message };
  }
  return { code: "internal", message: String(value) };
};

const call = async <T>(command: string, args?: Record<string, unknown>): Promise<T> => {
  try {
    return await invoke<T>(command, args);
  } catch (err: unknown) {
    throw toAppError(err);
  }
};

export const api = {
  listEntries: async (): Promise<Listing> => call<Listing>("list_entries"),
  cacheRepos: async (): Promise<readonly string[]> => call<string[]>("cache_repos"),
  createAgent: async (request: NewAgent): Promise<Created> => call<Created>("create_agent", { request }),
  removeEntry: async (workspaceId: string | null, sessionId: string | null): Promise<void> =>
    call<void>("remove_entry", { workspaceId, sessionId }),
  stopAgent: async (sessionId: string): Promise<void> => call<void>("stop_agent", { sessionId }),
  restoreAgents: async (): Promise<readonly string[]> => call<string[]>("restore_agents"),
  openExternal: async (target: string): Promise<void> => call<void>("open_external", { target }),
  terminalOpen: async (
    spec: TermSpec,
    cols: number,
    rows: number,
    onEvent: (event: TermEvent) => void,
  ): Promise<number> => {
    const channel = new Channel<TermEvent>();
    channel.onmessage = onEvent;
    return call<number>("terminal_open", { spec, cols, rows, onEvent: channel });
  },
  terminalWrite: async (id: number, data: string): Promise<void> => call<void>("terminal_write", { id, data }),
  terminalResize: async (id: number, cols: number, rows: number): Promise<void> =>
    call<void>("terminal_resize", { id, cols, rows }),
  terminalClose: async (id: number): Promise<void> => call<void>("terminal_close", { id }),
};

export type Api = typeof api;
