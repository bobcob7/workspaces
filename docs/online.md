# Workspace Online

Workspace Online runs the [desktop app](../desktop/README.md) on a server,
in Docker, and serves it to your browser behind a username and password.
Install it as a web app (PWA) on a laptop or phone, and it notifies you,
like the desktop tray, when one of your agents needs input or is done.
Claude Code is installed in the image and uses your Pro or Max
subscription through a long-lived token. Decided in
[0053](adrs/0053-workspace-online.md); the API is in the
[design](design/workspace-online/README.md).

## Quick start

```console
$ claude setup-token            # on any machine with a browser; copy the token
$ docker build -f deploy/docker/Dockerfile -t ws-online .
$ docker run -d --name ws-online -p 8080:8080 \
    -e WS_ONLINE_USERNAME=me \
    -e WS_ONLINE_PASSWORD='a long passphrase' \
    -e CLAUDE_CODE_OAUTH_TOKEN='sk-ant-oat01-…' \
    -e GH_TOKEN='ghp_…' \
    -v ws-home:/home/ws -v ws-data:/data \
    ws-online
```

Open <http://localhost:8080>, log in, and start an agent. To reach it
from another device, put it behind HTTPS (see below).

## Getting the long-lived token

Claude Code in the container signs in with your subscription through
`CLAUDE_CODE_OAUTH_TOKEN`:

1. On a machine with a browser and Claude Code, run `claude setup-token`.
2. Sign in with the account that has the Pro or Max plan.
3. Copy the token it prints (it starts `sk-ant-oat01-`) into
   `CLAUDE_CODE_OAUTH_TOKEN`. It's valid for a year; run `setup-token`
   again to replace it, then restart the container.

Treat it like a password: anyone with it uses your subscription. Prefer
an env file or Docker secret over the command line.

If agents started in the container don't pick the token up (see
"Known limits"), log in once inside the container instead:
`docker exec -it ws-online claude /login`. That writes
`~/.claude/.credentials.json` to the home volume, where it persists.

## Environment

| Variable | Required | Default | Meaning |
| -------- | -------- | ------- | ------- |
| `WS_ONLINE_USERNAME` | yes | | The one user who can log in |
| `WS_ONLINE_PASSWORD` | one of these two | | The password, in plain text |
| `WS_ONLINE_PASSWORD_HASH` | one of these two | | An argon2 hash of it, from `docker run --rm -i ws-online ws-online hash-password` (reads the password on stdin) |
| `CLAUDE_CODE_OAUTH_TOKEN` | yes | | The token from `claude setup-token` |
| `GH_TOKEN` | no | | A GitHub token for `gh`, reviews, and `git push` over HTTPS (`gh auth setup-git` runs at start) |
| `WS_GIT_NAME`, `WS_GIT_EMAIL` | no | | Written to `~/.gitconfig` at start, for agents' commits |
| `WS_ONLINE_PUBLIC_URL` | behind a proxy | | The URL you open, e.g. `https://ws.example.com`; the only allowed `Origin`, and the push contact |
| `WS_ONLINE_SECURE_COOKIE` | no | `auto` | `auto` marks the cookie `Secure` when the public URL is `https://`; or `true` / `false` |
| `WS_ONLINE_SESSION_DAYS` | no | `30` | How long a login lasts (1–365) |
| `WS_ONLINE_TRUST_PROXY` | no | `false` | `true` to rate-limit logins by `X-Forwarded-For` (only behind a proxy you control) |
| `WS_ONLINE_VAPID_SUBJECT` | no | public URL, else `mailto:ws-online@localhost` | Contact sent to push services; Apple wants a real `mailto:` or `https://` |
| `WS_ONLINE_ADDR` | no | `0.0.0.0:8080` | Listen address inside the container (the health check assumes port 8080) |
| `WS_CONFIG_DIR` | no | `/data/ws` | `ws`'s directory: config, cache, workspaces |
| `WS_ONLINE_DATA_DIR` | no | `/data/online` | The server's settings, push keys, subscriptions, sessions |
| `TZ` | no | UTC | Time zone for loops and timestamps |

The container exits at start, with a message naming the variable, when a
required one is missing, both password variables are set, or a value is
invalid.

## Volumes

| Mount | Suggested volume | What persists |
| ----- | ---------------- | ------------- |
| `/home/ws` | `ws-home` | Everything in the home directory: Claude Code's `~/.claude/` (settings.json, agents, commands, jobs, transcripts, credentials) and `~/.claude.json` (MCP servers, trust, onboarding); Claude Code itself in `~/.local/bin` and `~/.local/share/claude`, which updates itself there; anything you `npm install -g` (MCP servers), since npm's prefix is `~/.local`; `gh`'s login, `~/.ssh`, `~/.gitconfig` |
| `/data` | `ws-data` | `ws` (`/data/ws`: config.toml, the repo cache, workspaces and their state, reviews, loops) and the server (`/data/online`: `desktop.json`, `vapid.json`, `subscriptions.json`, `sessions.json`, review reports) |

The image runs as user `ws`, uid 1000. Named volumes get the right owner
automatically; for bind mounts, `chown -R 1000:1000` the host
directories first.

On first start the entrypoint copies Claude Code into the home volume
if `~/.local/bin/claude` isn't there, marks Claude Code's onboarding as
done, and trusts `/data/ws/workspaces` in `~/.claude.json`, so agents
start without prompts. Add MCP servers as usual, for example
`docker exec -it ws-online claude mcp add --scope user …`; they're kept.

## Docker Compose

`deploy/docker/docker-compose.yml`:

```yaml
services:
  ws-online:
    build:
      context: ../..
      dockerfile: deploy/docker/Dockerfile
    image: ws-online
    restart: unless-stopped
    ports:
      - "127.0.0.1:8080:8080"
    env_file: .env
    volumes:
      - ws-home:/home/ws
      - ws-data:/data
volumes:
  ws-home:
  ws-data:
```

with a `.env` beside it (keep it out of git):

```sh
WS_ONLINE_USERNAME=me
WS_ONLINE_PASSWORD_HASH=$argon2id$v=19$m=19456,t=2,p=1$…
CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-…
GH_TOKEN=ghp_…
WS_ONLINE_PUBLIC_URL=https://ws.example.com
```

Run `docker compose -f deploy/docker/docker-compose.yml up -d --build`.
In a `.env` file, a `$` in the hash must be written `$$`, or the line
quoted with single quotes, depending on your Compose version.

## HTTPS and a reverse proxy

Browsers allow service workers, PWA install and push only over HTTPS,
or on `http://localhost`. Anywhere else, put a TLS proxy in front, set
`WS_ONLINE_PUBLIC_URL` to the URL you open, and publish the port only to
the proxy (as `127.0.0.1:8080` above). The proxy must pass WebSockets
on `/api/ws`.

Caddy:

```
ws.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

nginx:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_read_timeout 1h;
}
```

A Tailscale or WireGuard address with a certificate (`tailscale serve`)
works the same way.

## Installing the app

- **Chrome, Edge (desktop and Android):** the install icon in the address
  bar, or menu › Install Workspaces.
- **Safari on macOS:** File › Add to Dock.
- **iPhone and iPad (16.4+):** Share › Add to Home Screen, then open it
  from the Home Screen. Push works only from there.

## Notifications

They follow the desktop tray's rules ([0050](adrs/0050-desktop-tray-notifications.md)):
only your top-level, non-hidden agents, once when one starts needing
input or finishes. Settings › Notifications has the same three switches,
shared by every browser, and a **This browser** row:

- **Turn on** asks the browser for permission and subscribes this device
  to push; it then gets notifications with no tab open.
- **Send a test** sends one to every subscribed device.
- **Turn off** unsubscribes this device.

With the app open but push off, the tab still shows notifications while
permission is granted. Clicking one opens the app on that agent.

## What web mode leaves out

| Desktop feature | In the browser |
| --------------- | -------------- |
| Open folder, Open in editor | Hidden: the files are on the server. Use a shell workspace instead |
| Settings › Workspaces (open commands) | Hidden |
| Menu-bar tray | Replaced by push notifications; the window title still counts agents needing input |
| Links (PRs, findings) | Open in a new browser tab |
| Review report in the browser | Opens in a new tab, served by the server |
| Terminals after closing the tab | Closed; agents keep running, but a `ws shell` started in that tab ends |

Everything else (agents, conversations, terminals, repos, reviews,
loops, settings) works as in the desktop app. The auto-review checker
and loops run in the server, with or without a browser open.

## Security notes

- One user. The password is compared in constant time; failed logins are
  limited to 10 per client address per 15 minutes.
- The session cookie is `HttpOnly` and `SameSite=Strict`, and `Secure`
  over HTTPS. Every `POST` and the WebSocket must come from the app's own
  origin. Logins last `WS_ONLINE_SESSION_DAYS`; changing the username or
  password ends every session, and so does deleting
  `/data/online/sessions.json` and restarting.
- Logging in gives a shell on the server, as the `ws` user, with your
  Claude subscription and GitHub token: treat the password like an SSH
  key. Use a long one, prefer `WS_ONLINE_PASSWORD_HASH`, and don't expose
  plain HTTP beyond localhost.
- Agents run with whatever permission mode you start them in, inside the
  container. The container is the sandbox: don't mount the Docker socket
  or host directories you don't want agents to change.
- `GET /healthz` is public and says only `ok` and the version.

## Health check and logs

The image's `HEALTHCHECK` calls `http://127.0.0.1:8080/healthz`. Server
messages go to the container's stderr: `docker logs ws-online`.

## Known limits

- There is no published image yet: build it from this repo. The image
  smoke test runs on the builder's own platform only.
- **Unverified** until the first real run: Claude Code's background
  service and `claude attach` inside a container, and the token reaching
  background sessions. See [0053](adrs/0053-workspace-online.md),
  Consequences.
