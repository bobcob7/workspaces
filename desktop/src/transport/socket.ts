import type { AppError } from "../api";

type Handler = (payload: unknown) => void;

interface PendingInvoke {
  readonly resolve: (value: unknown) => void;
  readonly reject: (error: AppError) => void;
}

const RECONNECT_MIN_MS = 1000;
const RECONNECT_MAX_MS = 30000;

/** The `/api/ws` connection: channels, invoke frames and server events (ADR 0053). */
export class Socket {
  private readonly url: string;
  private readonly create: (url: string) => WebSocket;
  private socket: WebSocket | null = null;
  private open = false;
  private connection: string | null = null;
  private connectionWaiters: ((id: string) => void)[] = [];
  private readonly channels = new Map<number, Handler>();
  private readonly pending = new Map<number, PendingInvoke>();
  private readonly listeners = new Map<string, Set<Handler>>();
  private nextChannel = 1;
  private nextInvoke = 1;
  private reconnectMs = RECONNECT_MIN_MS;

  constructor(url: string, create: (url: string) => WebSocket = (u) => new WebSocket(u)) {
    this.url = url;
    this.create = create;
  }

  connect(): void {
    const socket = this.create(this.url);
    this.socket = socket;
    socket.onopen = () => {
      this.open = true;
    };
    socket.onmessage = (event: MessageEvent<string>) => this.receive(event.data);
    socket.onclose = () => this.close();
  }

  connectionId(): Promise<string> {
    if (this.connection !== null) {
      return Promise.resolve(this.connection);
    }
    return new Promise((resolve) => this.connectionWaiters.push(resolve));
  }

  openChannel(onMessage: Handler): number {
    const id = this.nextChannel;
    this.nextChannel += 1;
    this.channels.set(id, onMessage);
    return id;
  }

  invoke(command: string, args: Record<string, unknown> = {}): Promise<unknown> {
    const id = this.nextInvoke;
    this.nextInvoke += 1;
    return new Promise((resolve, reject) => {
      if (!this.open) {
        reject({ code: "disconnected", message: "the connection is closed" });
        return;
      }
      this.pending.set(id, { resolve, reject });
      this.send({ type: "invoke", id, command, args });
    });
  }

  isOpen(): boolean {
    return this.open;
  }

  on(event: string, handler: Handler): () => void {
    const handlers = this.listeners.get(event) ?? new Set<Handler>();
    handlers.add(handler);
    this.listeners.set(event, handlers);
    return () => {
      handlers.delete(handler);
    };
  }

  private send(frame: object): void {
    this.socket?.send(JSON.stringify(frame));
  }

  private receive(data: string): void {
    const frame = JSON.parse(data) as Record<string, unknown>;
    switch (frame.type) {
      case "hello":
        this.hello(frame.connection as string);
        return;
      case "channel":
        this.channels.get(frame.id as number)?.(frame.payload);
        return;
      case "event":
        this.listeners.get(frame.event as string)?.forEach((handler) => handler(frame.payload));
        return;
      case "result":
        this.settle(frame);
        return;
    }
  }

  private hello(connection: string): void {
    this.connection = connection;
    this.reconnectMs = RECONNECT_MIN_MS;
    const waiters = this.connectionWaiters;
    this.connectionWaiters = [];
    waiters.forEach((resolve) => resolve(connection));
  }

  private settle(frame: Record<string, unknown>): void {
    const pending = this.pending.get(frame.id as number);
    if (pending === undefined) {
      return;
    }
    this.pending.delete(frame.id as number);
    if (frame.ok === true) {
      pending.resolve(frame.value ?? null);
      return;
    }
    pending.reject(frame.error as AppError);
  }

  private close(): void {
    this.open = false;
    this.connection = null;
    this.socket = null;
    this.exitChannels();
    this.rejectPending();
    setTimeout(() => this.connect(), this.reconnectMs);
    this.reconnectMs = Math.min(this.reconnectMs * 2, RECONNECT_MAX_MS);
  }

  private exitChannels(): void {
    const handlers = [...this.channels.values()];
    this.channels.clear();
    handlers.forEach((handler) => handler({ event: "exit", code: null }));
  }

  private rejectPending(): void {
    const invokes = [...this.pending.values()];
    this.pending.clear();
    invokes.forEach((invoke) => invoke.reject({ code: "disconnected", message: "the connection closed" }));
  }
}
