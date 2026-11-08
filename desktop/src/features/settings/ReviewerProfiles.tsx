import { Alert, Button, Chip, CircularProgress, MenuItem, TextField } from "@mui/material";
import { useState, type JSX } from "react";
import type { ConfigListing, ListedProfile, ProfileSource, Runs } from "../../api";
import { models } from "../create/NewAgentDialog";
import { ProfileEditor, runsLabels } from "./ProfileEditor";
import { RemoveProfileDialog } from "./RemoveProfileDialog";
import { settingOf } from "./settingsSlice";
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

/** The config.toml keys behind a profile's Runs and Model. */
export const profileKeys = (name: string): { readonly when: string; readonly model: string } => ({
  when: `review.profiles.${name}.when`,
  model: `review.profiles.${name}.model`,
});

/** Every review.profiles.* key that config.toml sets. */
export const setProfileKeys = (config: ConfigListing | null): readonly string[] =>
  config?.settings.filter((setting) => setting.key.startsWith("review.profiles.") && setting.set).map((setting) => setting.key) ?? [];

const isSet = (config: ConfigListing | null, key: string): boolean => settingOf(config, key)?.set === true;

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

export interface ReviewerProfilesProps {
  readonly profiles: readonly ListedProfile[] | null;
  readonly error: string | null;
  /** config.toml's review.profiles.* settings, to know which rows are set. */
  readonly config: ConfigListing | null;
  /** Called with the row's new Runs or Model. */
  readonly onRowChange: (profile: ListedProfile, change: { readonly runs?: Runs; readonly model?: string }) => void;
  /** Called by a row's Reset: unset both keys. */
  readonly onRowReset: (profile: ListedProfile) => void;
  /** Called by Reset to defaults: unset every set review.profiles.* key. */
  readonly onResetAll: () => void;
}

/**
 * The Reviewer profiles table: every profile `ws review profiles` lists,
 * with when it runs and on which model. Changes are passed to every review
 * as --profile and --auto (ADR 0038). Profiles are added, edited, and
 * removed from here too (ADR 0042).
 */
export const ReviewerProfiles = ({
  profiles,
  error,
  config,
  onRowChange,
  onRowReset,
  onResetAll,
}: ReviewerProfilesProps): JSX.Element => {
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
            const off = profile.when === "off";
            const keys = profileKeys(profile.name);
            const resettable = isSet(config, keys.when) || isSet(config, keys.model);
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
                    value={profile.when}
                    onChange={(e) => onRowChange(profile, { runs: e.target.value as Runs })}
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
                    value={profile.model}
                    disabled={off}
                    onChange={(model) => onRowChange(profile, { model })}
                  />
                </td>
                <td className={styles.files}>{profile.patterns.join(", ")}</td>
                <td className={styles.actions}>
                  <Button size="small" aria-label={`Edit ${profile.name}`} onClick={() => setEditing({ kind: "edit", profile })}>
                    Edit
                  </Button>
                  {resettable && (
                    <Button size="small" aria-label={`Reset ${profile.name}`} onClick={() => onRowReset(profile)}>
                      Reset
                    </Button>
                  )}
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
        <Button size="small" disabled={setProfileKeys(config).length === 0} onClick={onResetAll}>
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
