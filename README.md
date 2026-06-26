# ec2tail

Tail plaintext log files across a set of EC2 instances (selected by tag) and interleave
their lines into one prefixed, color-coded stream — **"[stern](https://github.com/stern/stern)
for EC2"**.

It connects over **AWS SSM Session Manager** using the SDK directly: **no SSH**, no
`session-manager-plugin` binary, and no `aws` CLI required.

```
ec2tail --tag app=web --tag env=prod '/var/log/app/*.log'
```
```
web-01 │ 2026-06-26T15:01:02 INFO  request handled in 12ms
web-02 │ 2026-06-26T15:01:02 INFO  request handled in  9ms
web-01 │ 2026-06-26T15:01:03 WARN  upstream slow
web-02 ✗ session ended
```

## Why

There is no `stern` for EC2. When the same service runs on a handful of tagged instances,
watching its logs usually means SSHing into each box in a separate terminal. `ec2tail`
fans out to all matching instances at once and folds their output into a single stream,
each line prefixed with its source.

Because it speaks the SSM data-channel protocol in-process, there is nothing to install on
your machine beyond the binary itself, and nothing to configure on the instances beyond the
SSM agent they already run.

## Install

Requires Go 1.26+.

```sh
go build -o ec2tail .
```

Drop the resulting `ec2tail` binary anywhere on your `PATH`.

## Usage

```
ec2tail --tag key=value [--tag key=value ...] '<glob>' ['<glob>' ...]
```

- `--tag key=value` — instance filter. **Repeatable** and **AND-combined**; at least one
  required. Values support AWS wildcards: `*` matches zero or more characters, `?` matches
  one. Quote them so your local shell does not expand them:
  - `--tag 'Name=web-*'` (prefix match)
  - `--tag 'Name=*staging*'` (substring match)
- **Positional arguments** — one or more remote file paths or globs to tail. Quote them too,
  so they travel to the remote instead of being expanded locally.

AWS **region, profile, and credentials are taken from the environment** (e.g. `AWS_PROFILE`,
`AWS_REGION`). There are no `--profile` / `--region` flags.

### Examples

```sh
# All running instances tagged app=web AND env=prod
ec2tail --tag app=web --tag env=prod '/var/log/app/*.log'

# Wildcard tag match, multiple globs
ec2tail --tag 'Name=web-*' '/var/log/app/*.log' '/var/log/nginx/error.log'
```

Press **Ctrl-C** to stop. `ec2tail` tears down every live session cleanly before exiting.

## How it works

- **Discovery** — `ec2:DescribeInstances` filtered by your tags and restricted to
  `instance-state-name=running`. Zero matches is a clear error with a non-zero exit. The
  matched instance count is printed before connecting, giving you a Ctrl-C window.
- **Identity** — each line is prefixed with the instance's `Name` tag, falling back to the
  instance ID. Colors are stable per host (only on a TTY, and suppressed when
  [`NO_COLOR`](https://no-color.org/) is set).
- **Tailing** — for each instance, a remote `tail -n 10 -f <globs> 2>&1` is run over the SSM
  data channel. Remote `tail` errors (e.g. a missing file) are folded inline with the host
  prefix. A per-session random marker swallows the connection banner and shell prompt so only
  real log output is shown.
- **Serialization** — one goroutine per instance feeds **complete lines** to a single writer
  goroutine that owns stdout, so lines never interleave mid-line across hosts.
- **Independent sessions** — a host that fails to connect or whose session dies produces a
  one-line `✗` error and the rest keep streaming; one bad box never aborts the run. There is
  no auto-reconnect.

### Cleanup

`ec2tail` does not leave orphaned SSM sessions behind:

1. **Primary** — on Ctrl-C (or normal EOF) every channel is terminated and closed in-process.
2. **Sweep** — on shutdown it calls `ssm:DescribeSessions` and terminates any stragglers for
   the targeted instances, which also reaps orphans left by a previously `SIGKILL`ed run.

Only a `SIGKILL` of `ec2tail` itself is unrecoverable, since no handler can run.

## Requirements

### IAM permissions

The credentials in your environment need:

- `ec2:DescribeInstances`
- `ssm:StartSession`
- `ssm:DescribeSessions`
- `ssm:TerminateSession`

### Instances

- The [SSM agent](https://docs.aws.amazon.com/systems-manager/latest/userguide/ssm-agent.html)
  must be running and the instance registered with Systems Manager (a suitable instance
  profile / role attached).
- `tail` must be available on the instance (it is, on any standard Linux).

## Debugging

Set `EC2TAIL_DEBUG` to any value to print trace output and surface the underlying
SSM/websocket protocol messages on stderr (normally suppressed to keep output clean).

```sh
EC2TAIL_DEBUG=1 ec2tail --tag app=web '/var/log/app/*.log'
```

## Limitations

By design, the following are out of scope:

- Log rotation (`tail -f`, not `-F`) and files created after connect.
- journald, Docker, or arbitrary remote commands — plaintext files only.
- Auto-reconnect on session death.
- `--profile` / `--region` flags (use the environment).
- Confirmation prompt for large instance counts — it connects to all matches.

## Platform support

macOS and Linux are supported. The SSM data-channel transport is platform-neutral, so
Windows is likely to work but is not a supported target.
