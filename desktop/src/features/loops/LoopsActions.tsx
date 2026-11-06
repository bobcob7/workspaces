import { Button } from "@mui/material";
import type { JSX } from "react";
import { useAppDispatch } from "../../store";
import { creatingStarted } from "./loopsSlice";

/** The Loops section's top-bar actions: New loop. */
export const LoopsActions = (): JSX.Element => {
  const dispatch = useAppDispatch();
  return (
    <Button size="small" variant="contained" onClick={() => dispatch(creatingStarted())}>
      New loop
    </Button>
  );
};
