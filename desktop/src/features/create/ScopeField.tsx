import { Alert, Checkbox, FormControl, FormControlLabel, FormGroup, FormHelperText, FormLabel } from "@mui/material";
import { useEffect, useMemo, useState, type JSX } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { scopeChoices, splitDefaultScope, defaultScopeOf, type ScopeChoice } from "../environments/environments";
import { fetchConfig } from "../settings/settingsSlice";

/** The environments a new agent may start children on, as the New agent dialog shows them. */
export interface ScopePick {
  /** Same as the Environment picker: some environment is enrolled. */
  readonly visible: boolean;
  /** Own environment first, then local, then by name. */
  readonly choices: readonly ScopeChoice[];
  /** Own environment first, then by name. */
  readonly selected: readonly string[];
  /** default_scope names that aren't enrolled. */
  readonly leftOut: readonly string[];
  /** Set when default_scope couldn't be read. */
  readonly warning: string | null;
  /** default_scope is still loading and the own environment is shown: New agent waits for it. */
  readonly pending: boolean;
  /** More than 64 selected: New agent can't start. */
  readonly tooMany: boolean;
  readonly toggle: (name: string) => void;
}

interface ScopeLoad {
  readonly status: "loading" | "loaded" | "failed";
  readonly defaults: readonly string[];
  /** Each name the person checked or unchecked. It wins over default_scope, whenever that lands. */
  readonly toggles: Readonly<Record<string, boolean>>;
  readonly warning: string | null;
}

const unloaded: ScopeLoad = { status: "loading", defaults: [], toggles: {}, warning: null };

const maxSelected = 64;

/** default_scope's enrolled names besides the target, capped so the target still fits in maxSelected. */
const preselectedNames = (defaults: readonly string[], target: string, known: readonly string[]): readonly string[] =>
  defaults.filter((name) => name !== target && known.includes(name)).slice(0, maxSelected - 1);

/** The names checked: default_scope's, with each toggle applied over them. */
const checkedNames = (load: ScopeLoad, target: string, known: readonly string[]): readonly string[] => {
  const preselected = preselectedNames(load.defaults, target, known);
  const named = [...new Set([...preselected, ...Object.keys(load.toggles)])];
  return named.filter((name) => load.toggles[name] ?? preselected.includes(name));
};

/** The target first, then the checked names it can still use, by name. */
const selectedNames = (target: string, checked: readonly string[], known: readonly string[]): readonly string[] => {
  const others = [...new Set(checked)].filter((name) => name !== target && known.includes(name)).sort((a, b) => a.localeCompare(b));
  return [target, ...others];
};

/**
 * The scope a new agent gets: its own environment plus default_scope, read once per opening.
 * Never remembered between openings and never read from the last choice or prefilled specs.
 */
export const useScopePick = (open: boolean, target: string): ScopePick => {
  const dispatch = useAppDispatch();
  const items = useAppSelector((state) => state.environments.items);
  const [load, setLoad] = useState<ScopeLoad>(unloaded);
  useEffect(() => {
    setLoad(unloaded);
    if (!open) {
      return;
    }
    let current = true;
    void dispatch(fetchConfig()).then((result) => {
      if (!current) {
        return;
      }
      if (fetchConfig.fulfilled.match(result)) {
        const defaults = defaultScopeOf(result.payload);
        setLoad((latest) => ({ ...latest, status: "loaded", defaults }));
        return;
      }
      const reason = result.payload?.message ?? result.error.message ?? "unknown error";
      setLoad((latest) => ({
        ...latest,
        status: "failed",
        defaults: [],
        warning: `couldn't read default_scope: ${reason}; the agent will start with its own environment only.`,
      }));
    });
    return () => {
      current = false;
    };
  }, [dispatch, open]);
  return useMemo((): ScopePick => {
    const choices = scopeChoices(items, target);
    const known = choices.map((choice) => choice.name);
    const selected = selectedNames(target, checkedNames(load, target, known), known);
    return {
      visible: items.some((env) => env.state === "enrolled"),
      choices,
      selected,
      leftOut: splitDefaultScope(load.defaults, items).leftOut,
      warning: load.warning,
      pending: load.status === "loading",
      tooMany: selected.length > maxSelected,
      toggle: (name: string): void => {
        if (name === target) {
          return;
        }
        const checked = selected.includes(name);
        setLoad((latest) => ({ ...latest, toggles: { ...latest.toggles, [name]: !checked } }));
      },
    };
  }, [items, target, load]);
};

const choiceLabel = (choice: ScopeChoice): string => {
  if (choice.own) {
    return `${choice.name} · its own environment`;
  }
  if (choice.status === "offline") {
    return `${choice.name} · offline`;
  }
  if (choice.status === "unknown") {
    return `${choice.name} · status unknown`;
  }
  return choice.name;
};

const leftOutMessage = (names: readonly string[]): string =>
  names.length === 1
    ? `default_scope lists ${names[0]}, which isn't enrolled; it's left out.`
    : `default_scope lists ${names.join(", ")}, which aren't enrolled; they're left out.`;

/** The "May start children on" checkboxes, right after the Environment picker. Hidden when none is enrolled. */
export const ScopeField = ({ pick }: { readonly pick: ScopePick }): JSX.Element | null => {
  if (!pick.visible) {
    return null;
  }
  const offline = pick.choices.filter((choice) => !choice.own && choice.status !== "online" && pick.selected.includes(choice.name));
  return (
    <FormControl component="fieldset" variant="standard">
      <FormLabel component="legend">May start children on</FormLabel>
      <FormGroup>
        {pick.choices.map((choice) => (
          <FormControlLabel
            key={choice.name}
            label={choiceLabel(choice)}
            control={
              <Checkbox
                checked={pick.selected.includes(choice.name)}
                disabled={choice.own}
                onChange={() => pick.toggle(choice.name)}
              />
            }
          />
        ))}
      </FormGroup>
      <FormHelperText>
        Only you grant this: the agent and its children can narrow it, never widen it. Starts from default_scope in config.toml.
      </FormHelperText>
      {offline.map((choice) => (
        <FormHelperText key={choice.name}>
          {`${choice.name} is offline: a child can start there once it's back online.`}
        </FormHelperText>
      ))}
      {pick.tooMany && <FormHelperText error>At most 64 environments.</FormHelperText>}
      {pick.leftOut.length > 0 && <Alert severity="warning">{leftOutMessage(pick.leftOut)}</Alert>}
      {pick.pending && <FormHelperText>Loading default_scope…</FormHelperText>}
      {pick.warning !== null && <Alert severity="warning">{pick.warning}</Alert>}
    </FormControl>
  );
};
