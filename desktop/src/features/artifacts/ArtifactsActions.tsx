import { Button } from "@mui/material";
import { useRef, type ChangeEvent, type JSX } from "react";
import { useAppDispatch } from "../../store";
import { uploadArtifacts } from "./artifactsSlice";
import styles from "./ArtifactsActions.module.css";

/** The file types the picker offers; the same list the drop zone takes. */
export const artifactAccept =
  ".png,.jpg,.jpeg,.gif,.webp,.svg,.md,.markdown,.txt,.log,.json,.diff,.patch,.csv,.yaml,.yml,image/*";

/** The Artifacts section's top-bar actions: Upload, which opens the file picker. */
export const ArtifactsActions = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const input = useRef<HTMLInputElement>(null);
  const onChange = (event: ChangeEvent<HTMLInputElement>): void => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    if (files.length > 0) {
      void dispatch(uploadArtifacts(files));
    }
  };
  return (
    <>
      <Button
        size="small"
        variant="contained"
        className={styles.upload}
        onClick={() => input.current?.click()}
      >
        Upload
      </Button>
      <input ref={input} type="file" multiple accept={artifactAccept} hidden onChange={onChange} />
    </>
  );
};
