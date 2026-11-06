import { describe, expect, it } from "vitest";
import { everyOf, formatGoDuration, parseGoDuration, periodOf, scriptTimeoutOf, timeoutOf } from "./period";

describe("parseGoDuration", () => {
  it.each([
    ["15m", 900],
    ["1h30m", 5400],
    ["24h", 86400],
    ["90s", 90],
    ["1m30s", 90],
  ])("parses %s to %i seconds", (text, seconds) => {
    expect(parseGoDuration(text)).toBe(seconds);
  });

  it.each(["", "1d", "abc", "1.5h"])("returns null for %j", (text) => {
    expect(parseGoDuration(text)).toBeNull();
  });
});

describe("formatGoDuration", () => {
  it.each([
    [900, "15m"],
    [5400, "1h30m"],
    [86400, "24h"],
    [90, "1m30s"],
    [45, "45s"],
  ])("formats %i seconds as %s", (seconds, text) => {
    expect(formatGoDuration(seconds)).toBe(text);
  });
});

describe("periodOf", () => {
  it.each([
    ["48h", { amount: "2", unit: "d" }],
    ["90m", { amount: "90", unit: "m" }],
    ["6h", { amount: "6", unit: "h" }],
    ["15m", { amount: "15", unit: "m" }],
    ["x", { amount: "1", unit: "h" }],
  ])("gives the largest unit that divides %s", (every, period) => {
    expect(periodOf(every)).toEqual(period);
  });
});

describe("everyOf", () => {
  it.each([
    [{ amount: "15", unit: "m" }, "15m"],
    [{ amount: "90", unit: "m" }, "1h30m"],
    [{ amount: "2", unit: "d" }, "48h"],
    [{ amount: "7", unit: "d" }, "168h"],
  ] as const)("gives the Go duration for %j", (period, every) => {
    expect(everyOf(period)).toBe(every);
  });

  it.each([
    { amount: "4", unit: "m" },
    { amount: "8", unit: "d" },
    { amount: "0", unit: "h" },
    { amount: "1.5", unit: "h" },
    { amount: "", unit: "h" },
  ] as const)("returns null for %j", (period) => {
    expect(everyOf(period)).toBeNull();
  });
});

describe("timeoutOf", () => {
  it.each([
    ["2m", { amount: "2", unit: "m" }],
    ["45s", { amount: "45", unit: "s" }],
    ["1m30s", { amount: "90", unit: "s" }],
    [null, { amount: "1", unit: "m" }],
  ] as const)("gives the timeout for %j", (timeout, expected) => {
    expect(timeoutOf(timeout)).toEqual(expected);
  });
});

describe("scriptTimeoutOf", () => {
  it.each([
    [{ amount: "2", unit: "m" }, "2m"],
    [{ amount: "45", unit: "s" }, "45s"],
    [{ amount: "90", unit: "s" }, "1m30s"],
  ] as const)("gives the Go duration for %j", (timeout, text) => {
    expect(scriptTimeoutOf(timeout)).toBe(text);
  });

  it.each([
    { amount: "0", unit: "s" },
    { amount: "11", unit: "m" },
  ] as const)("returns null for %j", (timeout) => {
    expect(scriptTimeoutOf(timeout)).toBeNull();
  });
});
