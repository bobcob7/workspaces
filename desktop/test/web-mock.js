// Turns the Tauri mock into the web build's /api and WebSocket, so the app runs
// in web mode in a plain browser. Load it after tauri-mock.js. Like it, this
// is not a test: it's outside src/ and its name doesn't match the test pattern.
(() => {
  const invokeCommand = window.__TAURI_INTERNALS__.invoke;
  delete window.__TAURI_INTERNALS__;
  window.__WEB_MOCK__ = { authenticated: true, username: "me", calls: [], push: "subscribed" };
  const realFetch = window.fetch.bind(window);
  const vapidKey = "BMockWebPushPublicKey0000000000000000000000000000000000000000000000000000000000000000000000";

  const json = (status, body) =>
    new Response(JSON.stringify(body ?? null), { status, headers: { "Content-Type": "application/json" } });
  const noContent = () => new Response(null, { status: 204 });
  const isChannel = (value) => typeof value === "object" && value !== null && "__channel" in value;

  const withoutChannels = (args) => Object.fromEntries(Object.entries(args).filter(([, value]) => !isChannel(value)));

  const asAppError = (error) =>
    typeof error === "object" && error !== null && "code" in error
      ? error
      : { code: "internal", message: error?.message ?? String(error) };

  let socket = null;
  const oauthUrl =
    "https://claude.ai/oauth/authorize?code=true&client_id=abc&redirect_uri=http%3A%2F%2Flocalhost%3A54545%2Fcallback";

  // terminal_open's channel gets one line holding an OAuth URL, as `/mcp` prints.
  const emitTerminalLine = (body) => {
    const channel = Object.values(body).find(isChannel);
    if (channel === undefined) {
      return;
    }
    const data = `Open this URL to sign in:\r\n${oauthUrl}\r\n`;
    setTimeout(() => socket?.emit({ type: "channel", id: channel.__channel, payload: { event: "data", data } }), 20);
  };

  const onlineSettings = {
    username: "me",
    addr: "0.0.0.0:8080",
    publicUrl: "https://ws.example.com",
    secureCookie: true,
    sessionDays: 30,
    trustProxy: true,
    dataDir: "/data/online",
    webDir: "/app/dist",
    vapidSubject: "mailto:me@example.com",
    password: "hash",
  };

  const invokeRoute = async (command, body) => {
    window.__WEB_MOCK__.calls.push({ command, args: body });
    try {
      const value = await invokeCommand(command, withoutChannels(body));
      if (command === "terminal_open") {
        emitTerminalLine(body);
      }
      if (command === "settings_environment") {
        return json(200, { ...value, online: onlineSettings });
      }
      return json(200, value);
    } catch (error) {
      return json(422, asAppError(error));
    }
  };

  const login = (body) => {
    if (body.username === "me" && body.password === "secret") {
      window.__WEB_MOCK__.authenticated = true;
      return noContent();
    }
    return json(401, { code: "bad_credentials", message: "wrong username or password" });
  };

  const pushRoute = (path) => {
    if (path === "/api/push/vapid") {
      return json(200, { publicKey: vapidKey });
    }
    if (path === "/api/push/unsubscribe") {
      return noContent();
    }
    if (path === "/api/push/subscribe") {
      return json(200, { id: "mock" });
    }
    if (path === "/api/push/test") {
      return json(200, { sent: 2, failed: 0 });
    }
    return json(200, {});
  };

  window.fetch = async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, location.href);
    const path = url.pathname;
    const method = (init.method ?? "GET").toUpperCase();
    const body = init.body ? JSON.parse(init.body) : {};
    if (path === "/api/session") {
      const { authenticated, username } = window.__WEB_MOCK__;
      return json(200, { authenticated, username: authenticated ? username : null, version: "0.1.0" });
    }
    if (path === "/api/login" && method === "POST") {
      return login(body);
    }
    if (path === "/api/logout" && method === "POST") {
      window.__WEB_MOCK__.authenticated = false;
      return noContent();
    }
    if (path.startsWith("/api/invoke/") && method === "POST") {
      return invokeRoute(decodeURIComponent(path.slice("/api/invoke/".length)), body);
    }
    if (path.startsWith("/api/push/")) {
      return pushRoute(path);
    }
    return realFetch(input, init);
  };

  class MockWebSocket {
    constructor(url) {
      this.url = url;
      this.readyState = 0;
      this.onopen = null;
      this.onmessage = null;
      this.onclose = null;
      socket = this;
      setTimeout(() => {
        this.readyState = 1;
        this.onopen?.(new Event("open"));
        this.emit({ type: "hello", connection: "mock" });
      }, 0);
    }

    send(data) {
      const frame = JSON.parse(data);
      if (frame.type === "invoke") {
        setTimeout(() => this.emit({ type: "result", id: frame.id, ok: true, value: null }), 0);
      }
    }

    close() {
      this.readyState = 3;
      this.onclose?.({ code: 1000 });
    }

    emit(message) {
      this.onmessage?.({ data: JSON.stringify(message) });
    }
  }

  window.WebSocket = MockWebSocket;

  // Push state is window.__WEB_MOCK__.push: "subscribed" (default), "granted", "default",
  // "denied" or "unsupported" (which removes the APIs). Read at call time.
  const pushMode = () => window.__WEB_MOCK__.push;
  const permission = () => (pushMode() === "denied" ? "denied" : pushMode() === "default" ? "default" : "granted");
  const whenSupported = (value) => () => (pushMode() === "unsupported" ? undefined : value);

  const subscription = {
    endpoint: "https://push.mock.test/subscription",
    expirationTime: null,
    keys: { p256dh: "mock-p256dh", auth: "mock-auth" },
    toJSON() {
      return { endpoint: this.endpoint, expirationTime: null, keys: this.keys };
    },
    async unsubscribe() {
      if (pushMode() === "subscribed") {
        window.__WEB_MOCK__.push = "granted";
      }
      return true;
    },
  };

  const registration = {
    pushManager: {
      getSubscription: async () => (pushMode() === "subscribed" ? subscription : null),
      subscribe: async () => {
        window.__WEB_MOCK__.push = "subscribed";
        return subscription;
      },
    },
    showNotification: async () => undefined,
  };

  const serviceWorker = {
    register: async () => registration,
    getRegistration: async () => registration,
    ready: Promise.resolve(registration),
    addEventListener() {},
    removeEventListener() {},
  };

  class MockNotification {
    static get permission() {
      return permission();
    }

    static requestPermission() {
      if (pushMode() === "default") {
        window.__WEB_MOCK__.push = "granted";
      }
      return Promise.resolve(permission());
    }
  }

  class MockPushManager {}

  Object.defineProperty(Navigator.prototype, "serviceWorker", { configurable: true, get: whenSupported(serviceWorker) });
  Object.defineProperty(window, "Notification", { configurable: true, get: whenSupported(MockNotification) });
  Object.defineProperty(window, "PushManager", { configurable: true, get: whenSupported(MockPushManager) });
})();
