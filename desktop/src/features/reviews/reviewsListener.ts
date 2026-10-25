import { createListenerMiddleware, type ThunkDispatch, type UnknownAction } from "@reduxjs/toolkit";
import { addRepo, removeRepo } from "../repos/reposSlice";
import type { RootState, ThunkExtra } from "../../store";
import { isGitHubRepo } from "./RepoSlots";
import { loadRepos, setWatch } from "./reviewsSlice";

type Dispatch = ThunkDispatch<RootState, ThunkExtra, UnknownAction>;

/**
 * Follows Repos' adding and removing (repos.md, "Slots for Reviews"): a repo
 * added with Watch on is watched, and a removed one leaves the Watched repos
 * list. Its switches stay in desktop.json for if it's added again (ADR 0034).
 */
export const reviewsListener = createListenerMiddleware<RootState, Dispatch>();

reviewsListener.startListening({
  actionCreator: addRepo.fulfilled,
  effect: async (action, { dispatch, getState }) => {
    const repo = action.payload.id;
    if (action.meta.arg.watch !== true || !isGitHubRepo(repo, getState().reviews.view)) {
      return;
    }
    await dispatch(setWatch({ repo, watch: true, autoReview: false }));
  },
});

reviewsListener.startListening({
  actionCreator: removeRepo.fulfilled,
  effect: async (_action, { dispatch }) => {
    await dispatch(loadRepos());
  },
});
