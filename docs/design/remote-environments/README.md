# Design: remote environments

Decisions: [0072](../../adrs/0072-remote-environments.md),
[0073](../../adrs/0073-environment-agent-is-ws.md),
[0074](../../adrs/0074-mqtt-transport-embedded-broker.md),
[0075](../../adrs/0075-environment-enrollment-and-certificates.md),
[0076](../../adrs/0076-environment-platforms-and-phasing.md).

## Picture

```
 browser / desktop app
        |
   ws-online  ──  ws daemon ── embedded MQTT 5 broker  (:8883 mTLS, :8884 enroll)
                                   ^            ^
                    dials out,     |            |   dials out,
                    mTLS           |            |   mTLS
                         linux-server           win-rig
                         ws env agent run       ws env agent run
                         └ own CONFIG_DIR       └ own CONFIG_DIR
                         └ git cache, state     └ ...
                         └ claude (installed    └ claude (installed
                           by the user)           by the user)
```

Every workspace has one environment. `ws machine spawn` inside a remote
workspace runs the remote's own `ws`, so the child is on the same machine.

## Enrolling

```
$ ws env add linux-server
Code (valid 15 minutes, shown once):
  ws-enroll-v1.eyJoIjoid3MuZXhhbXBsZS5jb206ODg4NCIs...

On linux-server:
  ws env agent enroll ws-enroll-v1.eyJoIjoid3Mu...
  ws env agent run            # or install the service: see docs/tools/ws-env.md
```

## Settings › Environments (desktop and Workspace Online)

```
Environments                                              [ Add environment ]
┌───────────────────────────────────────────────────────────────────────────┐
│ ● local          this machine                                             │
│ ● linux-server   online   ws 0.31  linux/amd64   claude 2.4 signed in     │
│                  4 workspaces                          [ Rotate ] [ Remove ]│
│ ○ win-rig        offline since 14:02  (last seen)      [ Rotate ] [ Remove ]│
│ ◌ nas            waiting for enrollment  code expires in 11:42  [ Cancel ]  │
└───────────────────────────────────────────────────────────────────────────┘
claude: missing  -> "Install Claude Code on this machine, then try again."
```

Add environment: a name field, then the code and the two commands to copy,
with the expiry counting down. When the agent connects, the row turns
green on its own.

## New agent dialog

```
New agent
  Repos        [ buff-bear@main                 ]
  Environment  [ linux-server               v ]     local
                                                      linux-server
  Prompt       [ ...                            ]     win-rig  (offline, disabled)
```

The picker remembers the last choice; offline environments are disabled
with their reason. For a child of a workspace there's no picker: it shows
"Runs on linux-server, like its parent".

## Agents list

Each row gets a small environment badge unless it's `local`. A filter chip
row lists environments with their online dots. An offline environment's
rows are dimmed with "offline" and keep their last known status; their
actions (attach, stop, remove) are disabled with the reason.

## Failure messages

| Situation | Message |
| --- | --- |
| Start on an offline environment | `win-rig is offline (last seen 14:02). Nothing was started.` |
| Child with a different environment than its parent | `environment_mismatch: this workspace is a child of one on linux-server` |
| Agent older than the controller | `linux-server runs ws 0.28; this controller needs 0.31 or newer. Update it there.` |
| Claude Code missing on the remote | `claude isn't installed on linux-server.` |
| Code expired or used | `That code was used or has expired. Run ws env rotate linux-server.` |
