export type PushState = "unsupported" | "insecure" | "denied" | "default" | "granted" | "subscribed";

/** What the server sends for one notification (design README, "Payload"). */
export interface PushPayload {
  readonly title: string;
  readonly body: string;
  readonly tag: string;
  readonly key: string | null;
  readonly url: string;
}

export interface TestResult {
  readonly sent: number;
  readonly failed: number;
}

const ICON = "/icons/icon-192.png";

const supported = (): boolean =>
  navigator.serviceWorker !== undefined && typeof PushManager !== "undefined" && typeof Notification !== "undefined";

const currentSubscription = async (): Promise<PushSubscription | null> => {
  const registration = await navigator.serviceWorker.getRegistration();
  return registration === undefined ? null : registration.pushManager.getSubscription();
};

const readyRegistration = (): Promise<ServiceWorkerRegistration> => navigator.serviceWorker.ready;

const failed = (path: string, res: Response): Error => new Error(`${path}: status ${res.status}`);

const postJson = async (path: string, body: unknown): Promise<void> => {
  const res = await fetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw failed(path, res);
  }
};

const base64UrlToBytes = (value: string): Uint8Array<ArrayBuffer> => {
  const base64 = value.replace(/-/g, "+").replace(/_/g, "/");
  return Uint8Array.from(atob(base64.padEnd(Math.ceil(base64.length / 4) * 4, "=")), (char) => char.charCodeAt(0));
};

const vapidKey = async (): Promise<Uint8Array<ArrayBuffer>> => {
  const res = await fetch("/api/push/vapid", { credentials: "same-origin" });
  if (!res.ok) {
    throw failed("/api/push/vapid", res);
  }
  const { publicKey } = (await res.json()) as { publicKey: string };
  return base64UrlToBytes(publicKey);
};

const browserName = (ua: string): string => {
  if (ua.includes("Edg/")) {
    return "Edge";
  }
  if (ua.includes("Firefox/")) {
    return "Firefox";
  }
  if (ua.includes("Chrome/")) {
    return "Chrome";
  }
  if (ua.includes("Safari/")) {
    return "Safari";
  }
  return "Browser";
};

const osName = (ua: string): string => {
  if (/iPhone|iPad|iPod/.test(ua)) {
    return "iOS";
  }
  if (ua.includes("Android")) {
    return "Android";
  }
  if (ua.includes("Windows")) {
    return "Windows";
  }
  if (ua.includes("Mac OS X")) {
    return "macOS";
  }
  if (ua.includes("Linux")) {
    return "Linux";
  }
  return "unknown OS";
};

const browserLabel = (ua: string): string => `${browserName(ua)} on ${osName(ua)}`;

export const pushState = async (): Promise<PushState> => {
  if (!isSecureContext) {
    return "insecure";
  }
  if (!supported()) {
    return "unsupported";
  }
  if (Notification.permission === "denied") {
    return "denied";
  }
  if (Notification.permission === "default") {
    return "default";
  }
  return (await currentSubscription()) === null ? "granted" : "subscribed";
};

export const enablePush = async (): Promise<PushState> => {
  const permission = await Notification.requestPermission();
  if (permission !== "granted") {
    return pushState();
  }
  const applicationServerKey = await vapidKey();
  const registration = await readyRegistration();
  const subscription = await registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey });
  try {
    await postJson("/api/push/subscribe", { subscription: subscription.toJSON(), label: browserLabel(navigator.userAgent) });
  } catch (err: unknown) {
    await subscription.unsubscribe();
    throw err;
  }
  return "subscribed";
};

export const disablePush = async (): Promise<PushState> => {
  const subscription = await currentSubscription();
  if (subscription === null) {
    return pushState();
  }
  await postJson("/api/push/unsubscribe", { endpoint: subscription.endpoint });
  await subscription.unsubscribe();
  return pushState();
};

export const sendTestPush = async (): Promise<TestResult> => {
  const res = await fetch("/api/push/test", { method: "POST", credentials: "same-origin" });
  if (!res.ok) {
    throw failed("/api/push/test", res);
  }
  return (await res.json()) as TestResult;
};

export const registerServiceWorker = async (): Promise<void> => {
  if (navigator.serviceWorker === undefined) {
    return;
  }
  await navigator.serviceWorker.register("/sw.js");
};

/** Shows a notification from the open page, for a browser that is not subscribed to push. */
export const showInPage = async (payload: PushPayload): Promise<void> => {
  if ((await pushState()) !== "granted") {
    return;
  }
  const registration = await readyRegistration();
  await registration.showNotification(payload.title, {
    body: payload.body,
    tag: payload.tag,
    data: { url: payload.url, key: payload.key },
    icon: ICON,
    badge: ICON,
  });
};
