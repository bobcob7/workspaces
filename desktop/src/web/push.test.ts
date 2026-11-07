import { afterEach, describe, expect, it, vi } from "vitest";
import {
  disablePush,
  enablePush,
  pushState,
  registerServiceWorker,
  sendTestPush,
  showInPage,
  type PushPayload,
} from "./push";

const CHROME_MAC =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36";

const subscription = {
  endpoint: "https://push.example.test/abc",
  toJSON: () => ({ endpoint: "https://push.example.test/abc", expirationTime: null, keys: { p256dh: "p", auth: "a" } }),
  unsubscribe: vi.fn(async () => {
    registration.pushManager.getSubscription.mockResolvedValue(null);
    return true;
  }),
};

const registration = {
  pushManager: {
    getSubscription: vi.fn(async (): Promise<unknown> => null),
    subscribe: vi.fn(async () => subscription),
  },
  showNotification: vi.fn(async () => undefined),
};

/** Stubs the browser for one state: the permission, and whether this browser holds a subscription. */
const stubBrowser = (permission: NotificationPermission, subscribed = false): void => {
  vi.stubGlobal("Notification", {
    permission,
    requestPermission: vi.fn(async () => (permission === "denied" ? "denied" : "granted")),
  });
  vi.stubGlobal("PushManager", class PushManager {});
  vi.stubGlobal("isSecureContext", true);
  registration.pushManager.getSubscription.mockResolvedValue(subscribed ? subscription : null);
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: {
      register: vi.fn(async () => registration),
      ready: Promise.resolve(registration),
      getRegistration: vi.fn(async () => registration),
    },
  });
};

const jsonResponse = (body: unknown): Response =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

const emptyResponse = (): Response => new Response(null, { status: 204 });

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  Reflect.deleteProperty(navigator, "serviceWorker");
  Reflect.deleteProperty(navigator, "userAgent");
});

describe("pushState", () => {
  it("is default when the permission hasn't been asked", async () => {
    stubBrowser("default");
    expect(await pushState()).toBe("default");
  });

  it("is denied when the permission was refused", async () => {
    stubBrowser("denied");
    expect(await pushState()).toBe("denied");
  });

  it("is granted when the permission is given but this browser isn't subscribed", async () => {
    stubBrowser("granted");
    expect(await pushState()).toBe("granted");
  });

  it("is subscribed when this browser holds a subscription", async () => {
    stubBrowser("granted", true);
    expect(await pushState()).toBe("subscribed");
  });

  it("is unsupported without a service worker", async () => {
    stubBrowser("granted");
    Object.defineProperty(navigator, "serviceWorker", { configurable: true, value: undefined });
    expect(await pushState()).toBe("unsupported");
  });

  it("is insecure outside a secure context", async () => {
    stubBrowser("granted", true);
    vi.stubGlobal("isSecureContext", false);
    expect(await pushState()).toBe("insecure");
  });
});

describe("enablePush", () => {
  it("subscribes with the server's key and posts the subscription with a browser label", async () => {
    stubBrowser("default");
    Object.defineProperty(navigator, "userAgent", { configurable: true, value: CHROME_MAC });
    const fetchMock = vi.fn(async (input: string) =>
      input === "/api/push/vapid" ? jsonResponse({ publicKey: "BPublicKeyAAAA" }) : jsonResponse({ id: "x" }),
    );
    vi.stubGlobal("fetch", fetchMock);

    expect(await enablePush()).toBe("subscribed");
    expect(registration.pushManager.subscribe).toHaveBeenCalledWith(
      expect.objectContaining({ userVisibleOnly: true, applicationServerKey: expect.any(Uint8Array) }),
    );
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/push/subscribe",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ subscription: subscription.toJSON(), label: "Chrome on macOS" }),
      }),
    );
  });

  it("doesn't subscribe when the permission is refused", async () => {
    stubBrowser("denied");
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    expect(await enablePush()).toBe("denied");
    expect(fetchMock).not.toHaveBeenCalled();
    expect(registration.pushManager.subscribe).not.toHaveBeenCalled();
  });
});

describe("disablePush", () => {
  it("posts the endpoint and unsubscribes", async () => {
    stubBrowser("granted", true);
    const fetchMock = vi.fn(async () => emptyResponse());
    vi.stubGlobal("fetch", fetchMock);

    expect(await disablePush()).toBe("granted");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/push/unsubscribe",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ endpoint: subscription.endpoint }) }),
    );
    expect(subscription.unsubscribe).toHaveBeenCalled();
  });
});

describe("sendTestPush", () => {
  it("returns the sent and failed counts", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({ sent: 2, failed: 1 })));
    expect(await sendTestPush()).toEqual({ sent: 2, failed: 1 });
  });
});

describe("registerServiceWorker", () => {
  it("registers /sw.js", async () => {
    stubBrowser("granted", true);
    await registerServiceWorker();
    expect(navigator.serviceWorker.register).toHaveBeenCalledWith("/sw.js");
  });

  it("does nothing without a service worker", async () => {
    Object.defineProperty(navigator, "serviceWorker", { configurable: true, value: undefined });
    await expect(registerServiceWorker()).resolves.toBeUndefined();
  });
});

describe("showInPage", () => {
  const payload: PushPayload = {
    title: "fix-login needs input",
    body: "Which branch should I use?",
    tag: "needsInput:fix-login",
    key: "fix-login",
    url: "/?agent=fix-login",
  };

  it("shows the payload under its tag when granted and not subscribed", async () => {
    stubBrowser("granted");
    await showInPage(payload);
    expect(registration.showNotification).toHaveBeenCalledWith(
      "fix-login needs input",
      expect.objectContaining({ tag: "needsInput:fix-login", data: { url: "/?agent=fix-login", key: "fix-login" } }),
    );
  });

  it("leaves it to push when subscribed", async () => {
    stubBrowser("granted", true);
    await showInPage(payload);
    expect(registration.showNotification).not.toHaveBeenCalled();
  });
});
