import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import { toAppError, type AppError, type DecisionKind, type ReviewReport } from "../../api";
import { createAppThunk } from "../../thunk";
import { noticeShown } from "../ui/uiSlice";
import { noFilter, type ReportFilter } from "./report";

/** A review's report, by repo and commit. */
export const reportKey = (repo: string, ref: string): string => `${repo}@${ref}`;

export interface ReportEntry {
  readonly report: ReviewReport | null;
  readonly error: AppError | null;
  readonly loading: boolean;
}

export type DetailTab = "findings" | "description" | "agent";

/** How a pull request's report is being looked at, kept for the session. */
export interface Triage {
  /** null for the default: Findings once there is a report. */
  readonly tab: DetailTab | null;
  /** The review shown, by workspace; null for the newest. */
  readonly viewed: string | null;
  readonly filter: ReportFilter;
  readonly optionalOpen: boolean;
  readonly expanded: readonly string[];
  /** Findings picked with ⌘- or Shift-click, decided together. */
  readonly selected: readonly string[];
  readonly focused: string | null;
  /** Notes typed on findings that have no decision yet, saved with the next one. */
  readonly notes: Readonly<Record<string, string>>;
}

export const defaultTriage: Triage = {
  tab: null,
  viewed: null,
  filter: noFilter,
  optionalOpen: false,
  expanded: [],
  selected: [],
  focused: null,
  notes: {},
};

export interface ReportsState {
  readonly byKey: Readonly<Record<string, ReportEntry>>;
  /** By pull request key. */
  readonly triage: Readonly<Record<string, Triage>>;
  /** Finding ids with a decision being saved. */
  readonly deciding: readonly string[];
}

const initialState: ReportsState = { byKey: {}, triage: {}, deciding: [] };

export interface ReportRequest {
  readonly repo: string;
  readonly ref: string;
  /** Read it again rather than take the session's copy. */
  readonly refresh: boolean;
}

/** Reads a review's report (`ws review report --json`). */
export const fetchReport = createAppThunk<ReviewReport, ReportRequest>(
  "reports/fetch",
  async ({ repo, ref, refresh }, { extra, rejectWithValue }) => {
    try {
      return await extra.api.reviewReport(repo, ref, refresh);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
  { condition: ({ repo, ref }, { getState }) => getState().reports.byKey[reportKey(repo, ref)]?.loading !== true },
);

export interface DecideFindings {
  readonly repo: string;
  readonly ref: string;
  /** Each finding with the note to keep with its decision. */
  readonly findings: readonly { readonly id: string; readonly note: string }[];
  readonly decision: DecisionKind | "clear";
  readonly suggestion?: number | null;
}

/**
 * Saves one click's decision (`ws review decide`): one call for all the
 * findings, or one per distinct note, since a call carries one note.
 */
export const decideFindings = createAppThunk<ReviewReport, DecideFindings>(
  "reports/decide",
  async ({ repo, ref, findings, decision, suggestion = null }, { extra, dispatch, rejectWithValue }) => {
    const byNote = new Map<string, string[]>();
    for (const f of findings) {
      const note = decision === "clear" ? "" : f.note.trim();
      byNote.set(note, [...(byNote.get(note) ?? []), f.id]);
    }
    try {
      let report: ReviewReport | null = null;
      for (const [note, ids] of byNote) {
        report = await extra.api.reviewDecide(repo, ref, { findings: ids, decision, suggestion, note });
      }
      return report ?? (await extra.api.reviewReport(repo, ref, false));
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

/** Writes the HTML report to the app's cache and opens it in the browser. */
export const openFullReport = createAppThunk<string, { readonly repo: string; readonly ref: string }>(
  "reports/openFull",
  async ({ repo, ref }, { extra, dispatch, rejectWithValue }) => {
    try {
      return await extra.api.reviewOpenReport(repo, ref);
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

interface ForPr<T> {
  readonly pr: string;
  readonly value: T;
}

const toggle = (list: readonly string[], id: string): readonly string[] =>
  list.includes(id) ? list.filter((x) => x !== id) : [...list, id];

/** The state with one pull request's triage changed. */
const withTriage = (state: ReportsState, pr: string, change: (triage: Triage) => Partial<Triage>): ReportsState => {
  const triage = state.triage[pr] ?? defaultTriage;
  return { ...state, triage: { ...state.triage, [pr]: { ...triage, ...change(triage) } } };
};

const withReport = (state: ReportsState, key: string, entry: ReportEntry): ReportsState => ({
  ...state,
  byKey: { ...state.byKey, [key]: entry },
});

const reportsSlice = createSlice({
  name: "reports",
  initialState,
  reducers: {
    tabChosen: (state, action: PayloadAction<ForPr<DetailTab>>): ReportsState =>
      withTriage(state, action.payload.pr, () => ({ tab: action.payload.value })),
    reviewViewed: (state, action: PayloadAction<ForPr<string | null>>): ReportsState =>
      withTriage(state, action.payload.pr, () => ({ viewed: action.payload.value, expanded: [], selected: [], focused: null })),
    filterChanged: (state, action: PayloadAction<ForPr<Partial<ReportFilter>>>): ReportsState =>
      withTriage(state, action.payload.pr, (t) => ({ filter: { ...t.filter, ...action.payload.value } })),
    optionalToggled: (state, action: PayloadAction<string>): ReportsState =>
      withTriage(state, action.payload, (t) => ({ optionalOpen: !t.optionalOpen })),
    findingToggled: (state, action: PayloadAction<ForPr<string>>): ReportsState =>
      withTriage(state, action.payload.pr, (t) => ({
        expanded: toggle(t.expanded, action.payload.value),
        focused: action.payload.value,
      })),
    findingFocused: (state, action: PayloadAction<ForPr<string>>): ReportsState =>
      withTriage(state, action.payload.pr, () => ({ focused: action.payload.value })),
    selectionToggled: (state, action: PayloadAction<ForPr<string>>): ReportsState =>
      withTriage(state, action.payload.pr, (t) => ({
        selected: toggle(t.selected, action.payload.value),
        focused: action.payload.value,
      })),
    selectionCleared: (state, action: PayloadAction<string>): ReportsState =>
      withTriage(state, action.payload, () => ({ selected: [] })),
    /** A report `ws` printed after acting on findings. */
    reportReplaced: (state, action: PayloadAction<{ readonly repo: string; readonly ref: string; readonly report: ReviewReport }>): ReportsState => {
      const key = reportKey(action.payload.repo, action.payload.ref);
      return withReport(state, key, { report: action.payload.report, error: null, loading: state.byKey[key]?.loading ?? false });
    },
    noteDrafted: (state, action: PayloadAction<ForPr<{ readonly id: string; readonly text: string }>>): ReportsState =>
      withTriage(state, action.payload.pr, (t) => ({
        notes: { ...t.notes, [action.payload.value.id]: action.payload.value.text },
      })),
  },
  extraReducers: (builder) => {
    builder
      .addCase(fetchReport.pending, (state, action): ReportsState => {
        const key = reportKey(action.meta.arg.repo, action.meta.arg.ref);
        const entry = state.byKey[key];
        return withReport(state, key, { report: entry?.report ?? null, error: entry?.error ?? null, loading: true });
      })
      .addCase(fetchReport.fulfilled, (state, action): ReportsState =>
        withReport(state, reportKey(action.meta.arg.repo, action.meta.arg.ref), {
          report: action.payload,
          error: null,
          loading: false,
        }),
      )
      .addCase(fetchReport.rejected, (state, action): ReportsState => {
        const key = reportKey(action.meta.arg.repo, action.meta.arg.ref);
        return withReport(state, key, {
          report: state.byKey[key]?.report ?? null,
          error: action.payload ?? { code: "internal", message: action.error.message ?? "reading the report failed" },
          loading: false,
        });
      })
      .addCase(decideFindings.pending, (state, action): ReportsState => ({
        ...state,
        deciding: [...state.deciding, ...action.meta.arg.findings.map((f) => f.id)],
      }))
      .addCase(decideFindings.fulfilled, (state, action): ReportsState => {
        const ids = action.meta.arg.findings.map((f) => f.id);
        const key = reportKey(action.meta.arg.repo, action.meta.arg.ref);
        const next = withReport(state, key, { report: action.payload, error: null, loading: state.byKey[key]?.loading ?? false });
        const triage = Object.fromEntries(
          Object.entries(next.triage).map(([pr, t]) => [
            pr,
            { ...t, notes: Object.fromEntries(Object.entries(t.notes).filter(([id]) => !ids.includes(id))) },
          ]),
        );
        return { ...next, triage, deciding: next.deciding.filter((id) => !ids.includes(id)) };
      })
      .addCase(decideFindings.rejected, (state, action): ReportsState => {
        const ids = action.meta.arg.findings.map((f) => f.id);
        return { ...state, deciding: state.deciding.filter((id) => !ids.includes(id)) };
      });
  },
});

export const {
  tabChosen,
  reviewViewed,
  filterChanged,
  optionalToggled,
  findingToggled,
  findingFocused,
  selectionToggled,
  selectionCleared,
  noteDrafted,
  reportReplaced,
} = reportsSlice.actions;
export const reportsReducer = reportsSlice.reducer;
