import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Api, CaStatus } from "../../api";
import { fakeApi, remoteEnvironment, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { CaCard } from "./CaCard";

let copied = "";

beforeEach(() => {
  copied = "";
  Object.defineProperty(navigator, "clipboard", { value: { writeText: vi.fn(async (text: string) => void (copied = text)) }, configurable: true });
});

afterEach(() => {
  Reflect.deleteProperty(navigator, "clipboard");
});

const ca = (fingerprint: string, notAfter: string) => ({ fingerprint, notAfter });
const status = (overrides: Partial<CaStatus> = {}): CaStatus => ({
  current: ca("0123456789abcdef0123456789abcdef", "2036-10-02T09:00:00Z"),
  old: null,
  environmentsOnCurrent: 2,
  environmentsOnOld: 0,
  warnings: [],
  ...overrides,
});

const open = async (state: CaStatus, overrides: Partial<Api> = {}) => {
  const api = fakeApi({ environmentsCaStatus: vi.fn(async () => state), ...overrides });
  const store = testStore({ api });
  renderWithStore(<CaCard client={api} />, store);
  await screen.findByRole("group", { name: "Certificate authority" });
  return api;
};

describe("CaCard", () => {
  it("shows the first 16 hex of the fingerprint and when it expires", async () => {
    await open(status());
    expect(screen.getByLabelText("Current CA fingerprint")).toHaveTextContent(/^0123456789abcdef$/);
    expect(screen.getByText("expires 2036-10-02")).toBeInTheDocument();
  });

  it("copies the whole fingerprint", async () => {
    await open(status());
    await userEvent.click(screen.getByRole("button", { name: "Copy Current CA fingerprint" }));
    expect(copied).toBe("0123456789abcdef0123456789abcdef");
  });

  it("Finish is disabled while environments use the old CA", async () => {
    await open(status({ old: ca("fedcba9876543210ffff", "2030-01-01T00:00:00Z"), environmentsOnOld: 2 }));
    expect(screen.getByRole("button", { name: "Finish rotation" })).toBeDisabled();
    expect(screen.getByText("2 environments still use the old CA")).toBeInTheDocument();
    expect(screen.getByText("2 environments still on the old CA")).toBeInTheDocument();
    expect(screen.getByLabelText("Old CA fingerprint")).toHaveTextContent("fedcba9876543210");
  });

  it("Finish is enabled once none use the old CA", async () => {
    const api = await open(status({ old: ca("fedcba9876543210ffff", "2030-01-01T00:00:00Z"), environmentsOnOld: 0 }));
    await userEvent.click(screen.getByRole("button", { name: "Finish rotation" }));
    expect(api.environmentsCaRotate).toHaveBeenCalledWith(true, false);
  });

  it("Force enables Finish and names the environments it strands", async () => {
    const api = fakeApi({
      environmentsCaStatus: vi.fn(async () => status({ old: ca("fedcba9876543210ffff", "2030-01-01T00:00:00Z"), environmentsOnOld: 1 })),
      environmentsCaRotate: vi.fn(async () => ({ current: null, old: null, dropped: null, stranded: ["win-rig"], warnings: [] })),
    });
    const store = testStore({ api, state: { environments: { items: [remoteEnvironment({ name: "win-rig", certCa: "old" })], loadedAt: 1, error: null, warnings: [], lastChoice: "local", latestRequestId: null } } });
    renderWithStore(<CaCard client={api} />, store);
    await screen.findByRole("group", { name: "Certificate authority" });
    await userEvent.click(screen.getByRole("checkbox", { name: /Force.*win-rig/ }));
    await userEvent.click(screen.getByRole("button", { name: "Finish rotation" }));
    expect(api.environmentsCaRotate).toHaveBeenCalledWith(true, true);
    expect(await screen.findByText(/Stranded, enroll again: win-rig/)).toBeInTheDocument();
  });

  it("Rotate CA asks first", async () => {
    const api = await open(status());
    await userEvent.click(screen.getByRole("button", { name: "Rotate CA" }));
    expect(api.environmentsCaRotate).not.toHaveBeenCalled();
    await userEvent.click(within(screen.getByRole("dialog", { name: "Rotate the CA?" })).getByRole("button", { name: "Rotate CA" }));
    expect(api.environmentsCaRotate).toHaveBeenCalledWith(false, false);
    await waitFor(() => expect(api.environmentsCaStatus).toHaveBeenCalledTimes(2));
  });

  it("Rotate CA is disabled while an old CA exists", async () => {
    await open(status({ old: ca("fedcba9876543210ffff", "2030-01-01T00:00:00Z"), environmentsOnOld: 0 }));
    expect(screen.getByRole("button", { name: "Rotate CA" })).toBeDisabled();
  });

  it("shows warnings", async () => {
    await open(status({ warnings: ["the CA expires in 12 days"] }));
    expect(screen.getByText("the CA expires in 12 days")).toBeInTheDocument();
  });
});
