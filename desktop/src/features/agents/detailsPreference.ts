const storageKey = "ws.agentDetailsExpanded";

/** Whether an agent's details show under its title. Remembered per browser; expanded when nothing is stored. */
export const readDetailsExpanded = (): boolean => {
  try {
    return window.localStorage.getItem(storageKey) !== "false";
  } catch {
    return true;
  }
};

export const writeDetailsExpanded = (expanded: boolean): void => {
  try {
    window.localStorage.setItem(storageKey, String(expanded));
  } catch {
    // Without storage the choice lasts until the pane remounts.
  }
};
