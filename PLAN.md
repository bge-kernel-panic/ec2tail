# ec2tail — design & implementation plan

A small Go CLI that tails plaintext log files from a set of EC2 instances (selected
by tag) and interleaves their lines into one prefixed, color-coded stream — the
"stern for EC2" that doesn't otherwise exist. No SSH; uses AWS SSM Session Manager.

## Core decisions (settled)

| Topic | Decision |
|---|---|
| Language / runtime | **Go** — single static binary, fast startup (no Python interpreter) |
| Binary name | `ec2tail` |
| Module path | `github.com/bge-kernel-panic/ec2tail` |
| Flag parsing | stdlib `flag` (tiny surface; no cobra/pflag) |
| AWS SDK | `aws-sdk-go-v2` (`config`, `ec2`, `ssm`) |
| SSM transport | **`github.com/mmmorris1975/ssm-session-client`**, used at the `datachannel.SsmDataChannel` level — **not** `ShellSession` |
| Local PTY | **None.** Eliminated — we read the data channel bytes directly. (The *remote* pty, allocated by the interactive shell session, is what gives line-buffered `tail` output.) |
| `session-manager-plugin` binary | **Not needed** — the protocol is handled in-process by the library |
| `aws` CLI | **Not needed** — `StartSession` is called via the SDK inside the library |

## Why this transport

`ssm-session-client` is a native-Go implementation of the SSM session data plane that
imports AWS's own `github.com/aws/session-manager-plugin` protocol packages (so it reuses
battle-tested framing, not a hand-roll) over `gorilla/websocket`, on `aws-sdk-go-v2`, MIT
licensed.

`ShellSession()` itself is unusable for us — it hardcodes `os.Stdin`/`os.Stdout` and puts
the local terminal in raw mode (N of those would fight over one terminal). One level down,
`datachannel.SsmDataChannel` is exported and is a plain `io.ReadWriter`. We drive it directly.

### Verified against source (master, June 2026)

- `c.Open(cfg, &ssm.StartSessionInput{Target})` → calls StartSession API, dials websocket,
  sends auth, spawns its own `processOutboundQueue` retransmit goroutine.
- `Read()` internally pumps the protocol (ACK/PausePublication) and returns only clean
  output-stream bytes — proven by `ShellSession` being nothing but `io.Copy(os.Stdout, c)`.
  **Our read loop is the pump; no extra plumbing.**
- `WaitForHandshakeComplete` is **port-forwarding only**; shell sessions never call it.
- `Write()` wraps payload in `InputStreamData` — our command injection goes straight in.
- `TerminateSession()` sends the proper protocol FIN/terminate over the websocket.
  `Close()` closes only the websocket (so order is **TerminateSession then Close**).
- `SetTerminalSize(rows, cols)` is on the `datachannel.DataChannel` interface (platform-neutral).
- **`datachannel` package is 100% platform-neutral**: no build tags, no `x/sys/unix`,
  no `syscall`, no ioctl — pure stdlib + aws-sdk-go-v2 + uuid + gorilla/websocket.

## Behavior

### Instance discovery
- `--tag key=value`, **repeatable**, **AND**-combined → `ec2:DescribeInstances` filters
  (`Name=tag:<key>,Values=<value>` per tag).
- Always restrict to `instance-state-name=running`.
- AWS region/profile/credentials come **only from the environment** (no `--profile`/`--region`
  flags in v1).
- Zero matches → clear error, non-zero exit.
- On match: **print the discovered instance count to stderr before connecting** (gives a
  Ctrl-C window). No confirmation prompt — connect to all (typical N ≤ ~10; Unix-philosophy:
  do what was asked).

### Prefix / identity
- Prefix each line with the instance's **`Name` tag**, falling back to **instance ID** when
  absent.
- Stable per-host color (hash name → palette), **only when stdout is a TTY**, respecting
  **`NO_COLOR`**. Prefix padded to align across hosts: `name │ <line>`.

### What we tail
- Positional arg = file path(s)/globs, **quoted** so the local shell doesn't expand them —
  they must travel to the remote. Passed to remote `tail`.
- Remote command (sent via the data channel):
  ```
  echo <MARKER>; exec tail -n 10 -f <globs> 2>&1
  ```
  - `exec` replaces the shell with `tail` → no prompt redraws, clean exit closes the session.
  - `2>&1` folds remote `tail` errors (e.g. "cannot open") inline with the host prefix.
  - Backlog hardcoded to `-n 10`. `-f` (not `-F`) — short-lived sessions, log rotation
    out of scope. Globs expand once at connect; new-files-later not handled (out of scope).
- Plain files only. **No** journald/docker/`--cmd` escape hatch (out of scope).

### Output cleaning (marker protocol)
- After connect, send the command above, then **suppress all received bytes until a line
  that *exactly equals* `<MARKER>`** (trimmed). This swallows the plugin banner, the shell
  prompt, and the echoed command line in one robust move — independent of prompt format.
  - The marker text appears twice (in the echoed input line, and as `echo`'s output); the
    **exact-line-equality** test stops on the echo *output*, not the echoed command.
  - `<MARKER>` is a distinctive per-session random token (e.g. `__EC2TAIL_<rand>__`) so it
    cannot collide with log content.
- After the marker: every subsequent byte is real output. Accumulate until `\n`, trim a
  trailing `\r` (remote pty is cooked → emits `\r\n`), prefix, emit.

### Concurrency & output serialization
- One goroutine per instance, all fanned out at once (no cap; N is small).
- Each goroutine sends **complete lines** over a channel to a **single writer goroutine**
  that owns stdout → no mid-line interleaving, no stdout mutex tangle.
- Right after `Open`, call `SetTerminalSize(45, 132)` once (defensive: in case the remote
  pty gates output on an initial size; cheap insurance). No resize loop.

### Failure handling (Unix-y, independent sessions)
- A session that fails to establish → one-line error to **stderr** prefixed with the host
  (`name ✗ failed to start session: <reason>`); **keep streaming the rest**. Never abort the
  whole run for one bad box.
- Connected-then-`tail`-errored (file missing) → surfaced as that host's stream line, keep going.
- Mid-session death → `name ✗ session ended`; **no auto-reconnect** (out of scope).
- Exit code reflects whether *any* session ran.

### Cleanup (do not leave orphaned SSM sessions)
- **Layer 1 (native, primary):** on SIGINT/SIGTERM (and on normal EOF), for every channel
  call `c.TerminateSession()` then `c.Close()`. In-process, so no external plugin to SIGKILL
  and no grace-period dance — far more reliable than the plugin-subprocess approach.
- **Layer 2 (belt-and-suspenders sweep):** `SsmDataChannel` does not expose `SessionId`, so
  on shutdown call `ssm:DescribeSessions(State=Active)`, filter to our target instances, and
  `ssm:TerminateSession` any stragglers. Also reaps orphans left by a previously SIGKILLed run.
  - Note: only a `SIGKILL` of `ec2tail` itself is unrecoverable (no handler can run) — accepted.

## Cross-platform

- **Mac + Linux: required and free** — the `datachannel` package is platform-neutral.
- **Windows: desired but strictly optional.** Implement it **only if it adds little code**;
  do not let it drive complexity. The local-pty problem (ConPTY/`go-pty`) is already gone, so
  Windows should be nearly free. The only platform-specific bits in our code:
  - Signals: `os.Interrupt` works everywhere; guard unix-only `SIGTERM` behind a build tag
    (or rely on `os.Interrupt`).
  - TTY/color detection: `golang.org/x/term.IsTerminal` is cross-platform.
  - Terminal size: hardcoded → no ioctl/`getWinSize` needed.
  If Windows ever requires a `//go:build` split or meaningful extra code, **drop it** and ship
  Mac/Linux only.

## CLI shape

```
ec2tail --tag app=web --tag env=prod '/var/log/app/*.log'
```
- `--tag key=value` (repeatable, required: at least one)
- positional: one or more file paths/globs (required)
- `--help` / usage via stdlib `flag`

## Dependencies

- `github.com/mmmorris1975/ssm-session-client/datachannel`
- `github.com/aws/aws-sdk-go-v2/{config,service/ec2,service/ssm,aws}`
- `golang.org/x/term` (TTY detection)
- (transitively: gorilla/websocket, google/uuid, aws session-manager-plugin protocol pkgs)

## Proposed file layout

```
ec2tail/
  go.mod
  main.go            # flag parsing, env config, orchestration, signal handling, writer goroutine
  discover.go        # tag → DescribeInstances → []instance{id, name}
  session.go         # per-instance: Open, SetTerminalSize, send command, read+marker+prefix loop, teardown
  cleanup.go         # Layer-2 DescribeSessions sweep
  PLAN.md
```
(Exact split is a guideline; small enough it could collapse into fewer files.)

## Open risks / accepted limitations

- `SetTerminalSize` "required vs nice" is unconfirmed — neutralized by always sending it once.
- `ssm-session-client` is single-maintainer (MIT, recently active). Mitigation: MIT lets us
  vendor/fork if it goes stale.
- `gorilla/websocket v1.4.2` is old but stable; pulled in transitively.
- Out of scope (by decision): log rotation (`-F`), journald/docker, new-files-after-connect,
  auto-reconnect, `--profile`/`--region`, large-N confirmation prompt.
