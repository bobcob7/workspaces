import { isTauri } from "./transport/transport";

export const isWeb = (): boolean => !isTauri();
