import type { TermSpec } from "../../api";

/** Opens and drives the terminals behind the terminals slice. */
export interface TerminalHost {
  open(key: string, spec: TermSpec, onExit: (code: number | null) => void): Promise<void>;
  close(key: string): Promise<void>;
  send(key: string, text: string): Promise<void>;
}
