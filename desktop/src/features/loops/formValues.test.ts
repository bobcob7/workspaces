import { describe, expect, it } from "vitest";
import { loop } from "../../test/fixtures";
import { formErrors, isValid, loopValues, newLoopValues, requestOf, type LoopFormValues } from "./formValues";

const valid: LoopFormValues = { ...newLoopValues, name: "deps", prompt: "Update the dependencies." };

describe("loopValues", () => {
  it("reads an existing loop into form values with vars in key order", () => {
    const values = loopValues(loop({ every: "48h", keep: 3, script: "true", scriptTimeout: "45s", vars: { B: "2", A: "1" } }));
    expect(values.period).toEqual({ amount: "2", unit: "d" });
    expect(values.keep).toBe("3");
    expect(values.scriptTimeout).toEqual({ amount: "45", unit: "s" });
    expect(values.vars).toEqual([
      { key: "A", value: "1" },
      { key: "B", value: "2" },
    ]);
  });
});

describe("formErrors", () => {
  it("has no errors for valid values", () => {
    expect(formErrors(valid)).toEqual({});
    expect(isValid(valid)).toBe(true);
  });

  it.each([
    ["blank name", { name: "  " }, { name: "Required" }],
    ["bad name", { name: "Deps" }, { name: "1-40 of a-z, 0-9 and -, starting with a letter or digit" }],
    ["period under 5 minutes", { period: { amount: "4", unit: "m" } }, { every: "A whole number, 5 minutes to 7 days" }],
    ["keep above 50", { keep: "51" }, { keep: "1 to 50" }],
    ["blank prompt", { prompt: "   " }, { prompt: "Required" }],
    ["script with a bad timeout", { script: "true", scriptTimeout: { amount: "11", unit: "m" } }, { scriptTimeout: "A whole number, 1 second to 10 minutes" }],
  ] as const)("reports %s", (_label, change, expected) => {
    expect(formErrors({ ...valid, ...change })).toEqual(expected);
  });

  it("ignores a bad timeout when there is no script", () => {
    expect(formErrors({ ...valid, scriptTimeout: { amount: "11", unit: "m" } })).toEqual({});
  });

  it("reports a var key that is not a valid name", () => {
    expect(formErrors({ ...valid, vars: [{ key: "1BAD", value: "x" }] })).toEqual({
      vars: "Bad key 1BAD: letters, digits and _, not starting with a digit",
    });
  });

  it("reports a var key given twice, after trimming", () => {
    expect(formErrors({ ...valid, vars: [{ key: "BRANCH", value: "a" }, { key: " BRANCH ", value: "b" }] })).toEqual({
      vars: "BRANCH is given twice",
    });
  });
});

describe("requestOf", () => {
  it("builds the request with keep, model and permissionMode", () => {
    expect(requestOf({ ...valid, name: " deps ", keep: "7", model: "haiku", permissionMode: "acceptEdits", specs: ["api"] })).toEqual({
      name: "deps",
      every: "1h",
      prompt: "Update the dependencies.",
      specs: ["api"],
      keep: 7,
      model: "haiku",
      permissionMode: "acceptEdits",
    });
  });

  it("omits script fields when the script is blank", () => {
    const request = requestOf({ ...valid, script: "  ", scriptTimeout: { amount: "2", unit: "m" } });
    expect(request).not.toHaveProperty("script");
    expect(request).not.toHaveProperty("scriptTimeout");
  });

  it("includes the script and its timeout when the script is set", () => {
    expect(requestOf({ ...valid, script: "make check", scriptTimeout: { amount: "45", unit: "s" } })).toMatchObject({
      script: "make check",
      scriptTimeout: "45s",
    });
  });

  it("drops rows with blank keys and trims the rest", () => {
    const request = requestOf({
      ...valid,
      vars: [
        { key: " BRANCH ", value: "main" },
        { key: "  ", value: "orphan" },
      ],
    });
    expect(request.vars).toEqual([{ key: "BRANCH", value: "main" }]);
  });

  it("omits vars when no row has a key", () => {
    expect(requestOf({ ...valid, vars: [{ key: "", value: "x" }] })).not.toHaveProperty("vars");
  });
});
