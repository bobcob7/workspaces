import { tauriTransport } from "./tauri";
import { Socket } from "./socket";
import { isTauri, type Transport } from "./transport";
import { webTransport } from "./web";

let web: Transport | undefined;

const socketUrl = (): string => {
  const scheme = location.protocol === "https:" ? "wss" : "ws";
  return `${scheme}://${location.host}/api/ws`;
};

const webSocket = (): Transport => {
  if (web === undefined) {
    const socket = new Socket(socketUrl());
    socket.connect();
    web = webTransport(socket);
  }
  return web;
};

/** The transport for the runtime the app is in, decided on each call. */
export const transport = (): Transport => (isTauri() ? tauriTransport : webSocket());
