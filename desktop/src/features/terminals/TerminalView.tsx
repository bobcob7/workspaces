import { useEffect, useRef, type JSX } from "react";
import { useAppDispatch } from "../../store";
import { useTerminalRegistry } from "./context";
import { terminalShown } from "./terminalsSlice";
import styles from "./TerminalView.module.css";

export interface TerminalViewProps {
  readonly terminalKey: string;
  readonly generation: number;
}

/** Shows a terminal the registry owns, refitting it as the pane resizes. */
export const TerminalView = ({ terminalKey, generation }: TerminalViewProps): JSX.Element => {
  const registry = useTerminalRegistry();
  const dispatch = useAppDispatch();
  const container = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const element = container.current;
    if (element === null) {
      return undefined;
    }
    registry.mount(terminalKey, element);
    dispatch(terminalShown(terminalKey));
    const observer = new ResizeObserver(() => registry.fit(terminalKey));
    observer.observe(element);
    return () => {
      observer.disconnect();
      registry.unmount(terminalKey);
    };
  }, [registry, dispatch, terminalKey, generation]);
  return <div ref={container} className={styles.root} data-testid="terminal" />;
};
