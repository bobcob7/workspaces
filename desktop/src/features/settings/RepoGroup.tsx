import { Alert, Button, CircularProgress, IconButton, TextField } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { AppError, ConfigListing, ConfigSetting, ConfigValue, Resolution, ResolveSource } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { ConfigSource } from "./ConfigSource";
import { Row } from "./Row";
import { fetchConfig, resolveSpec, searchSpecs, setConfig, settingOf, unsetConfig } from "./settingsSlice";
import settingsStyles from "./SettingsPane.module.css";
import styles from "./RepoGroup.module.css";

const matchedBy: Readonly<Record<ResolveSource, string>> = {
  spec: "The spec names its host.",
  cache: "Found by name in the cache.",
  default: "Not cached: taken to be on the default host.",
  probe: "Found upstream under a default owner.",
};

const isList = (value: ConfigValue | undefined): value is readonly string[] => Array.isArray(value);

const isStringList = (value: unknown): value is readonly string[] => Array.isArray(value) && value.every((item) => typeof item === "string");

const candidatesOf = (error: AppError): readonly string[] => {
  const found: unknown = error.details?.candidates;
  return isStringList(found) ? found : [];
};

/** The list with the item at index swapped with its neighbour. */
const swapped = (list: readonly string[], index: number, by: -1 | 1): readonly string[] => {
  const target = index + by;
  const item = list[index];
  const neighbour = list[target];
  if (item === undefined || neighbour === undefined) {
    return list;
  }
  return list.map((owner, i) => (i === index ? neighbour : i === target ? item : owner));
};

/** A text setting saved when it loses focus, so typing doesn't save every keystroke. */
const useDraft = (value: string): [string, (next: string) => void] => {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return [draft, setDraft];
};

interface HostRowProps {
  readonly setting: ConfigSetting | undefined;
  readonly path: string;
  readonly error: string | undefined;
}

const HostRow = ({ setting, path, error }: HostRowProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const saved = typeof setting?.value === "string" ? setting.value : "";
  const [draft, setDraft] = useDraft(saved);
  const save = (): void => {
    const next = draft.trim();
    setDraft(next);
    if (next === saved) {
      return;
    }
    void dispatch(setConfig({ key: "defaults.host", values: [next] })).then((result) => {
      if (setConfig.rejected.match(result)) {
        setDraft(saved);
      }
    });
  };
  return (
    <Row
      label="Default host"
      help={
        <>
          For specs without one, such as owner/name. <ConfigSource setting={setting} path={path} />
        </>
      }
      errors={[error]}
    >
      <div className={settingsStyles.inline}>
        <TextField
          size="small"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={save}
          slotProps={{ htmlInput: { "aria-label": "Default host" } }}
        />
        {setting?.set === true && (
          <Button size="small" aria-label="Reset default host" onClick={() => void dispatch(unsetConfig("defaults.host"))}>
            Reset
          </Button>
        )}
      </div>
    </Row>
  );
};

interface OwnersRowProps {
  readonly setting: ConfigSetting | undefined;
  readonly path: string;
  readonly error: string | undefined;
}

const OwnersRow = ({ setting, path, error }: OwnersRowProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const value = setting?.value;
  const owners = isList(value) ? value : [];
  const [newOwner, setNewOwner] = useState("");
  const save = (next: readonly string[]): void => {
    void dispatch(setConfig({ key: "defaults.owners", values: next }));
  };
  const add = (): void => {
    const owner = newOwner.trim().toLowerCase();
    if (owner === "" || owners.includes(owner)) {
      return;
    }
    save([...owners, owner]);
    setNewOwner("");
  };
  return (
    <Row
      label="Default owners"
      wide
      help={
        <>
          Tried in order for a bare repo name that isn't cached. <ConfigSource setting={setting} path={path} />
        </>
      }
      errors={[error]}
    >
      <ul className={styles.owners}>
        {owners.map((owner, index) => (
          <li key={owner} className={styles.owner}>
            <span className={settingsStyles.mono}>{owner}</span>
            {index > 0 && (
              <IconButton size="small" aria-label={`Move ${owner} up`} onClick={() => save(swapped(owners, index, -1))}>
                ↑
              </IconButton>
            )}
            {index < owners.length - 1 && (
              <IconButton size="small" aria-label={`Move ${owner} down`} onClick={() => save(swapped(owners, index, 1))}>
                ↓
              </IconButton>
            )}
            <IconButton size="small" aria-label={`Remove ${owner}`} onClick={() => save(owners.filter((o) => o !== owner))}>
              ×
            </IconButton>
          </li>
        ))}
      </ul>
      <div className={styles.add}>
        <TextField
          size="small"
          value={newOwner}
          placeholder="owner"
          onChange={(e) => setNewOwner(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          slotProps={{ htmlInput: { "aria-label": "Add owner" } }}
        />
        <Button size="small" onClick={add}>
          Add
        </Button>
      </div>
    </Row>
  );
};

const ResolutionView = ({ resolution }: { readonly resolution: Resolution }): JSX.Element => (
  <dl className={styles.facts} role="status" aria-label="Resolution">
    <dt>Repo</dt>
    <dd className={settingsStyles.mono}>{resolution.id}</dd>
    <dt>Ref</dt>
    <dd>{resolution.ref ?? "default branch"}</dd>
    <dt>Upstream</dt>
    <dd className={settingsStyles.mono}>{resolution.upstream}</dd>
    <dt>Cached</dt>
    <dd>{resolution.cached ? "Yes" : "No"}</dd>
    <dt>Matched by</dt>
    <dd>{matchedBy[resolution.source]}</dd>
  </dl>
);

const SpecRow = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const resolve = useAppSelector((s) => s.settings.resolve);
  const matches = useAppSelector((s) => s.settings.searchMatches);
  const [text, setText] = useState("");
  const query = text.trim();
  useEffect(() => {
    if (query.length < 2) {
      return;
    }
    const timer = setTimeout(() => void dispatch(searchSpecs(query)), 250);
    return () => clearTimeout(timer);
  }, [dispatch, query]);
  const resolveText = (spec: string): void => {
    setText(spec);
    void dispatch(resolveSpec(spec));
  };
  const submit = (): void => {
    if (query !== "") {
      resolveText(query);
    }
  };
  const current = resolve.spec === query ? resolve : null;
  const error = current?.error ?? null;
  return (
    <Row
      label="Try a spec"
      wide
      help="Shows what a spec resolves to with the settings above, without cloning. Uses ws cache resolve and ws cache search."
      errors={[]}
    >
      <div className={settingsStyles.inline}>
        <TextField
          size="small"
          className={settingsStyles.mono}
          value={text}
          placeholder="owner/name, name@branch or a URL"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              submit();
            }
          }}
          slotProps={{ htmlInput: { "aria-label": "Repo spec" } }}
        />
        <Button size="small" disabled={query === ""} onClick={submit}>
          Resolve
        </Button>
      </div>
      {query.length >= 2 && matches.length > 0 && (
        <ul className={styles.matches} aria-label="Cached matches">
          {matches.map((match) => {
            const label = match.ref === null ? match.id : `${match.id}@${match.ref}`;
            return (
              <li key={label}>
                <Button size="small" onClick={() => resolveText(label)}>
                  {label}
                </Button>
              </li>
            );
          })}
        </ul>
      )}
      {current?.resolution && <ResolutionView resolution={current.resolution} />}
      {error?.code === "repo_ambiguous" && (
        <>
          <span>Matches more than one cached repo:</span>
          <ul className={styles.candidates}>
            {candidatesOf(error).map((candidate) => (
              <li key={candidate}>
                <Button size="small" onClick={() => resolveText(candidate)}>
                  {candidate}
                </Button>
              </li>
            ))}
          </ul>
        </>
      )}
      {error !== null && error.code !== "repo_ambiguous" && <Alert severity="warning">{error.message}</Alert>}
    </Row>
  );
};

/** The Repos group: the default host and owners in config.toml, and a box that resolves a spec. */
export const RepoGroup = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const config: ConfigListing | null = useAppSelector((s) => s.settings.config);
  const configError = useAppSelector((s) => s.settings.configError);
  const configErrors = useAppSelector((s) => s.settings.configErrors);
  useEffect(() => {
    if (config === null) {
      void dispatch(fetchConfig());
    }
  }, [dispatch, config]);
  return (
    <section className={settingsStyles.group} aria-label="Repos">
      <h1 className={settingsStyles.title}>Repos</h1>
      {configError !== null && <Alert severity="error">{configError.message}</Alert>}
      {configError === null && config === null && <CircularProgress size={20} aria-label="Loading repos settings" />}
      {configError === null && config !== null && (
        <>
          <HostRow setting={settingOf(config, "defaults.host")} path={config.path} error={configErrors["defaults.host"]} />
          <OwnersRow setting={settingOf(config, "defaults.owners")} path={config.path} error={configErrors["defaults.owners"]} />
          <SpecRow />
        </>
      )}
    </section>
  );
};
