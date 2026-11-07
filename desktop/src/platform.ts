import { isTauri } from "./transport/transport";

/** True when the app runs in a browser rather than the Tauri shell. */
export const isWeb = (): boolean => !isTauri();
