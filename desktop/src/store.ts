import { combineReducers, configureStore } from "@reduxjs/toolkit";
import { useDispatch, useSelector, useStore } from "react-redux";
import type { Api } from "./api";
import { agentsReducer } from "./features/agents/agentsSlice";
import { actionsReducer } from "./features/reviews/actionsSlice";
import { conversationsReducer } from "./features/conversation/conversationSlice";
import { reposReducer } from "./features/repos/reposSlice";
import { reportsReducer } from "./features/reviews/reportsSlice";
import { reviewsListener } from "./features/reviews/reviewsListener";
import { reviewsReducer } from "./features/reviews/reviewsSlice";
import { settingsReducer } from "./features/settings/settingsSlice";
import type { TerminalHost } from "./features/terminals/host";
import { shellLinkListener } from "./features/terminals/shellLinks";
import { terminalsReducer } from "./features/terminals/terminalsSlice";
import { uiReducer } from "./features/ui/uiSlice";

export interface ThunkExtra {
  readonly api: Api;
  readonly terminals: TerminalHost;
}

export const rootReducer = combineReducers({
  actions: actionsReducer,
  agents: agentsReducer,
  conversations: conversationsReducer,
  repos: reposReducer,
  reports: reportsReducer,
  reviews: reviewsReducer,
  settings: settingsReducer,
  terminals: terminalsReducer,
  ui: uiReducer,
});

export type RootState = ReturnType<typeof rootReducer>;

export const createStore = (extra: ThunkExtra, preloadedState?: Partial<RootState>) =>
  configureStore({
    reducer: rootReducer,
    preloadedState,
    middleware: (getDefault) => getDefault({ thunk: { extraArgument: extra } }).prepend(reviewsListener.middleware, shellLinkListener.middleware),
  });

export type AppStore = ReturnType<typeof createStore>;
export type AppDispatch = AppStore["dispatch"];

export const useAppDispatch = useDispatch.withTypes<AppDispatch>();
export const useAppSelector = useSelector.withTypes<RootState>();
export const useAppStore = useStore.withTypes<AppStore>();
