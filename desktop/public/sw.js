const CACHE = "ws-shell-v1";
const SHELL = ["/", "/manifest.webmanifest"];
const ICON = "/icons/icon-192.png";

const isApi = (pathname) => pathname === "/api" || pathname.startsWith("/api/");

const isStatic = (pathname) => pathname.startsWith("/assets/") || pathname.startsWith("/icons/");

const networkFirst = async (request) => {
  const cache = await caches.open(CACHE);
  try {
    const response = await fetch(request);
    if (response.ok) {
      await cache.put("/", response.clone());
    }
    return response;
  } catch {
    return (await cache.match("/")) ?? Response.error();
  }
};

const cacheFirst = async (request) => {
  const cached = await caches.match(request);
  if (cached !== undefined) {
    return cached;
  }
  const response = await fetch(request);
  if (response.ok) {
    const cache = await caches.open(CACHE);
    await cache.put(request, response.clone());
  }
  return response;
};

const readPayload = async (event) => {
  try {
    return event.data ? await event.data.json() : {};
  } catch {
    return {};
  }
};

const focusOrOpen = async (url, key) => {
  const [client] = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  if (client === undefined) {
    await self.clients.openWindow(url ?? "/");
    return;
  }
  await client.focus();
  if (key) {
    client.postMessage({ type: "select", key });
  }
};

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(SHELL)));
});

self.addEventListener("fetch", (event) => {
  const { request } = event;
  const { pathname } = new URL(request.url);
  if (request.method !== "GET" || isApi(pathname) || pathname === "/healthz") {
    return;
  }
  if (request.mode === "navigate") {
    event.respondWith(networkFirst(request));
    return;
  }
  if (isStatic(pathname)) {
    event.respondWith(cacheFirst(request));
  }
});

self.addEventListener("push", (event) => {
  event.waitUntil(
    readPayload(event).then((payload) =>
      self.registration.showNotification(payload.title ?? "Workspaces", {
        body: payload.body,
        tag: payload.tag,
        data: { url: payload.url, key: payload.key },
        icon: ICON,
        badge: ICON,
      }),
    ),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const { url, key } = event.notification.data ?? {};
  event.waitUntil(focusOrOpen(url, key));
});
