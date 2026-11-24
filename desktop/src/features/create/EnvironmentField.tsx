import { MenuItem, TextField } from "@mui/material";
import { useMemo, useState, type JSX } from "react";
import type { RemoteEnvironment } from "../../api";
import { useAppSelector } from "../../store";
import { clock, selectEnvironmentChoices, type EnvironmentChoice } from "../environments/environments";

/** The environment a dialog will use and what to tell the user about it. */
export interface EnvironmentPick {
  /** `local`, or the name of an enrolled, online environment. */
  readonly environment: string;
  /** Set when the remembered choice couldn't be used. */
  readonly note: string | null;
  /** Shown when the environment has no `claude`; New agent can't start there. */
  readonly claudeMissing: boolean;
  /** Whether the picker is shown: some environment is enrolled. */
  readonly visible: boolean;
  readonly choices: readonly EnvironmentChoice[];
  readonly items: readonly RemoteEnvironment[];
  readonly choose: (name: string) => void;
}

const sameChoices = (a: readonly EnvironmentChoice[], b: readonly EnvironmentChoice[]): boolean =>
  JSON.stringify(a) === JSON.stringify(b);

/** The picked environment: the user's pick, else the last choice when it is enabled, else local. */
export const useEnvironmentPick = (): EnvironmentPick => {
  const choices = useAppSelector((state) => selectEnvironmentChoices(state), sameChoices);
  const items = useAppSelector((state) => state.environments.items);
  const lastChoice = useAppSelector((state) => state.environments.lastChoice);
  const [picked, setPicked] = useState<string | null>(null);
  return useMemo((): EnvironmentPick => {
    const enabled = (name: string): boolean => choices.some((c) => c.name === name && !c.disabled);
    const wanted = picked ?? lastChoice;
    const wantedOffline = choices.find((c) => c.name === wanted)?.disabled === true;
    const environment = picked !== null && enabled(picked) ? picked : enabled(lastChoice) ? lastChoice : "local";
    const hello = items.find((env) => env.name === environment)?.hello ?? null;
    return {
      environment,
      note: wantedOffline ? `${wanted} is offline; using ${environment}` : null,
      claudeMissing: environment !== "local" && hello?.claude === "missing",
      visible: choices.length > 1,
      choices,
      items,
      choose: setPicked,
    };
  }, [choices, items, lastChoice, picked]);
};

const optionLabel = (choice: EnvironmentChoice, items: readonly RemoteEnvironment[]): string => {
  if (!choice.disabled) {
    return choice.name;
  }
  const env = items.find((item) => item.name === choice.name);
  if (env?.status === "unknown") {
    return `${choice.name} (the ws daemon isn't running)`;
  }
  if (env?.lastSeen != null) {
    return `${choice.name} (offline since ${clock(env.lastSeen, Date.now())})`;
  }
  return `${choice.name} (offline)`;
};

/** A select of where to run: this machine or an enrolled environment. Hidden when none is enrolled. */
export const EnvironmentField = ({ pick }: { readonly pick: EnvironmentPick }): JSX.Element | null => {
  if (!pick.visible) {
    return null;
  }
  return (
    <TextField
      select
      label="Environment"
      value={pick.environment}
      onChange={(e) => pick.choose(e.target.value)}
      helperText={pick.note ?? undefined}
    >
      {pick.choices.map((choice) => (
        <MenuItem key={choice.name} value={choice.name} disabled={choice.disabled}>
          {optionLabel(choice, pick.items)}
        </MenuItem>
      ))}
    </TextField>
  );
};
