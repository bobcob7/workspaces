import { combineReducers, configureStore } from "@reduxjs/toolkit";
import { useDispatch, useSelector } from "react-redux";
import type { Api } from "./api";
import { agentsReducer } from "./features/agents/agentsSlice";
import { conversationsReducer } from "./features/conversation/conversationSlice";
import type { TerminalHost } from "./features/terminals/host";
import { terminalsReducer } from "./features/terminals/terminalsSlice";
import { uiReducer } from "./features/ui/uiSlice";

export interface ThunkExtra {
  readonly api: Api;
  readonly terminals: TerminalHost;
}

export const rootReducer = combineReducers({
  agents: agentsReducer,
  conversations: conversationsReducer,
  terminals: terminalsReducer,
  ui: uiReducer,
});

export type RootState = ReturnType<typeof rootReducer>;

export const createStore = (extra: ThunkExtra, preloadedState?: Partial<RootState>) =>
  configureStore({
    reducer: rootReducer,
    preloadedState,
    middleware: (getDefault) => getDefault({ thunk: { extraArgument: extra } }),
  });

export type AppStore = ReturnType<typeof createStore>;
export type AppDispatch = AppStore["dispatch"];

export const useAppDispatch = useDispatch.withTypes<AppDispatch>();
export const useAppSelector = useSelector.withTypes<RootState>();
