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

It uses AWS libraries to do all its work, and doesn't require any AWS CLI installation.

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

## License

`ec2tail` is licensed under the GNU General Public License v3.0 — see [`LICENSE`](LICENSE).

The compiled binary statically links third-party Go modules whose licenses (Apache-2.0,
BSD, and MIT — including `github.com/mmmorris1975/ssm-session-client`, © 2020 Mike Morris)
require their notices to be preserved. Those notices are reproduced in
[`THIRD_PARTY_LICENSES.md`](THIRD_PARTY_LICENSES.md).
