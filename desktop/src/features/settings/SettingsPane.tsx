import { Alert, Button, Checkbox, CircularProgress, FormControlLabel, MenuItem, TextField } from "@mui/material";
import { useEffect, useState, type JSX, type ReactNode } from "react";
import type { ListedProfile, NotificationSettings, OpenSettings, ReviewSettings, Runs } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { models, permissionModes } from "../create/NewAgentDialog";
import { isWeb } from "../../platform";
import { BrowserPushRow } from "../../web/BrowserPushRow";
import { AboutGroup } from "./AboutGroup";
import { ConfigSource } from "./ConfigSource";
import { Row } from "./Row";
import { RepoGroup } from "./RepoGroup";
import { RolesGroup } from "./RolesGroup";
import { profileKeys, ReviewerProfiles, setProfileKeys } from "./ReviewerProfiles";
import {
  fetchConfig,
  fetchOpenDefaults,
  fetchProfiles,
  fetchSettings,
  moveProfileOverrides,
  saveNotificationSettings,
  saveOpenSettings,
  saveReviewSettings,
  setConfig,
  settingOf,
  unsetConfig,
  type ReviewSettingKey,
} from "./settingsSlice";
import styles from "./SettingsPane.module.css";

export const checkIntervals: readonly number[] = [1, 2, 5, 10, 15, 30, 60];

const intervalLabel = (minutes: number): string =>
  minutes === 60 ? "Every hour" : `Every ${minutes} minute${minutes === 1 ? "" : "s"}`;

/** A text setting saved when it loses focus, so typing doesn't save every keystroke. */
const useDraft = <T,>(value: T): [T, (next: T) => void] => {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return [draft, setDraft];
};

const reviewTimeouts: readonly { readonly value: string; readonly label: string }[] = [
  { value: "15m", label: "15 minutes" },
  { value: "30m", label: "30 minutes" },
  { value: "1h", label: "1 hour" },
  { value: "1h30m", label: "1½ hours" },
  { value: "2h", label: "2 hours" },
  { value: "4h", label: "4 hours" },
];

/** The wait timeouts, with the current value added when it isn't one of them. */
const timeoutOptions = (current: string): readonly { readonly value: string; readonly label: string }[] =>
  current === "" || reviewTimeouts.some((option) => option.value === current)
    ? reviewTimeouts
    : [...reviewTimeouts, { value: current, label: current }];

interface ProfileRowChange {
  readonly runs?: Runs;
  readonly model?: string;
}

/** Saves a legacy desktop.json profile override as config.toml keys, then clears them (ADR 0054). */
const LegacyProfileOverrides = ({ count }: { readonly count: number }): JSX.Element => {
  const dispatch = useAppDispatch();
  const [failure, setFailure] = useState<string | null>(null);
  const move = (): void => {
    void dispatch(moveProfileOverrides()).then((result) => {
      setFailure(moveProfileOverrides.rejected.match(result) ? (result.payload?.message ?? result.error.message ?? "moving failed") : null);
    });
  };
  return (
    <Alert severity="info" action={<Button size="small" onClick={move}>Move to ws config</Button>}>
      This app still overrides {count} profile(s) for the reviews it starts. Move them to config.toml so the command line uses them too.
      {failure !== null && <div>{failure}</div>}
    </Alert>
  );
};

const ReviewsGroup = ({ settings }: { readonly settings: ReviewSettings }): JSX.Element => {
  const dispatch = useAppDispatch();
  const errors = useAppSelector((s) => s.settings.fieldErrors);
  const profiles = useAppSelector((s) => s.settings.profiles);
  const profilesError = useAppSelector((s) => s.settings.profilesError);
  const config = useAppSelector((s) => s.settings.config);
  const configErrors = useAppSelector((s) => s.settings.configErrors);
  const [max, setMax] = useDraft(String(settings.maxAutoReviews));
  useEffect(() => {
    void dispatch(fetchProfiles());
  }, [dispatch]);
  useEffect(() => {
    if (config === null) {
      void dispatch(fetchConfig());
    }
  }, [dispatch, config]);
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
  const timeout = settingOf(config, "review.timeout");
  const timeoutValue = typeof timeout?.value === "string" ? timeout.value : "";
  const removeFinished = settingOf(config, "review.remove_finished");
  const configPath = config?.path ?? "";
  const profileErrors = Object.entries(configErrors)
    .filter(([key]) => key.startsWith("review.profiles."))
    .map(([, message]) => message);
  const legacyCount = Object.keys(settings.profiles).length;
  const unsetKeys = (keys: readonly string[]): void => {
    void Promise.all(keys.map((key) => dispatch(unsetConfig(key)))).then(() => {
      void dispatch(fetchProfiles());
    });
  };
  const changeProfile = (profile: ListedProfile, change: ProfileRowChange): void => {
    const keys = profileKeys(profile.name);
    const refetchIfSaved = (saved: boolean): void => {
      if (saved) {
        void dispatch(fetchProfiles());
      }
    };
    if (change.runs !== undefined) {
      void dispatch(setConfig({ key: keys.when, values: [change.runs] })).then((result) => refetchIfSaved(setConfig.fulfilled.match(result)));
    } else if (change.model === undefined || change.model === "") {
      void dispatch(unsetConfig(keys.model)).then((result) => refetchIfSaved(unsetConfig.fulfilled.match(result)));
    } else {
      void dispatch(setConfig({ key: keys.model, values: [change.model] })).then((result) => refetchIfSaved(setConfig.fulfilled.match(result)));
    }
  };
  const resetProfile = (profile: ListedProfile): void => {
    const keys = profileKeys(profile.name);
    unsetKeys(setProfileKeys(config).filter((key) => key === keys.when || key === keys.model));
  };
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
        label="Wait for a review"
        help={
          <>
            How long ws review wait waits without --timeout. <ConfigSource setting={timeout} path={configPath} />
          </>
        }
        errors={[configErrors["review.timeout"]]}
      >
        <div className={styles.inline}>
          <TextField
            select
            size="small"
            value={timeoutValue}
            onChange={(e) => void dispatch(setConfig({ key: "review.timeout", values: [e.target.value] }))}
            slotProps={{ htmlInput: { "aria-label": "Review wait timeout" } }}
          >
            {timeoutOptions(timeoutValue).map((option) => (
              <MenuItem key={option.value} value={option.value}>
                {option.label}
              </MenuItem>
            ))}
          </TextField>
          {timeout?.set === true && (
            <Button size="small" onClick={() => void dispatch(unsetConfig("review.timeout"))}>
              Reset
            </Button>
          )}
        </div>
      </Row>
      <Row
        label="Clean up finished reviews"
        help={
          <>
            The findings are kept. <ConfigSource setting={removeFinished} path={configPath} />
          </>
        }
        errors={[configErrors["review.remove_finished"]]}
      >
        <FormControlLabel
          control={
            <Checkbox
              size="small"
              checked={removeFinished?.value === true}
              onChange={(e) => void dispatch(setConfig({ key: "review.remove_finished", values: [String(e.target.checked)] }))}
            />
          }
          label="Remove a review's agent and workspace once every reviewer has reported"
        />
      </Row>
      <Row
        label="Reviewer profiles"
        wide
        help={
          <>
            Each runs as its own subagent on the model chosen here. <strong>Always</strong> runs on every review;{" "}
            <strong>When files match</strong> runs when the diff touches its files. From <code>ws review profiles</code>.
          </>
        }
        errors={[error("profiles"), ...profileErrors]}
      >
        {legacyCount > 0 && <LegacyProfileOverrides count={legacyCount} />}
        <ReviewerProfiles
          profiles={profiles}
          error={profilesError?.message ?? null}
          config={config}
          onRowChange={changeProfile}
          onRowReset={resetProfile}
          onResetAll={() => unsetKeys(setProfileKeys(config))}
        />
      </Row>
      <Row
        label="Review agent"
        help={
          <>
            Model and permission mode of the agent that runs the subagents. It runs <code>ws review</code> and{" "}
            <code>git</code>; allow them in Claude Code's permissions, or it waits for you.
          </>
        }
        errors={[error("model"), error("permissionMode")]}
      >
        <div className={styles.inline}>
          <TextField
            select
            size="small"
            value={settings.model}
            onChange={(e) => save({ model: e.target.value })}
            slotProps={{ select: { displayEmpty: true }, htmlInput: { "aria-label": "Review agent model" } }}
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
            slotProps={{ select: { displayEmpty: true }, htmlInput: { "aria-label": "Review agent permission mode" } }}
          >
            {permissionModes.map((m) => (
              <MenuItem key={m} value={m}>
                {m === "" ? "default" : m}
              </MenuItem>
            ))}
          </TextField>
        </div>
      </Row>
      <Row
        label="Auto-review only"
        help="Which new PRs in an auto-review repo start a review."
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
    </section>
  );
};

const NotificationsGroup = ({ settings }: { readonly settings: NotificationSettings }): JSX.Element => {
  const dispatch = useAppDispatch();
  const error = useAppSelector((s) => s.settings.notificationsError);
  const save = (change: Partial<NotificationSettings>): void => {
    void dispatch(saveNotificationSettings(change));
  };
  return (
    <section className={styles.group} aria-label="Notifications">
      <h1 className={styles.title}>Notifications</h1>
      <Row
        label="Notify me"
        help={
          isWeb()
            ? "When one of your agents needs input or is done. These switches apply to every browser."
            : "When one of your agents needs input or is done. Children and hidden workspaces never notify."
        }
        errors={[error ?? undefined]}
      >
        <div className={styles.checks}>
          <FormControlLabel
            control={<Checkbox size="small" checked={settings.enabled} onChange={(e) => save({ enabled: e.target.checked })} />}
            label="Show notifications"
          />
          <FormControlLabel
            control={
              <Checkbox
                size="small"
                checked={settings.needsInput}
                disabled={!settings.enabled}
                onChange={(e) => save({ needsInput: e.target.checked })}
              />
            }
            label="When an agent needs input"
          />
          <FormControlLabel
            control={
              <Checkbox size="small" checked={settings.done} disabled={!settings.enabled} onChange={(e) => save({ done: e.target.checked })} />
            }
            label="When an agent is done"
          />
        </div>
      </Row>
      {isWeb() && <BrowserPushRow />}
    </section>
  );
};

interface OpenCommandRowProps {
  readonly label: string;
  readonly inputLabel: string;
  readonly help: ReactNode;
  readonly field: keyof OpenSettings;
  readonly value: string;
  readonly placeholder: string;
  readonly error: string | undefined;
}

/** One command, saved on blur when it changed. A rejected save puts back the saved value. */
const OpenCommandRow = ({ label, inputLabel, help, field, value, placeholder, error }: OpenCommandRowProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [draft, setDraft] = useDraft(value);
  const save = (): void => {
    const next = draft.trim();
    setDraft(next);
    if (next === value) {
      return;
    }
    void dispatch(saveOpenSettings({ [field]: next })).then((result) => {
      if (saveOpenSettings.rejected.match(result)) {
        setDraft(value);
      }
    });
  };
  return (
    <Row label={label} help={help} errors={[error]}>
      <TextField
        size="small"
        fullWidth
        className={styles.mono}
        value={draft}
        placeholder={placeholder}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={save}
        slotProps={{ htmlInput: { "aria-label": inputLabel } }}
      />
    </Row>
  );
};

const WorkspacesGroup = ({ settings }: { readonly settings: OpenSettings }): JSX.Element => {
  const dispatch = useAppDispatch();
  const defaults = useAppSelector((s) => s.settings.openDefaults);
  const errors = useAppSelector((s) => s.settings.openErrors);
  useEffect(() => {
    if (defaults === null) {
      void dispatch(fetchOpenDefaults());
    }
  }, [dispatch, defaults]);
  return (
    <section className={styles.group} aria-label="Workspaces">
      <h1 className={styles.title}>Workspaces</h1>
      {isWeb() && <p className={styles.help}>The web app has no Open buttons. These commands are kept on the server for parity with the desktop app.</p>}
      <OpenCommandRow
        label="File manager"
        inputLabel="File manager command"
        help="Opens a workspace's folder. Empty uses the system's. The folder is added at the end, or where {path} is."
        field="fileManager"
        value={settings.fileManager}
        placeholder={defaults?.fileManager ?? ""}
        error={errors.fileManager}
      />
      <OpenCommandRow
        label="Editor"
        inputLabel="Editor command"
        help={
          <>
            Opens a workspace in your editor: its repo when it has exactly one, otherwise the workspace folder. Empty uses VS Code's{" "}
            <code>code</code>. Try <code>open -a "Visual Studio Code"</code>, <code>cursor</code> or <code>zed</code>; use full paths,
            since ~ and $VARS aren't expanded.
          </>
        }
        field="editor"
        value={settings.editor}
        placeholder={defaults?.editor ?? ""}
        error={errors.editor}
      />
    </section>
  );
};

/** The chosen settings group. Changes save as they're made. */
export const SettingsPane = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const settings = useAppSelector((s) => s.settings.settings);
  const error = useAppSelector((s) => s.settings.error);
  const group = useAppSelector((s) => s.settings.group);
  useEffect(() => {
    void dispatch(fetchSettings());
  }, [dispatch]);
  if (group === "about") {
    return (
      <div className={styles.root}>
        <AboutGroup />
      </div>
    );
  }
  if (group === "repos") {
    return (
      <div className={styles.root}>
        <RepoGroup />
      </div>
    );
  }
  if (group === "roles") {
    return (
      <div className={styles.root}>
        <RolesGroup />
      </div>
    );
  }
  if (settings === null) {
    return (
      <div className={styles.loading}>
        {error === null ? <CircularProgress size={20} aria-label="Loading settings" /> : <Alert severity="error">{error.message}</Alert>}
      </div>
    );
  }
  if (group === "notifications") {
    return (
      <div className={styles.root}>
        <NotificationsGroup settings={settings.notifications} />
      </div>
    );
  }
  if (group === "workspaces") {
    return (
      <div className={styles.root}>
        <WorkspacesGroup settings={settings.open} />
      </div>
    );
  }
  return (
    <div className={styles.root}>
      <ReviewsGroup settings={settings.reviews} />
    </div>
  );
};
