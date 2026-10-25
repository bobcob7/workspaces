import { Button, CircularProgress } from "@mui/material";
import type { JSX } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { dialogOpened } from "../ui/uiSlice";
import { restoreAgents } from "./agentsSlice";

/** The Agents section's top-bar actions. */
export const AgentsActions = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const loading = useAppSelector((s) => s.agents.loading);
  return (
    <>
      {loading && <CircularProgress size={14} aria-label="Refreshing" />}
      <Button size="small" onClick={() => void dispatch(restoreAgents())}>
        Restore stopped
      </Button>
      <Button size="small" variant="outlined" onClick={() => dispatch(dialogOpened("shell"))}>
        New shell
      </Button>
      <Button size="small" variant="contained" onClick={() => dispatch(dialogOpened("agent"))}>
        New agent
      </Button>
    </>
  );
};
