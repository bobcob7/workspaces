import { Alert, Button, TextField } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { ConfigListing, ConfigSetting } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { Row } from "./Row";
import { fetchConfig, restartDaemon, setConfig, settingOf, unsetConfig } from "./settingsSlice";
import styles from "./EnvironmentsGroup.module.css";

interface KeySpec {
  readonly key: string;
  readonly label: string;
  readonly help: string;
  /** What ws uses when config.toml doesn't list the key (it lists them only when [environments] exists). */
  readonly fallback: string;
  readonly numeric?: boolean;
}

const keys: readonly KeySpec[] = [
  { key: "environments.public_name", label: "Public name", help: "The host name machines reach this one by. No listener starts until it is set.", fallback: "" },
  { key: "environments.listen", label: "Listen", help: "Where connected machines dial in (MQTT over mTLS).", fallback: "0.0.0.0:8883" },
  { key: "environments.enroll_listen", label: "Enroll listen", help: "Where a new machine presents its one-time code.", fallback: "0.0.0.0:8884" },
  { key: "environments.renew_before_days", label: "Renew before (days)", help: "Certificates renew this many days before they expire.", fallback: "30", numeric: true },
];

/** A setting's value as text; a list is joined by commas. */
const textOf = (setting: ConfigSetting | undefined, fallback: string): string => {
  if (setting === undefined) {
    return fallback;
  }
  const value = setting.value;
  return Array.isArray(value) ? value.join(", ") : String(value);
};

const valuesOf = (setting: ConfigSetting | undefined, text: string): readonly string[] =>
  setting?.type === "strings"
    ? text
        .split(",")
        .map((part) => part.trim())
        .filter((part) => part !== "")
    : [text];

interface KeyRowProps {
  readonly spec: KeySpec;
  readonly config: ConfigListing;
  readonly error: string | undefined;
}

const KeyRow = ({ spec, config, error }: KeyRowProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const setting = settingOf(config, spec.key);
  const shown = textOf(setting, spec.fallback);
  const [draft, setDraft] = useState(shown);
  useEffect(() => setDraft(shown), [shown]);
  const save = (): void => {
    const text = draft.trim();
    if (text === shown) {
      return;
    }
    if (text === "") {
      void dispatch(unsetConfig(spec.key));
      return;
    }
    void dispatch(setConfig({ key: spec.key, values: valuesOf(setting, text) }));
  };
  return (
    <Row label={spec.label} help={spec.help} errors={[error]}>
      <TextField
        size="small"
        value={draft}
        placeholder={spec.fallback}
        slotProps={{ htmlInput: { "aria-label": spec.label, inputMode: spec.numeric === true ? "numeric" : undefined } }}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={save}
      />
    </Row>
  );
};

/** The `[environments]` keys of config.toml, each saved with `config_set` when it loses focus. */
export const EnvironmentSettingsCard = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const config = useAppSelector((s) => s.settings.config);
  const configError = useAppSelector((s) => s.settings.configError);
  const errors = useAppSelector((s) => s.settings.configErrors);
  const [restarting, setRestarting] = useState(false);
  const [restartError, setRestartError] = useState<string | null>(null);
  useEffect(() => {
    if (config === null) {
      void dispatch(fetchConfig());
    }
  }, [dispatch, config]);
  const restart = async (): Promise<void> => {
    setRestarting(true);
    const result = await dispatch(restartDaemon());
    setRestarting(false);
    setRestartError(restartDaemon.fulfilled.match(result) ? null : (result.payload?.message ?? result.error.message ?? "restarting the ws daemon failed"));
  };
  return (
    <div className={styles.card} role="group" aria-label="Environment settings">
      <h2 className={styles.subtitle}>Settings</h2>
      {configError !== null && <Alert severity="error">{configError.message}</Alert>}
      {config !== null && keys.map((spec) => <KeyRow key={spec.key} spec={spec} config={config} error={errors[spec.key]} />)}
      <p className={styles.muted}>Changes apply when the ws daemon reloads; the desktop restarts it.</p>
      {restartError !== null && <Alert severity="error">{restartError}</Alert>}
      <div className={styles.buttons}>
        <Button size="small" variant="outlined" disabled={restarting} onClick={() => void restart()}>
          Restart daemon
        </Button>
      </div>
    </div>
  );
};
