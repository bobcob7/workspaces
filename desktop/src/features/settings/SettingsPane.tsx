import { Alert, Checkbox, CircularProgress, FormControlLabel, MenuItem, TextField } from "@mui/material";
import { useEffect, useState, type JSX, type ReactNode } from "react";
import type { ReviewSettings } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { models, permissionModes } from "../create/NewAgentDialog";
import { fetchSettings, saveReviewSettings, type ReviewSettingKey } from "./settingsSlice";
import styles from "./SettingsPane.module.css";

export const checkIntervals: readonly number[] = [1, 2, 5, 10, 15, 30, 60];

const intervalLabel = (minutes: number): string =>
  minutes === 60 ? "Every hour" : `Every ${minutes} minute${minutes === 1 ? "" : "s"}`;

interface RowProps {
  readonly label: string;
  readonly help?: string;
  readonly errors: readonly (string | undefined)[];
  readonly children: ReactNode;
}

const Row = ({ label, help, errors, children }: RowProps): JSX.Element => {
  const shown = errors.filter((e) => e !== undefined);
  return (
    <div className={styles.row} role="group" aria-label={label}>
      <div className={styles.text}>
        <span className={styles.label}>{label}</span>
        {help !== undefined && <span className={styles.help}>{help}</span>}
      </div>
      <div className={styles.control}>
        {children}
        {shown.length > 0 && (
          <span className={styles.error} role="alert">
            {shown[0]}
          </span>
        )}
      </div>
    </div>
  );
};

/** A text setting saved when it loses focus, so typing doesn't save every keystroke. */
const useDraft = <T,>(value: T): [T, (next: T) => void] => {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return [draft, setDraft];
};

const ReviewsGroup = ({ settings }: { readonly settings: ReviewSettings }): JSX.Element => {
  const dispatch = useAppDispatch();
  const errors = useAppSelector((s) => s.settings.fieldErrors);
  const [template, setTemplate] = useDraft(settings.taskTemplate);
  const [max, setMax] = useDraft(String(settings.maxAutoReviews));
  const save = (change: Partial<ReviewSettings>, revert?: () => void): void => {
    void dispatch(saveReviewSettings(change)).then((result) => {
      if (saveReviewSettings.rejected.match(result) && revert !== undefined) {
        revert();
      }
    });
  };
  const error = (key: ReviewSettingKey): string | undefined => errors[key];
  const intervals = checkIntervals.includes(settings.checkIntervalMinutes)
    ? checkIntervals
    : [...checkIntervals, settings.checkIntervalMinutes].toSorted((a, b) => a - b);
  return (
    <section className={styles.group} aria-label="Reviews">
      <h1 className={styles.title}>Reviews</h1>
      <Row label="Check for pull requests" help="How often watched repos are checked with gh." errors={[error("checkIntervalMinutes")]}>
        <TextField
          select
          size="small"
          value={settings.checkIntervalMinutes}
          onChange={(e) => save({ checkIntervalMinutes: Number(e.target.value) })}
          slotProps={{ htmlInput: { "aria-label": "Check interval" } }}
        >
          {intervals.map((m) => (
            <MenuItem key={m} value={m}>
              {intervalLabel(m)}
            </MenuItem>
          ))}
        </TextField>
      </Row>
      <Row
        label="Auto-review only"
        help="Which new PRs in an auto-review repo start an agent."
        errors={[error("skipDrafts"), error("skipMine"), error("onlyRequested")]}
      >
        <div className={styles.checks}>
          <FormControlLabel
            control={<Checkbox size="small" checked={settings.skipDrafts} onChange={(e) => save({ skipDrafts: e.target.checked })} />}
            label="Skip drafts"
          />
          <FormControlLabel
            control={<Checkbox size="small" checked={settings.skipMine} onChange={(e) => save({ skipMine: e.target.checked })} />}
            label="Skip pull requests I opened"
          />
          <FormControlLabel
            control={
              <Checkbox size="small" checked={settings.onlyRequested} onChange={(e) => save({ onlyRequested: e.target.checked })} />
            }
            label="Only when my review is requested"
          />
        </div>
      </Row>
      <Row label="At most" help="Auto-reviews running at once; the rest wait." errors={[error("maxAutoReviews")]}>
        <div className={styles.inline}>
          <TextField
            size="small"
            type="number"
            value={max}
            onChange={(e) => setMax(e.target.value)}
            onBlur={() => {
              if (Number(max) !== settings.maxAutoReviews) {
                save({ maxAutoReviews: Number(max) }, () => setMax(String(settings.maxAutoReviews)));
              }
            }}
            slotProps={{ htmlInput: { min: 1, max: 20, "aria-label": "Auto-reviews at once", className: styles.number } }}
          />
          <span className={styles.help}>review agents</span>
        </div>
      </Row>
      <Row
        label="Review task"
        help="Sent to each review agent. {repo}, {number}, {title}, {author}, {base}, {head}, and {body} are filled in."
        errors={[error("taskTemplate")]}
      >
        <TextField
          multiline
          minRows={6}
          maxRows={16}
          fullWidth
          value={template}
          onChange={(e) => setTemplate(e.target.value)}
          onBlur={() => {
            if (template !== settings.taskTemplate) {
              save({ taskTemplate: template }, () => setTemplate(settings.taskTemplate));
            }
          }}
          slotProps={{ htmlInput: { "aria-label": "Review task", className: styles.template } }}
        />
      </Row>
      <Row label="Model and permission mode" errors={[error("model"), error("permissionMode")]}>
        <div className={styles.inline}>
          <TextField
            select
            size="small"
            value={settings.model}
            onChange={(e) => save({ model: e.target.value })}
            slotProps={{ select: { displayEmpty: true }, htmlInput: { "aria-label": "Model" } }}
          >
            {models.map((m) => (
              <MenuItem key={m} value={m}>
                {m === "" ? "Default" : m}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            select
            size="small"
            value={settings.permissionMode}
            onChange={(e) => save({ permissionMode: e.target.value })}
            slotProps={{ select: { displayEmpty: true }, htmlInput: { "aria-label": "Permission mode" } }}
          >
            {permissionModes.map((m) => (
              <MenuItem key={m} value={m}>
                {m === "" ? "default" : m}
              </MenuItem>
            ))}
          </TextField>
        </div>
      </Row>
    </section>
  );
};

/** The chosen settings group. Changes save as they're made. */
export const SettingsPane = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const settings = useAppSelector((s) => s.settings.settings);
  const error = useAppSelector((s) => s.settings.error);
  useEffect(() => {
    void dispatch(fetchSettings());
  }, [dispatch]);
  if (settings === null) {
    return (
      <div className={styles.loading}>
        {error === null ? <CircularProgress size={20} aria-label="Loading settings" /> : <Alert severity="error">{error.message}</Alert>}
      </div>
    );
  }
  return (
    <div className={styles.root}>
      <ReviewsGroup settings={settings.reviews} />
    </div>
  );
};
