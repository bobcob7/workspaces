import { Alert, Button, Chip, CircularProgress, MenuItem, TextField } from "@mui/material";
import { useState, type JSX } from "react";
import type { ListedProfile, ProfileSetting, ProfileSource, Runs } from "../../api";
import { models } from "../create/NewAgentDialog";
import { ProfileEditor, runsLabels } from "./ProfileEditor";
import { RemoveProfileDialog } from "./RemoveProfileDialog";
import styles from "./ReviewerProfiles.module.css";

/** The badge after a profile's name: none for a built-in (ADR 0042). */
const badges: Readonly<Record<ProfileSource, { readonly label: string; readonly className: string } | null>> = {
  builtin: null,
  user: { label: "custom", className: styles.custom ?? "" },
  override: { label: "edited", className: styles.edited ?? "" },
};

type Editing = { readonly kind: "new" } | { readonly kind: "edit"; readonly profile: ListedProfile };

/** The choice in the Model menu that switches to typing a model id. */
const typeModel = "\u0000type";

/** A profile's row: its changed setting, or as ws lists it. */
export const rowOf = (profile: ListedProfile, changed: Readonly<Record<string, ProfileSetting>>): ProfileSetting =>
  changed[profile.name] ?? { runs: profile.when, model: profile.model };

/**
 * The changed rows with `row` set for `profile`. A row set back to what ws
 * lists is dropped, so only real changes are kept.
 */
export const withRow = (
  changed: Readonly<Record<string, ProfileSetting>>,
  profile: ListedProfile,
  row: ProfileSetting,
): Readonly<Record<string, ProfileSetting>> => {
  const { [profile.name]: _old, ...rest } = changed;
  return row.runs === profile.when && row.model === profile.model ? rest : { ...rest, [profile.name]: row };
};

interface ModelFieldProps {
  readonly profile: string;
  readonly value: string;
  readonly disabled: boolean;
  readonly onChange: (model: string) => void;
}

const ModelField = ({ profile, value, disabled, onChange }: ModelFieldProps): JSX.Element => {
  const [typing, setTyping] = useState(false);
  const [draft, setDraft] = useState("");
  if (typing) {
    const done = (): void => {
      setTyping(false);
      if (draft.trim() !== "") {
        onChange(draft.trim());
      }
    };
    return (
      <TextField
        size="small"
        autoFocus
        placeholder="claude-opus-5-5"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={done}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            done();
          } else if (e.key === "Escape") {
            setTyping(false);
          }
        }}
        slotProps={{ htmlInput: { "aria-label": `Model id for ${profile}`, className: styles.modelId } }}
      />
    );
  }
  const options: readonly string[] = models.includes(value as (typeof models)[number]) ? models : [...models, value];
  return (
    <TextField
      select
      size="small"
      value={value}
      disabled={disabled}
      onChange={(e) => {
        if (e.target.value === typeModel) {
          setDraft(value);
          setTyping(true);
          return;
        }
        onChange(e.target.value);
      }}
      slotProps={{ select: { displayEmpty: true }, htmlInput: { "aria-label": `Model for ${profile}` } }}
    >
      {options.map((m) => (
        <MenuItem key={m} value={m}>
          {m === "" ? "Default" : m}
        </MenuItem>
      ))}
      <MenuItem value={typeModel}>Model id…</MenuItem>
    </TextField>
  );
};

interface ReviewerProfilesProps {
  readonly profiles: readonly ListedProfile[] | null;
  readonly error: string | null;
  readonly changed: Readonly<Record<string, ProfileSetting>>;
  readonly onChange: (changed: Readonly<Record<string, ProfileSetting>>) => void;
}

/**
 * The Reviewer profiles table: every profile `ws review profiles` lists,
 * with when it runs and on which model. Changes are passed to every review
 * as --profile and --auto (ADR 0038). Profiles are added, edited, and
 * removed from here too (ADR 0042).
 */
export const ReviewerProfiles = ({ profiles, error, changed, onChange }: ReviewerProfilesProps): JSX.Element => {
  const [editing, setEditing] = useState<Editing | null>(null);
  const [removing, setRemoving] = useState<ListedProfile | null>(null);
  if (profiles === null) {
    return error === null ? (
      <CircularProgress size={20} aria-label="Loading profiles" />
    ) : (
      <Alert severity="error">{error}</Alert>
    );
  }
  return (
    <div className={styles.root}>
      <table className={styles.table}>
        <thead>
          <tr>
            <th>Profile</th>
            <th>Runs</th>
            <th>Model</th>
            <th>Files</th>
            <th aria-label="Actions" />
          </tr>
        </thead>
        <tbody>
          {profiles.map((profile) => {
            const row = rowOf(profile, changed);
            const off = row.runs === "off";
            const save = (next: ProfileSetting): void => onChange(withRow(changed, profile, next));
            const badge = badges[profile.source];
            return (
              <tr key={profile.name} className={off ? styles.off : undefined}>
                <td className={styles.name} title={profile.description}>
                  {profile.name}
                  {badge !== null && <Chip size="small" label={badge.label} className={`${styles.badge} ${badge.className}`} />}
                </td>
                <td>
                  <TextField
                    select
                    size="small"
                    value={row.runs}
                    onChange={(e) => save({ ...row, runs: e.target.value as Runs })}
                    slotProps={{ htmlInput: { "aria-label": `Runs for ${profile.name}` } }}
                  >
                    {(["always", "auto", "off"] as const).map((runs) => (
                      <MenuItem key={runs} value={runs} disabled={runs === "auto" && profile.patterns.length === 0}>
                        {runsLabels[runs]}
                      </MenuItem>
                    ))}
                  </TextField>
                </td>
                <td>
                  <ModelField
                    profile={profile.name}
                    value={row.model}
                    disabled={off}
                    onChange={(model) => save({ ...row, model })}
                  />
                </td>
                <td className={styles.files}>{profile.patterns.join(", ")}</td>
                <td className={styles.actions}>
                  <Button size="small" aria-label={`Edit ${profile.name}`} onClick={() => setEditing({ kind: "edit", profile })}>
                    Edit
                  </Button>
                  {profile.source === "user" && (
                    <Button size="small" color="error" aria-label={`Delete ${profile.name}`} onClick={() => setRemoving(profile)}>
                      Delete
                    </Button>
                  )}
                  {profile.source === "override" && (
                    <Button size="small" aria-label={`Reset ${profile.name} to built-in`} onClick={() => setRemoving(profile)}>
                      Reset to built-in
                    </Button>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <div className={styles.buttons}>
        <Button size="small" variant="outlined" onClick={() => setEditing({ kind: "new" })}>
          New profile
        </Button>
        <Button size="small" disabled={Object.keys(changed).length === 0} onClick={() => onChange({})}>
          Reset to defaults
        </Button>
      </div>
      {editing !== null && (
        <ProfileEditor profile={editing.kind === "edit" ? editing.profile : null} onClose={() => setEditing(null)} />
      )}
      {removing !== null && <RemoveProfileDialog profile={removing} onClose={() => setRemoving(null)} />}
    </div>
  );
};
