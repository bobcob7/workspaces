import { describe, expect, it } from "vitest";
import { loop } from "../../test/fixtures";
import { emptyValues, formErrors, isValid, loopLabel, requestOf, slugOf, valuesOf, type LoopFormValues } from "./formValues";

const valid: LoopFormValues = { ...emptyValues, title: "Deps", prompt: "Update the dependencies." };

describe("valuesOf", () => {
  it("reads an existing loop into form values with vars in key order", () => {
    const values = valuesOf(loop({ every: "48h", keep: 3, script: "true", scriptTimeout: "45s", vars: { B: "2", A: "1" } }));
    expect(values.period).toEqual({ amount: "2", unit: "d" });
    expect(values.keep).toBe("3");
    expect(values.scriptTimeout).toEqual({ amount: "45", unit: "s" });
    expect(values.vars).toEqual([
      { key: "A", value: "1" },
      { key: "B", value: "2" },
    ]);
  });

  it("reads the title, or the name when the loop has no title", () => {
    expect(valuesOf(loop({ name: "deps-bump", title: "Dependency bumps" }))).toMatchObject({
      title: "Dependency bumps",
      name: "deps-bump",
    });
    expect(valuesOf(loop({ name: "triage", title: "" }))).toMatchObject({ title: "triage", name: "triage" });
  });
});

describe("loopLabel", () => {
  it("returns the title, or the name when the title is blank", () => {
    expect(loopLabel({ name: "deps-bump", title: "Dependency bumps" })).toBe("Dependency bumps");
    expect(loopLabel({ name: "triage", title: "" })).toBe("triage");
    expect(loopLabel({ name: "triage", title: "  " })).toBe("triage");
  });
});

describe("slugOf", () => {
  it.each([
    ["Nightly Deps Bump!", "nightly-deps-bump"],
    ["  Café  Run ", "cafe-run"],
    ["!!!", ""],
    ["a".repeat(50), "a".repeat(40)],
    [`${"b".repeat(39)} c`, "b".repeat(39)],
  ])("turns %j into %j", (title, slug) => {
    expect(slugOf(title)).toBe(slug);
  });
});

describe("formErrors", () => {
  it("has no errors for valid values", () => {
    expect(formErrors(valid)).toEqual({});
    expect(isValid(valid)).toBe(true);
  });

  it.each([
    ["blank title", { title: "  " }, { title: "Required" }],
    ["title over 80 characters", { title: "x".repeat(81) }, { title: "1 to 80 characters" }],
    ["title with a line break", { title: "Nightly\ndeps" }, { title: "No line breaks or tabs" }],
    ["title with no letters or digits", { title: "!!!" }, { title: "Use at least one letter or digit" }],
    ["period under 5 minutes", { period: { amount: "4", unit: "m" } }, { every: "A whole number, 5 minutes to 7 days" }],
    ["keep above 50", { keep: "51" }, { keep: "1 to 50" }],
    ["blank prompt", { prompt: "   " }, { prompt: "Required" }],
    ["script with a bad timeout", { script: "true", scriptTimeout: { amount: "11", unit: "m" } }, { scriptTimeout: "A whole number, 1 second to 10 minutes" }],
  ] as const)("reports %s", (_label, change, expected) => {
    expect(formErrors({ ...valid, ...change })).toEqual(expected);
  });

  it("accepts a title of exactly 80 characters", () => {
    expect(formErrors({ ...valid, title: "x".repeat(80) })).toEqual({});
  });

  it("allows a title with no letters or digits on an existing loop, whose ID is kept", () => {
    expect(formErrors({ ...valid, name: "deps", title: "!!!" })).toEqual({});
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
    expect(requestOf({ ...valid, keep: "7", model: "haiku", permissionMode: "acceptEdits", specs: ["api"] })).toEqual({
      name: "deps",
      title: "Deps",
      every: "1h",
      prompt: "Update the dependencies.",
      specs: ["api"],
      keep: 7,
      model: "haiku",
      permissionMode: "acceptEdits",
    });
  });

  it("derives the ID from the title on create and trims the title", () => {
    expect(requestOf({ ...valid, title: "  Nightly Deps Bump!  " })).toMatchObject({
      name: "nightly-deps-bump",
      title: "Nightly Deps Bump!",
    });
  });

  it("keeps the loop's ID on edit and sends the new title", () => {
    expect(requestOf({ ...valid, name: "deps-bump", title: "Renamed" })).toMatchObject({
      name: "deps-bump",
      title: "Renamed",
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
