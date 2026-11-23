import type { JSX } from "react";
import { useAppSelector } from "../../store";

/** The Artifacts section's detail pane. Part A shows the selection only; the viewers come with part B. */
export const ArtifactsPane = (): JSX.Element => {
  const selected = useAppSelector((s) => s.artifacts.selected);
  return (
    <div role="region" aria-label="Artifact" style={{ padding: 16 }}>
      {selected === null ? "Select an artifact" : selected}
    </div>
  );
};
