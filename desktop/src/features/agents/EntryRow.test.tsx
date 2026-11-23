import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Entry, WsRunner } from "../../api";
import { entry } from "../../test/fixtures";
import { EntryRow } from "./EntryRow";

const wsRun: WsRunner = {
  modelAlias: "qwen9b",
  endpoint: "gpu",
  url: "http://gpu-a.inference.svc:1234",
  api: "openai",
  contextTokens: 65536,
  step: 12,
  maxSteps: 80,
  restarts: 0,
  usage: null,
  mcp: [],
  exit: null,
  endpointChanges: 0,
};

const wsChild = entry({ runner: "ws", model: "qwen/qwen3.5-9b", wsRunner: wsRun, status: "working" });
const claudeAgent = entry({ runner: "claude", model: "sonnet-5-5", status: "working" });

const renderRow = (agent: Entry) => render(<EntryRow entry={agent} current={false} onClick={vi.fn()} />);

/** The row's element tree as tags and class names, which the text doesn't change. */
const shape = (container: HTMLElement): string[] =>
  Array.from(container.querySelectorAll("*")).map((el) => `${el.tagName}.${el.className}`);

describe("EntryRow", () => {
  it("shows a Claude agent's model quietly, with Claude Code in its tooltip", () => {
    renderRow(claudeAgent);
    expect(screen.getByText("sonnet-5-5")).toBeInTheDocument();
    expect(screen.getByRole("button")).toHaveAttribute("title", "sonnet-5-5 · Claude Code");
  });

  it("shows a ws child's model whole, and its step as the summary", () => {
    renderRow(wsChild);
    expect(screen.getByText("qwen/qwen3.5-9b")).toBeInTheDocument();
    expect(screen.getByText("step 12/80")).toBeInTheDocument();
    expect(screen.getByRole("button")).toHaveAttribute(
      "title",
      "qwen/qwen3.5-9b · alias qwen9b · endpoint gpu (http://gpu-a.inference.svc:1234, OpenAI-compatible)",
    );
  });

  it("renders a ws child and a Claude agent with the same markup, apart from the text", () => {
    const claude = renderRow(claudeAgent);
    const claudeShape = shape(claude.container);
    claude.unmount();
    const ws = renderRow(wsChild);
    expect(shape(ws.container)).toEqual(claudeShape);
  });

  it("shows no model for an agent that hasn't answered yet", () => {
    renderRow(entry({ runner: "claude", model: null }));
    expect(screen.getByRole("button")).not.toHaveAttribute("title");
  });

  it("draws a progress bar of the workspace's steps when it has a rollup", () => {
    renderRow(entry({ name: "fix-login", rollup: { total: 4, done: 1, fraction: 0.25 } }));
    const bar = screen.getByRole("progressbar", { name: "fix-login progress" });
    expect(bar).toHaveAttribute("aria-valuenow", "1");
    expect(bar).toHaveAttribute("aria-valuemax", "4");
    expect(screen.getByText("1/4")).toBeVisible();
  });

  it("draws no progress bar without a rollup", () => {
    renderRow(entry({ rollup: null }));
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("draws a failed entry's progress bar in the error colour", () => {
    renderRow(entry({ name: "t1-review", result: "FAIL", rollup: { total: 1, done: 0, fraction: 0 } }));
    expect(screen.getByRole("progressbar", { name: "t1-review progress" })).toHaveClass("MuiLinearProgress-colorError");
  });
});
