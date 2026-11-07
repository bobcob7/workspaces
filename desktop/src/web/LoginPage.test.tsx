import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { testStore } from "../test/fixtures";
import { renderWithStore } from "../test/render";
import { LoginPage } from "./LoginPage";

const renderLogin = (onLoggedIn = vi.fn()) => {
  renderWithStore(<LoginPage onLoggedIn={onLoggedIn} />, testStore());
  return onLoggedIn;
};

const fillAndSubmit = async (): Promise<void> => {
  await userEvent.type(screen.getByRole("textbox", { name: "Username" }), "me");
  await userEvent.type(screen.getByLabelText("Password"), "secret");
  await userEvent.click(screen.getByRole("button", { name: "Log in" }));
};

describe("LoginPage", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
  });

  it("logs in with the typed credentials and calls onLoggedIn", async () => {
    const onLoggedIn = renderLogin();
    await fillAndSubmit();
    await waitFor(() => expect(onLoggedIn).toHaveBeenCalledTimes(1));
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/login",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ username: "me", password: "secret" }) }),
    );
  });

  it.each([
    [401, "Wrong username or password."],
    [429, "Too many attempts. Try again later."],
  ])("shows the message for status %i", async (status, message) => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status }));
    const onLoggedIn = renderLogin();
    await fillAndSubmit();
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(onLoggedIn).not.toHaveBeenCalled();
  });

  it("shows a reachability message when the request fails", async () => {
    fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    const onLoggedIn = renderLogin();
    await fillAndSubmit();
    expect(await screen.findByText("Couldn't reach the server.")).toBeInTheDocument();
    expect(onLoggedIn).not.toHaveBeenCalled();
  });

  it("clears the previous error when a new attempt is submitted", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 401 }));
    fetchMock.mockReturnValueOnce(new Promise(() => undefined));
    renderLogin();
    await fillAndSubmit();
    expect(await screen.findByText("Wrong username or password.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Log in" }));
    await waitFor(() => expect(screen.queryByText("Wrong username or password.")).not.toBeInTheDocument());
  });

  it("disables the button while the login is sent", async () => {
    fetchMock.mockReturnValueOnce(new Promise(() => undefined));
    renderLogin();
    await fillAndSubmit();
    expect(screen.getByRole("button", { name: "Log in" })).toBeDisabled();
  });
});
