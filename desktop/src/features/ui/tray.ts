import type { ThunkAction, UnknownAction } from "@reduxjs/toolkit";
import type { RootState, ThunkExtra } from "../../store";
import { agentFocused } from "./uiSlice";

/** Follows the tray menu's agent choices; resolves to an unsubscribe (ADR 0050). */
export const followTray =
  (): ThunkAction<Promise<() => void>, RootState, ThunkExtra, UnknownAction> =>
  async (dispatch, _getState, { api }) =>
    api.onTraySelect((key) => dispatch(agentFocused(key)));
