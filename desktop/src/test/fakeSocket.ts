import { Socket } from "../transport/socket";

/** A WebSocket the tests drive: open(), receive() and drop() stand in for the server. */
export class FakeWebSocket {
  readonly sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { readonly data: string }) => void) | null = null;
  onclose: (() => void) | null = null;

  constructor(readonly url: string) {}

  send(data: string): void {
    this.sent.push(data);
  }

  open(): void {
    this.onopen?.();
  }

  receive(frame: object): void {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }

  drop(): void {
    this.onclose?.();
  }
}

/** A Socket over FakeWebSockets; `sockets` lists each one created, oldest first. */
export const fakeSocket = (): { readonly socket: Socket; readonly sockets: FakeWebSocket[] } => {
  const sockets: FakeWebSocket[] = [];
  const socket = new Socket("ws://test/api/ws", (url) => {
    const ws = new FakeWebSocket(url);
    sockets.push(ws);
    return ws as unknown as WebSocket;
  });
  socket.connect();
  return { socket, sockets };
};
