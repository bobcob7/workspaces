import {
  Alert,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  MenuItem,
  TextField,
  useMediaQuery,
} from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { AppError, ListedRole, RoleDetail, RoleDraft } from "../../api";
import { useAppDispatch } from "../../store";
import { models } from "../create/NewAgentDialog";
import { fetchRoleDetail, saveRole } from "./settingsSlice";
import styles from "./ProfileEditor.module.css";

/** ws's limit on a description, in characters. */
const maxDescription = 120;

/** A role's name: lowercase letters, digits, and hyphens, starting with a letter or digit. */
const namePattern = /^[a-z0-9][a-z0-9-]{0,63}$/;

/** The Model menu's choice that switches to typing a model id. */
const typeModel = "\u0000type";

/** What the editor's fields hold. The lists are comma-separated, and the arguments one per line. */
interface RoleForm {
  readonly name: string;
  readonly description: string;
  readonly model: string;
  readonly pass: string;
  readonly fail: string;
  readonly vars: string;
  readonly claudeArgs: string;
  readonly prompt: string;
}

/** Why the form can't be saved, by field. */
interface RoleFormErrors {
  readonly name?: string;
  readonly description?: string;
  readonly prompt?: string;
}

const splitList = (text: string): readonly string[] =>
  text
    .split(",")
    .map((item) => item.trim())
    .filter((item) => item !== "");

const splitLines = (text: string): readonly string[] =>
  text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");

const formOf = (role: RoleDetail | null): RoleForm =>
  role === null
    ? { name: "", description: "", model: "", pass: "PASS", fail: "FAIL", vars: "", claudeArgs: "", prompt: "" }
    : {
        name: role.name,
        description: role.description,
        model: role.model,
        pass: role.pass.join(", "),
        fail: role.fail.join(", "),
        vars: role.vars.join(", "),
        claudeArgs: role.claudeArgs.join("\n"),
        prompt: role.prompt,
      };

const formErrors = (form: RoleForm, isNew: boolean): RoleFormErrors => {
  const errors: { -readonly [K in keyof RoleFormErrors]: RoleFormErrors[K] } = {};
  if (isNew && !namePattern.test(form.name)) {
    errors.name = "Lowercase letters, digits, and hyphens, up to 64, starting with a letter or digit";
  }
  const description = form.description.trim();
  if (description === "") {
    errors.description = "Say in one line what the role does";
  } else if ([...description].length > maxDescription) {
    errors.description = `At most ${maxDescription} characters`;
  }
  if (form.prompt.trim() === "") {
    errors.prompt = "The prompt is the role's instructions; it can't be empty";
  }
  return errors;
};

const draftOf = (form: RoleForm): RoleDraft => ({
  description: form.description.trim(),
  model: form.model,
  pass: splitList(form.pass),
  fail: splitList(form.fail),
  vars: splitList(form.vars),
  claudeArgs: splitLines(form.claudeArgs),
  prompt: form.prompt,
});

interface EditorFormProps {
  readonly loaded: RoleDetail | null;
  readonly onClose: () => void;
}

const EditorForm = ({ loaded, onClose }: EditorFormProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const isNew = loaded === null;
  const [form, setForm] = useState<RoleForm>(formOf(loaded));
  const [typing, setTyping] = useState(form.model !== "" && !models.includes(form.model as (typeof models)[number]));
  const [tried, setTried] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<AppError | null>(null);
  const errors = formErrors(form, isNew);
  const shown: RoleFormErrors = tried ? errors : {};
  const set = (change: Partial<RoleForm>): void => setForm({ ...form, ...change });
  const save = async (): Promise<void> => {
    setTried(true);
    if (Object.keys(errors).length > 0) {
      return;
    }
    setSaving(true);
    setFailure(null);
    const result = await dispatch(saveRole({ name: loaded?.name ?? form.name, draft: draftOf(form), isNew }));
    setSaving(false);
    if (saveRole.fulfilled.match(result)) {
      onClose();
      return;
    }
    setFailure(result.payload ?? { code: "internal", message: result.error.message ?? "saving the role failed" });
  };
  return (
    <>
      <DialogContent className={styles.content}>
        {loaded?.source === "builtin" && (
          <Alert severity="info">Saving writes your own copy; Remove brings the built-in back.</Alert>
        )}
        {failure !== null && <Alert severity="error">{failure.message}</Alert>}
        <div className={styles.fields}>
          {isNew ? (
            <TextField
              label="Name"
              size="small"
              value={form.name}
              autoFocus
              error={shown.name !== undefined}
              helperText={shown.name ?? "Lowercase letters, digits, and hyphens"}
              onChange={(e) => set({ name: e.target.value })}
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
            helperText={shown.description ?? `${[...form.description].length}/${maxDescription}`}
            onChange={(e) => set({ description: e.target.value })}
            className={styles.description}
          />
        </div>
        <div className={styles.fields}>
          <TextField
            select
            label="Model"
            size="small"
            value={typing ? typeModel : form.model}
            onChange={(e) => {
              if (e.target.value === typeModel) {
                setTyping(true);
                return;
              }
              setTyping(false);
              set({ model: e.target.value });
            }}
            slotProps={{ select: { displayEmpty: true } }}
          >
            {models.map((m) => (
              <MenuItem key={m} value={m}>
                {m === "" ? "Default" : m}
              </MenuItem>
            ))}
            <MenuItem value={typeModel}>Type a model id</MenuItem>
          </TextField>
          {typing && (
            <TextField
              label="Model id"
              size="small"
              value={form.model}
              placeholder="claude-opus-5-5"
              onChange={(e) => set({ model: e.target.value })}
              slotProps={{ htmlInput: { className: styles.mono } }}
            />
          )}
        </div>
        <div className={styles.fields}>
          <TextField
            label="Pass verdicts"
            size="small"
            value={form.pass}
            helperText="Comma-separated, such as PASS"
            onChange={(e) => set({ pass: e.target.value })}
            onBlur={() => set({ pass: form.pass.toUpperCase() })}
            className={styles.description}
          />
          <TextField
            label="Fail verdicts"
            size="small"
            value={form.fail}
            helperText="Comma-separated, such as FAIL"
            onChange={(e) => set({ fail: e.target.value })}
            onBlur={() => set({ fail: form.fail.toUpperCase() })}
            className={styles.description}
          />
        </div>
        <TextField
          label="Vars"
          size="small"
          value={form.vars}
          helperText="Comma-separated; the names the prompt may use"
          onChange={(e) => set({ vars: e.target.value })}
        />
        <TextField
          label="Claude arguments"
          size="small"
          multiline
          minRows={2}
          value={form.claudeArgs}
          helperText="One per line, such as --effort high; not --model or --settings"
          onChange={(e) => set({ claudeArgs: e.target.value })}
          slotProps={{ htmlInput: { className: styles.mono } }}
        />
        <TextField
          label="Prompt template"
          fullWidth
          multiline
          minRows={12}
          value={form.prompt}
          error={shown.prompt !== undefined}
          helperText={shown.prompt ?? "Markdown with Go template placeholders, such as {{.Vars.TASK}}"}
          onChange={(e) => set({ prompt: e.target.value })}
          slotProps={{ htmlInput: { className: styles.mono } }}
        />
      </DialogContent>
      <DialogActions>
        <span className={styles.applies}>Applies to agents started after you save</span>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="contained" disabled={saving} onClick={() => void save()}>
          {saving ? "Saving…" : "Save"}
        </Button>
      </DialogActions>
    </>
  );
};

/** The props of RoleEditor. */
export interface RoleEditorProps {
  /** The role to edit, or null for a new one. */
  readonly role: ListedRole | null;
  readonly onClose: () => void;
}

/**
 * Adds an agent role, or edits its description, model, verdicts, vars,
 * arguments, and prompt (ADR 0050). Saving runs `ws machine roles add` or
 * `edit`; an error keeps the dialog open with ws's message.
 */
export const RoleEditor = ({ role, onClose }: RoleEditorProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const fullScreen = useMediaQuery("(max-width: 720px)");
  const [loaded, setLoaded] = useState<RoleDetail | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const name = role?.name ?? null;
  useEffect(() => {
    if (name === null) {
      return;
    }
    let live = true;
    void dispatch(fetchRoleDetail(name)).then((result) => {
      if (!live) {
        return;
      }
      if (fetchRoleDetail.fulfilled.match(result)) {
        setLoaded(result.payload);
      } else {
        setLoadError(result.payload?.message ?? result.error.message ?? "reading the role failed");
      }
    });
    return () => {
      live = false;
    };
  }, [dispatch, name]);
  const body = (): JSX.Element => {
    if (role === null || loaded !== null) {
      return <EditorForm loaded={loaded} onClose={onClose} />;
    }
    return (
      <DialogContent className={styles.loading}>
        {loadError === null ? <CircularProgress size={20} aria-label="Loading role" /> : <Alert severity="error">{loadError}</Alert>}
      </DialogContent>
    );
  };
  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="md" fullScreen={fullScreen} aria-labelledby="role-editor-title">
      <DialogTitle id="role-editor-title">{role === null ? "New agent role" : "Edit agent role"}</DialogTitle>
      {body()}
    </Dialog>
  );
};
