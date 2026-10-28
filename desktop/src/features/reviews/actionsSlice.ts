import { createSlice } from "@reduxjs/toolkit";
import {
  toAppError,
  type AppError,
  type CommentPlan,
  type FixFinding,
  type FixPlan,
  type FixTarget,
  type PostRequest,
  type ReviewReport,
} from "../../api";
import type { AppDispatch } from "../../store";
import { createAppThunk } from "../../thunk";
import { fetchEntries } from "../agents/agentsSlice";
import { attachKey, messageKeys, openTerminal, typeMessage } from "../terminals/terminalsSlice";
import { noticeShown } from "../ui/uiSlice";
import { fetchReport, reportReplaced } from "./reportsSlice";

/** The review a dialog acts on: its repo and reviewed commit. */
export interface ActionSubject {
  readonly prKey: string;
  readonly repo: string;
  readonly ref: string;
}

export interface CommentDialog extends ActionSubject {
  readonly plan: CommentPlan | null;
  readonly error: AppError | null;
  readonly posting: boolean;
}

export interface FixDialog extends ActionSubject {
  readonly plan: FixPlan | null;
  readonly error: AppError | null;
  readonly sending: boolean;
}

export interface ActionsState {
  readonly comment: CommentDialog | null;
  readonly fix: FixDialog | null;
}

const initialState: ActionsState = { comment: null, fix: null };

/**
 * How long, in milliseconds, a `claude attach` terminal opened to send a fix
 * gets before the message is typed, so Claude Code is reading input.
 */
export const attachSettle = 2000;

const delay = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

/** Reads what Comment would post, for the preview (ADR 0041). Posts nothing. */
export const openCommentDialog = createAppThunk<CommentPlan, ActionSubject & { readonly number: number }>(
  "actions/commentPlan",
  async ({ repo, ref, number }, { extra, rejectWithValue }) => {
    try {
      return await extra.api.reviewCommentPlan(repo, ref, number);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

/** The report after acting, or a fresh read when ws couldn't record it. */
const settle = async (
  subject: ActionSubject,
  report: ReviewReport | null,
  dispatch: AppDispatch,
): Promise<void> => {
  if (report !== null) {
    dispatch(reportReplaced({ repo: subject.repo, ref: subject.ref, report }));
    return;
  }
  await dispatch(fetchReport({ repo: subject.repo, ref: subject.ref, refresh: true }));
};

/** Posts the previewed review, then marks its findings commented. */
export const postReview = createAppThunk<string, ActionSubject & { readonly request: PostRequest }>(
  "actions/post",
  async ({ request, ...subject }, { extra, dispatch, rejectWithValue }) => {
    try {
      const posted = await extra.api.reviewCommentPost(subject.repo, subject.ref, request);
      await settle(subject, posted.report, dispatch);
      dispatch(
        noticeShown({
          severity: posted.warning === null ? "success" : "info",
          message: posted.warning ?? "Review posted.",
          action: { label: "View on GitHub", href: posted.url },
        }),
      );
      return posted.url;
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

/** Reads whom the findings decided `fix` would go to, and the message. */
export const openFixDialog = createAppThunk<
  FixPlan,
  ActionSubject & { readonly review: string; readonly findings: readonly FixFinding[] }
>("actions/fixPlan", async ({ repo, ref, review, findings }, { extra, rejectWithValue }) => {
  try {
    return await extra.api.reviewFixPlan(repo, ref, review, findings);
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

export interface SendFix extends ActionSubject {
  readonly plan: FixPlan;
  readonly target: FixTarget;
  readonly message: string;
}

/**
 * Sends the fix: typed into the requester's `claude attach` terminal, as
 * the reply box does, or as a new fix agent's task. Then marks the
 * findings sent to that agent.
 */
export const sendFix = createAppThunk<string, SendFix>(
  "actions/sendFix",
  async ({ plan, target, message, ...subject }, { extra, dispatch, getState, rejectWithValue }) => {
    try {
      if (target === "agent") {
        const started = await extra.api.reviewFixStart(subject.repo, subject.ref, {
          findings: plan.findings,
          agent: plan.agent,
          message,
        });
        await settle(subject, started.report, dispatch);
        void dispatch(fetchEntries());
        dispatch(
          noticeShown({
            severity: started.warning === null ? "success" : "info",
            message: started.warning ?? `Started ${started.agent}.`,
            action: { label: "Open agent", agent: started.agent },
          }),
        );
        return started.agent;
      }
      const requester = plan.requester;
      if (requester === null || !requester.available || requester.sessionId === null) {
        return rejectWithValue({ code: "usage", message: "The requesting agent can't take messages now." });
      }
      const key = attachKey(requester.sessionId);
      const open = (): boolean => getState().terminals.items.some((t) => t.key === key && t.state === "open");
      if (!open()) {
        await dispatch(
          openTerminal({
            key,
            title: requester.workspaceId,
            spec: { kind: "attach", sessionId: requester.sessionId, cwd: requester.path },
          }),
        );
        if (!open()) {
          return rejectWithValue({ code: "attach_failed", message: `Couldn't attach to ${requester.workspaceId}.` });
        }
        await delay(attachSettle);
      }
      await typeMessage(extra.terminals, key, messageKeys(message));
      const report = await extra.api.reviewFixSent(subject.repo, subject.ref, plan.findings, requester.workspaceId);
      dispatch(reportReplaced({ repo: subject.repo, ref: subject.ref, report }));
      dispatch(
        noticeShown({
          severity: "success",
          message: `Sent to ${requester.workspaceId}.`,
          action: { label: "Open agent", agent: requester.workspaceId },
        }),
      );
      return requester.workspaceId;
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

const subjectOf = ({ prKey, repo, ref }: ActionSubject): ActionSubject => ({ prKey, repo, ref });

const failure = (payload: AppError | undefined, message: string | undefined): AppError =>
  payload ?? { code: "internal", message: message ?? "failed" };

const actionsSlice = createSlice({
  name: "actions",
  initialState,
  reducers: {
    commentDialogClosed: (state): ActionsState => ({ ...state, comment: null }),
    fixDialogClosed: (state): ActionsState => ({ ...state, fix: null }),
  },
  extraReducers: (builder) => {
    builder
      .addCase(openCommentDialog.pending, (state, action): ActionsState => ({
        ...state,
        comment: { ...subjectOf(action.meta.arg), plan: null, error: null, posting: false },
      }))
      .addCase(openCommentDialog.fulfilled, (state, action): ActionsState =>
        state.comment?.prKey === action.meta.arg.prKey ? { ...state, comment: { ...state.comment, plan: action.payload } } : state,
      )
      .addCase(openCommentDialog.rejected, (state, action): ActionsState =>
        state.comment?.prKey === action.meta.arg.prKey
          ? { ...state, comment: { ...state.comment, error: failure(action.payload, action.error.message) } }
          : state,
      )
      .addCase(postReview.pending, (state): ActionsState =>
        state.comment === null ? state : { ...state, comment: { ...state.comment, posting: true, error: null } },
      )
      .addCase(postReview.fulfilled, (state): ActionsState => ({ ...state, comment: null }))
      .addCase(postReview.rejected, (state, action): ActionsState =>
        state.comment === null
          ? state
          : { ...state, comment: { ...state.comment, posting: false, error: failure(action.payload, action.error.message) } },
      )
      .addCase(openFixDialog.pending, (state, action): ActionsState => ({
        ...state,
        fix: { ...subjectOf(action.meta.arg), plan: null, error: null, sending: false },
      }))
      .addCase(openFixDialog.fulfilled, (state, action): ActionsState =>
        state.fix?.prKey === action.meta.arg.prKey ? { ...state, fix: { ...state.fix, plan: action.payload } } : state,
      )
      .addCase(openFixDialog.rejected, (state, action): ActionsState =>
        state.fix?.prKey === action.meta.arg.prKey
          ? { ...state, fix: { ...state.fix, error: failure(action.payload, action.error.message) } }
          : state,
      )
      .addCase(sendFix.pending, (state): ActionsState =>
        state.fix === null ? state : { ...state, fix: { ...state.fix, sending: true, error: null } },
      )
      .addCase(sendFix.fulfilled, (state): ActionsState => ({ ...state, fix: null }))
      .addCase(sendFix.rejected, (state, action): ActionsState =>
        state.fix === null
          ? state
          : { ...state, fix: { ...state.fix, sending: false, error: failure(action.payload, action.error.message) } },
      );
  },
});

export const { commentDialogClosed, fixDialogClosed } = actionsSlice.actions;
export const actionsReducer = actionsSlice.reducer;
