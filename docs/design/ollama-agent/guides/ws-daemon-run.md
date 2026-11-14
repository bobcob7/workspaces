# ws daemon run

```
ws daemon run [--origin transient|managed]
```

Runs the `ws` daemon in the foreground until it gets `SIGTERM` or
`SIGINT`. It takes the lock `CONFIG_DIR/daemon/lock`, writes
`CONFIG_DIR/daemon/daemon.json`, logs to stderr and
`CONFIG_DIR/daemon/daemon.log`, and listens on its socket. Before it
listens, it adopts runners that are still running and, unless
`daemon.resume_on_start = false`, resumes the ones that were interrupted.

`--origin managed`, the default, runs until stopped: use it from launchd,
systemd or a container. `--origin transient` is what `ws` uses when it
starts a daemon on demand: it exits after 15 minutes with no running agent
and no request.

`--json` is refused: it prints no result, only log lines.

## Flags

| Flag | Meaning |
| ---- | ------- |
| `--origin transient\|managed` | Whether it exits when idle (`transient`) or runs until stopped (`managed`, the default) |

## Running it as a service

A launchd agent, `~/Library/LaunchAgents/com.github.bobcob7.ws-daemon.plist`:

```xml
<plist version="1.0"><dict>
  <key>Label</key><string>com.github.bobcob7.ws-daemon</string>
  <key>ProgramArguments</key><array><string>/usr/local/bin/ws</string><string>daemon</string><string>run</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
```

A systemd user unit, `~/.config/systemd/user/ws-daemon.service`:

```ini
[Service]
ExecStart=%h/.local/bin/ws daemon run
Restart=on-failure
[Install]
WantedBy=default.target
```

Set `WS_DAEMON_AUTOSTART=0` in your shell when a service runs it, so a
stopped service is an error instead of a second, on-demand daemon.

## Exit codes

| Situation                                                | Error code       | Exit |
| -------------------------------------------------------- | ---------------- | ---- |
| Arguments given, `--json`, or a bad `--origin`           | `usage`          | 2    |
| Another daemon holds the lock; `details.pid`             | `daemon_running` | 4    |
| `config.toml` doesn't parse                              | `config_invalid` | 1    |
| The socket directory isn't yours or can't be made        | `internal`       | 1    |

## Examples

```console
$ ws daemon run
$ ws daemon run --origin transient
```
