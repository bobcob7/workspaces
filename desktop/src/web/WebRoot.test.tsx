import { act, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { testStore } from "../test/fixtures";
import { renderWithStore } from "../test/render";
import { WebRoot } from "./WebRoot";

const session = (authenticated: boolean): Response =>
  new Response(JSON.stringify({ authenticated, username: authenticated ? "me" : null, version: "0.1.0" }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

const renderRoot = (): void => {
  renderWithStore(
    <WebRoot>
      <p>the app</p>
    </WebRoot>,
    testStore(),
  );
};

describe("WebRoot", () => {
  beforeEach(() => {
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
  });

  it("shows a spinner until the session is known", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => undefined)));
    renderRoot();
    expect(screen.getByRole("progressbar", { name: "Loading" })).toBeInTheDocument();
    vi.unstubAllGlobals();
  });

  it("shows the login page when the browser isn't signed in", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => session(false)),
    );
    renderRoot();
    expect(await screen.findByRole("heading", { name: "Workspaces" })).toBeInTheDocument();
    expect(screen.queryByText("the app")).not.toBeInTheDocument();
    vi.unstubAllGlobals();
  });

  it("shows its children when the browser is signed in", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => session(true)),
    );
    renderRoot();
    expect(await screen.findByText("the app")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Workspaces" })).not.toBeInTheDocument();
    vi.unstubAllGlobals();
  });

  it("shows the login page when a call reports the session has ended", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => session(true)),
    );
    renderRoot();
    await screen.findByText("the app");
    act(() => {
      window.dispatchEvent(new Event("ws-online:unauthenticated"));
    });
    expect(await screen.findByRole("heading", { name: "Workspaces" })).toBeInTheDocument();
    vi.unstubAllGlobals();
  });
});
