import { createContext, useContext } from "react";
import type { TerminalRegistry } from "./registry";

export const TerminalRegistryContext = createContext<TerminalRegistry | null>(null);

export const useTerminalRegistry = (): TerminalRegistry => {
  const registry = useContext(TerminalRegistryContext);
  if (registry === null) {
    throw new Error("useTerminalRegistry needs a TerminalRegistryContext provider");
  }
  return registry;
};
