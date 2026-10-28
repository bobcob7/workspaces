import {
  Alert,
  Button,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  MenuItem,
  Tab,
  Tabs,
  TextField,
} from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { AppError, ListedProfile, ProfileDetail, ProfileDraft, Runs } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { MarkdownText } from "../conversation/ConversationView";
import { fetchProfileDetail, saveProfile } from "./settingsSlice";
import styles from "./ProfileEditor.module.css";

export const runsLabels: Readonly<Record<Runs, string>> = { always: "Always", auto: "When files match", off: "Off" };

/** ws's limit on a description, in characters. */
export const maxDescription = 120;

/** What the editor's fields hold. */
export interface ProfileForm {
  readonly name: string;
  readonly description: string;
  readonly patterns: readonly string[];
  readonly guide: string;
  /** A new profile's Runs, passed as --when; ignored when editing. */
  readonly runs: Runs;
}

const emptyForm: ProfileForm = { name: "", description: "", patterns: [], guide: "", runs: "always" };

const formOf = (detail: ProfileDetail): ProfileForm => ({
  name: detail.name,
  description: detail.description,
  patterns: detail.patterns,
  guide: detail.guide,
  runs: detail.when,
});

/** Why a new profile's name can't be used, or null. */
export const nameError = (name: string, taken: readonly string[]): string | null => {
  if (name === "") {
    return "Give the profile a name";
  }
  if (!/^[a-z0-9-]+$/.test(name)) {
    return "Lowercase letters, digits, and hyphens only";
  }
  if (name === "decisions") {
    return "decisions is reserved; choose another name";
  }
  if (taken.includes(name)) {
    return `A profile named ${name} already exists`;
  }
  return null;
};

/** Why the form can't be saved, by field. */
export interface FormErrors {
  readonly name?: string;
  readonly description?: string;
  readonly patterns?: string;
  readonly guide?: string;
}

export const formErrors = (form: ProfileForm, creating: boolean, taken: readonly string[]): FormErrors => {
  const errors: { -readonly [K in keyof FormErrors]: FormErrors[K] } = {};
  const name = creating ? nameError(form.name, taken) : null;
  if (name !== null) {
    errors.name = name;
  }
  if (form.description.trim() === "") {
    errors.description = "Say in one line what it reviews";
  } else if ([...form.description.trim()].length > maxDescription) {
    errors.description = `At most ${maxDescription} characters`;
  }
  if (creating && form.runs === "auto" && form.patterns.length === 0) {
    errors.patterns = "When files match needs a file pattern";
  }
  if (form.guide.trim() === "") {
    errors.guide = "The guide tells the reviewer what to look for; it can't be empty";
  }
  return errors;
};

const samePatterns = (a: readonly string[], b: readonly string[]): boolean =>
  a.length === b.length && a.every((p, i) => p === b[i]);

/**
 * What saving the form passes to ws. A new profile passes every field and
 * its Runs as --when. An edit passes only what changed, or null for
 * nothing, so saving an untouched built-in doesn't make an edited copy.
 * Removing every pattern from a profile that runs when files match also
 * makes it run always, since ws refuses an auto profile without patterns.
 */
export const draftOf = (form: ProfileForm, loaded: ProfileDetail | null): ProfileDraft | null => {
  const description = form.description.trim();
  if (loaded === null) {
    return { description, when: form.runs, patterns: form.patterns, guide: form.guide };
  }
  const draft: { -readonly [K in keyof ProfileDraft]: ProfileDraft[K] } = {};
  if (description !== loaded.description) {
    draft.description = description;
  }
  if (!samePatterns(form.patterns, loaded.patterns)) {
    draft.patterns = form.patterns;
    if (form.patterns.length === 0 && loaded.when === "auto") {
      draft.when = "always";
    }
  }
  if (form.guide.trim() !== loaded.guide.trim()) {
    draft.guide = form.guide;
  }
  return Object.keys(draft).length === 0 ? null : draft;
};

interface PatternsFieldProps {
  readonly patterns: readonly string[];
  readonly error: string | undefined;
  readonly onChange: (patterns: readonly string[]) => void;
}

const PatternsField = ({ patterns, error, onChange }: PatternsFieldProps): JSX.Element => {
  const [draft, setDraft] = useState("");
  const add = (): void => {
    const pattern = draft.trim().replace(/,$/, "");
    setDraft("");
    if (pattern !== "" && !patterns.includes(pattern)) {
      onChange([...patterns, pattern]);
    }
  };
  return (
    <div className={styles.field} role="group" aria-label="File patterns">
      <span className={styles.label}>File patterns</span>
      <div className={styles.chips}>
        {patterns.map((p) => (
          <Chip
            key={p}
            size="small"
            label={p}
            className={styles.mono}
            onDelete={() => onChange(patterns.filter((q) => q !== p))}
            slotProps={{ root: { "aria-label": p } }}
          />
        ))}
        <TextField
          size="small"
          variant="standard"
          value={draft}
          placeholder={patterns.length === 0 ? "Add a pattern, such as *.go, so it can run when files match" : "Add a pattern"}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={add}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === ",") {
              e.preventDefault();
              add();
            } else if (e.key === "Backspace" && draft === "" && patterns.length > 0) {
              onChange(patterns.slice(0, -1));
            }
          }}
          className={styles.patternInput}
          slotProps={{ htmlInput: { "aria-label": "Add a file pattern", className: styles.mono } }}
        />
      </div>
      {error !== undefined && (
        <span className={styles.error} role="alert">
          {error}
        </span>
      )}
    </div>
  );
};

interface GuideFieldProps {
  readonly guide: string;
  readonly error: string | undefined;
  readonly onChange: (guide: string) => void;
}

const GuideField = ({ guide, error, onChange }: GuideFieldProps): JSX.Element => {
  const [tab, setTab] = useState<"write" | "preview">("write");
  return (
    <div className={styles.field}>
      <div className={styles.tabs}>
        <Tabs value={tab} onChange={(_e, next: "write" | "preview") => setTab(next)} aria-label="Guide view">
          <Tab value="write" label="Write" />
          <Tab value="preview" label="Preview" />
        </Tabs>
        <span className={styles.hint}>Markdown · the reviewer&apos;s instructions</span>
      </div>
      {tab === "write" ? (
        <TextField
          fullWidth
          multiline
          minRows={10}
          maxRows={18}
          value={guide}
          error={error !== undefined}
          helperText={error}
          onChange={(e) => onChange(e.target.value)}
          slotProps={{ htmlInput: { "aria-label": "Guide", className: styles.mono } }}
        />
      ) : (
        <div className={styles.preview} aria-label="Guide preview">
          {guide.trim() === "" ? <span className={styles.hint}>Nothing to preview yet.</span> : <MarkdownText text={guide} />}
        </div>
      )}
    </div>
  );
};

interface EditorFormProps {
  readonly loaded: ProfileDetail | null;
  readonly taken: readonly string[];
  readonly onClose: () => void;
}

const EditorForm = ({ loaded, taken, onClose }: EditorFormProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const creating = loaded === null;
  const [form, setForm] = useState<ProfileForm>(loaded === null ? emptyForm : formOf(loaded));
  const [tried, setTried] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<AppError | null>(null);
  const errors = formErrors(form, creating, taken);
  const shown: FormErrors = tried ? errors : {};
  const nameTaken = failure?.code === "profile_exists" ? failure.message : undefined;
  const set = (change: Partial<ProfileForm>): void => setForm({ ...form, ...change });
  const save = async (): Promise<void> => {
    setTried(true);
    if (Object.keys(errors).length > 0) {
      return;
    }
    const draft = draftOf(form, loaded);
    if (draft === null) {
      onClose();
      return;
    }
    setSaving(true);
    setFailure(null);
    const result = await dispatch(saveProfile({ name: form.name, draft, create: creating }));
    setSaving(false);
    if (saveProfile.fulfilled.match(result)) {
      onClose();
      return;
    }
    setFailure(result.payload ?? { code: "internal", message: result.error.message ?? "saving the profile failed" });
  };
  return (
    <>
      <DialogContent className={styles.content}>
        {loaded?.source === "builtin" && (
          <Alert severity="info">
            <b>{loaded.name}</b> is built in. Saving makes an edited copy in your profiles; <b>Reset to built-in</b> undoes it.
          </Alert>
        )}
        {failure !== null && nameTaken === undefined && <Alert severity="error">{failure.message}</Alert>}
        <div className={styles.fields}>
          {creating ? (
            <TextField
              label="Name"
              size="small"
              value={form.name}
              autoFocus
              error={shown.name !== undefined || nameTaken !== undefined}
              helperText={shown.name ?? nameTaken ?? "Lowercase letters, digits, and hyphens"}
              onChange={(e) => {
                set({ name: e.target.value });
                if (nameTaken !== undefined) {
                  setFailure(null);
                }
              }}
              className={styles.name}
              slotProps={{ htmlInput: { className: styles.mono } }}
            />
          ) : (
            <div className={styles.field}>
              <span className={styles.label}>Name</span>
              <span className={styles.mono}>{form.name}</span>
            </div>
          )}
          <TextField
            label="Description"
            size="small"
            value={form.description}
            error={shown.description !== undefined}
            helperText={shown.description ?? "One line, shown in the table and the report"}
            onChange={(e) => set({ description: e.target.value })}
            className={styles.description}
          />
          {creating && (
            <TextField
              select
              label="Runs"
              size="small"
              value={form.runs}
              onChange={(e) => set({ runs: e.target.value as Runs })}
              className={styles.runs}
            >
              {(["always", "auto", "off"] as const).map((runs) => (
                <MenuItem key={runs} value={runs}>
                  {runsLabels[runs]}
                </MenuItem>
              ))}
            </TextField>
          )}
        </div>
        <PatternsField patterns={form.patterns} error={shown.patterns} onChange={(patterns) => set({ patterns })} />
        <GuideField guide={form.guide} error={shown.guide} onChange={(guide) => set({ guide })} />
      </DialogContent>
      <DialogActions>
        <span className={styles.applies}>Applies to reviews started after you save</span>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="contained" disabled={saving} onClick={() => void save()}>
          {saving ? "Saving…" : "Save"}
        </Button>
      </DialogActions>
    </>
  );
};

export interface ProfileEditorProps {
  /** The profile to edit, or null for a new one. */
  readonly profile: ListedProfile | null;
  readonly onClose: () => void;
}

/**
 * Adds a reviewer profile, or edits one's description, file patterns, and
 * guide (ADR 0042). Saving runs `ws review profiles add` or `edit`; an
 * error keeps the dialog open with ws's message.
 */
export const ProfileEditor = ({ profile, onClose }: ProfileEditorProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const profiles = useAppSelector((s) => s.settings.profiles);
  const [loaded, setLoaded] = useState<ProfileDetail | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const name = profile?.name ?? null;
  useEffect(() => {
    if (name === null) {
      return;
    }
    let live = true;
    void dispatch(fetchProfileDetail(name)).then((result) => {
      if (!live) {
        return;
      }
      if (fetchProfileDetail.fulfilled.match(result)) {
        setLoaded(result.payload);
      } else {
        setLoadError(result.payload?.message ?? result.error.message ?? "reading the profile failed");
      }
    });
    return () => {
      live = false;
    };
  }, [dispatch, name]);
  const taken = (profiles ?? []).map((p) => p.name);
  const body = (): JSX.Element => {
    if (profile === null) {
      return <EditorForm loaded={null} taken={taken} onClose={onClose} />;
    }
    if (loaded !== null) {
      return <EditorForm loaded={loaded} taken={taken} onClose={onClose} />;
    }
    return (
      <DialogContent className={styles.loading}>
        {loadError === null ? <CircularProgress size={20} aria-label="Loading profile" /> : <Alert severity="error">{loadError}</Alert>}
      </DialogContent>
    );
  };
  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="md" aria-labelledby="profile-editor-title">
      <DialogTitle id="profile-editor-title">{profile === null ? "New reviewer profile" : "Edit reviewer profile"}</DialogTitle>
      {body()}
    </Dialog>
  );
};
