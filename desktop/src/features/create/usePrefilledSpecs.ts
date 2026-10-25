import { useEffect, useRef } from "react";
import { useAppSelector } from "../../store";

/**
 * Fills a dialog's repo specs from ui.dialogSpecs when it opens with some,
 * as New agent and New shell from Repos do, and clears them again the next
 * time it opens without.
 */
export const usePrefilledSpecs = (open: boolean, setSpecs: (specs: readonly string[]) => void): void => {
  const initialSpecs = useAppSelector((s) => s.ui.dialogSpecs);
  const prefilled = useRef(false);
  useEffect(() => {
    if (!open) {
      return;
    }
    if (initialSpecs.length > 0) {
      setSpecs(initialSpecs);
      prefilled.current = true;
    } else if (prefilled.current) {
      setSpecs([]);
      prefilled.current = false;
    }
  }, [open, initialSpecs, setSpecs]);
};
