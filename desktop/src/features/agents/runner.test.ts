import { describe, expect, it } from "vitest";
import type { WsRunner } from "../../api";
import { entry } from "../../test/fixtures";
import { isRunnerRunning, modelTooltip, stepLabel, tokenCount, tokensLine } from "./runner";

const run: WsRunner = {
  modelAlias: "qwen9b",
  endpoint: "gpu",
  url: "http://gpu-a.inference.svc:1234",
  api: "openai",
  contextTokens: 65536,
  step: 12,
  maxSteps: 80,
  restarts: 0,
  usage: { requests: 12, promptTokens: 210344, completionTokens: 9120 },
  mcp: [],
  exit: null,
  endpointChanges: 0,
};

describe("tokenCount", () => {
  it("rounds large counts to whole thousands and keeps one decimal below ten thousand", () => {
    expect([tokenCount(210344), tokenCount(9120), tokenCount(1000), tokenCount(512)]).toEqual(["210k", "9.1k", "1.0k", "512"]);
  });
});

describe("tokensLine", () => {
  it("shows prompt tokens in and completion tokens out", () => {
    expect(tokensLine({ requests: 12, promptTokens: 210344, completionTokens: 9120 })).toBe("tokens 210k in · 9.1k out");
  });
});

describe("stepLabel", () => {
  it("shows the step of a runner with a step count", () => {
    expect(stepLabel(run)).toBe("step 12/80");
  });

  it("shows nothing before the runner reports a step", () => {
    expect(stepLabel({ ...run, step: null })).toBeNull();
    expect(stepLabel(null)).toBeNull();
  });
});

describe("modelTooltip", () => {
  it("gives a ws child its model, alias and endpoint with the URL and API", () => {
    const child = entry({ runner: "ws", model: "qwen/qwen3.5-9b", wsRunner: run });
    expect(modelTooltip(child)).toBe(
      "qwen/qwen3.5-9b · alias qwen9b · endpoint gpu (http://gpu-a.inference.svc:1234, OpenAI-compatible)",
    );
  });

  it("gives a Claude agent its model and Claude Code", () => {
    expect(modelTooltip(entry({ runner: "claude", model: "sonnet-5-5" }))).toBe("sonnet-5-5 · Claude Code");
  });

  it("gives nothing for an agent without a model", () => {
    expect(modelTooltip(entry({ runner: "claude", model: null }))).toBeUndefined();
  });
});

describe("isRunnerRunning", () => {
  it.each([
    ["working", true],
    ["starting", true],
    ["idle", false],
    ["stopped", false],
  ] as const)("is %s for a ws child: %s", (status, expected) => {
    expect(isRunnerRunning(entry({ runner: "ws", status }))).toBe(expected);
  });

  it("is false for a Claude agent", () => {
    expect(isRunnerRunning(entry({ runner: "claude", status: "working" }))).toBe(false);
  });
});
