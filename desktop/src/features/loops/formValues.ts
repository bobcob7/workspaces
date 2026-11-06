import type { Loop, LoopVar, NewLoop } from "../../api";
import { everyOf, periodOf, scriptTimeoutOf, timeoutOf, type Amount, type PeriodUnit, type TimeoutUnit } from "./period";

/** What LoopForm edits. Text fields hold what was typed. */
export interface LoopFormValues {
  /** The display name typed in the Name field. */
  readonly title: string;
  /** An existing loop's ID; "" when adding, where the ID comes from the title. */
  readonly name: string;
  readonly period: Amount<PeriodUnit>;
  readonly keep: string;
  readonly prompt: string;
  readonly specs: readonly string[];
  readonly model: string;
  readonly permissionMode: string;
  readonly script: string;
  readonly scriptTimeout: Amount<TimeoutUnit>;
  readonly vars: readonly LoopVar[];
}

/** Errors to show under each field; a missing key is no error. */
export interface LoopFormErrors {
  readonly title?: string;
  readonly every?: string;
  readonly keep?: string;
  readonly prompt?: string;
  readonly scriptTimeout?: string;
  readonly vars?: string;
}

const maxTitleLength = 80;
const maxIdLength = 40;
const controlPattern = /\p{Cc}/u;
const keyPattern = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;
const wholeNumberPattern = /^\d+$/;
const minimumKeep = 1;
const maximumKeep = 50;

/** A new loop's values: blank title and prompt, every 1 h, keep "5", timeout 1 m, nothing else. */
export const emptyValues: LoopFormValues = {
  title: "",
  name: "",
  period: { amount: "1", unit: "h" },
  keep: "5",
  prompt: "",
  specs: [],
  model: "",
  permissionMode: "",
  script: "",
  scriptTimeout: { amount: "1", unit: "m" },
  vars: [],
};

/** The name to show for a loop: its title, or its ID when it has none. */
export const loopLabel = (loop: Pick<Loop, "name" | "title">): string =>
  loop.title.trim() === "" ? loop.name : loop.title;

/** The loop ID a title makes: lower-case a-z0-9 runs joined by "-", at most 40 characters. */
export const slugOf = (title: string): string =>
  title
    .toLowerCase()
    .normalize("NFKD")
    .replace(/\p{M}/gu, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, maxIdLength)
    .replace(/-+$/, "");

/** An existing loop's values, for the edit form. vars in key order. */
export const valuesOf = (loop: Loop): LoopFormValues => ({
  title: loopLabel(loop),
  name: loop.name,
  period: periodOf(loop.every),
  keep: String(loop.keep),
  prompt: loop.prompt,
  specs: [...loop.specs],
  model: loop.model ?? "",
  permissionMode: loop.permissionMode ?? "",
  script: loop.script ?? "",
  scriptTimeout: timeoutOf(loop.scriptTimeout),
  vars: Object.keys(loop.vars)
    .sort()
    .map((key) => ({ key, value: loop.vars[key] ?? "" })),
});

/** Whether the form adds a loop, whose ID comes from the title. */
const isNew = (values: LoopFormValues): boolean => values.name === "";

const titleError = (values: LoopFormValues): string | undefined => {
  const title = values.title.trim();
  if (title === "") {
    return "Required";
  }
  if ([...title].length > maxTitleLength) {
    return "1 to 80 characters";
  }
  if (controlPattern.test(title)) {
    return "No line breaks or tabs";
  }
  return isNew(values) && slugOf(title) === "" ? "Use at least one letter or digit" : undefined;
};

const everyError = (period: Amount<PeriodUnit>): string | undefined =>
  everyOf(period) === null ? "A whole number, 5 minutes to 7 days" : undefined;

const keepError = (keep: string): string | undefined => {
  const count = Number(keep.trim());
  const valid = wholeNumberPattern.test(keep.trim()) && count >= minimumKeep && count <= maximumKeep;
  return valid ? undefined : "1 to 50";
};

const promptError = (prompt: string): string | undefined => (prompt.trim() === "" ? "Required" : undefined);

const scriptTimeoutError = (values: LoopFormValues): string | undefined =>
  values.script.trim() !== "" && scriptTimeoutOf(values.scriptTimeout) === null
    ? "A whole number, 1 second to 10 minutes"
    : undefined;

/** The first problem with a variable row's key, or undefined when every key is fine. */
const varsError = (vars: readonly LoopVar[]): string | undefined => {
  const seen = new Set<string>();
  for (const row of vars) {
    const key = row.key.trim();
    if (key === "") {
      continue;
    }
    if (!keyPattern.test(key)) {
      return `Bad key ${key}: letters, digits and _, not starting with a digit`;
    }
    if (seen.has(key)) {
      return `${key} is given twice`;
    }
    seen.add(key);
  }
  return undefined;
};

/** What's wrong with values, as messages. */
export const formErrors = (values: LoopFormValues): LoopFormErrors => {
  const checks: Readonly<Record<keyof LoopFormErrors, string | undefined>> = {
    title: titleError(values),
    every: everyError(values.period),
    keep: keepError(values.keep),
    prompt: promptError(values.prompt),
    scriptTimeout: scriptTimeoutError(values),
    vars: varsError(values.vars),
  };
  return Object.fromEntries(Object.entries(checks).filter(([, message]) => message !== undefined)) as LoopFormErrors;
};

/** Whether formErrors is empty. */
export const isValid = (values: LoopFormValues): boolean => Object.keys(formErrors(values)).length === 0;

/** The request for values that are valid. */
export const requestOf = (values: LoopFormValues): NewLoop => {
  const hasScript = values.script.trim() !== "";
  const scriptTimeout = scriptTimeoutOf(values.scriptTimeout);
  const vars = values.vars
    .filter((row) => row.key.trim() !== "")
    .map((row) => ({ key: row.key.trim(), value: row.value }));
  return {
    name: isNew(values) ? slugOf(values.title) : values.name,
    title: values.title.trim(),
    every: everyOf(values.period) ?? "",
    prompt: values.prompt,
    specs: values.specs,
    keep: Number(values.keep.trim()),
    model: values.model,
    permissionMode: values.permissionMode,
    ...(hasScript && scriptTimeout !== null ? { script: values.script, scriptTimeout } : {}),
    ...(vars.length > 0 ? { vars } : {}),
  };
};
