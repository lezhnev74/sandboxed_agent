# sandboxed_agent

`sandboxed_agent` runs a coding-agent harness (Claude Code or OpenCode) once, headless, with a prompt, in a Docker box
scoped to the current directory. Ralph loops live in your own script: it calls `sandboxed_agent run` over and over and
decides from the exit code (or `--json`) what comes next.

```bash
cd ~/Code/foo
sandboxed_agent run claude -m opus -f task.md --json   # bundle: ./.sandboxed_agent
```

- **Bare**: nothing from the host (`~/.claude`, `~/.config/opencode`) and nothing from the repo's agent files
  (`CLAUDE.md`, `AGENTS.md`, `.claude/`, `.mcp.json`) is loaded. You shape the whole context: prompt, system prompt
  addition, MCP servers, skills, subagents, extra dirs.
- **Max access**: all permissions granted up front. The only boundary is the container and its mounts.
- **Any model**: `-m` goes to the harness verbatim. Everything after `--` goes to the harness untouched.
- **Real development**: the agent can install toolchains (mise, `sudo apt`) and run services (its own dockerd).
- **Files stay yours**: workspace files end up owned by the host user; commits carry your git identity.
- **Log in once**: the login survives bare runs.

Linux only (built on Fedora with rootful Docker).

## How it works

```
Host
+-- sandboxed_agent-<repo>-<hash>        box: one per workspace path, long-lived
|     harnesses via `docker exec`, mise toolchains, docker CLI, sudo
|     --network container:<dind>  -> inner services appear on localhost
+-- sandboxed_agent-<repo>-<hash>-dind   docker:dind --privileged, private dockerd
```

| Part           | What it does                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
|----------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Image          | `sandboxed_agent:latest`, built on first use from a Dockerfile embedded in the binary: Debian trixie-slim, git, rg, jq, build-essential, python3, node, psql, docker CLI + compose, mise, tini, sudo, Claude Code (`latest` channel) and latest OpenCode under `/opt`. User `agent` has your uid/gid and passwordless sudo.                                                                                                                                                                                                                                           |
| Box            | Created by the first `sandboxed_agent` call in a directory, reused later. 4 CPUs, 8 GB, 4096 pids. Mounts the workspace at the same path. Several runs, shells and TUIs can exec into it at once.                                                                                                                                                                                                                                                                                                                                                                        |
| dind sidecar   | Private dockerd per box (8 GB). The box shares its network namespace, so `localhost:5432` works; `DOCKER_HOST` and `TESTCONTAINERS_HOST_OVERRIDE=localhost` are set. Workspace and `--dir`s are mounted into dind at the same paths, so bind mounts work.                                                                                                                                                                                                                                                                  |
| Bare config    | Each run gets a fresh harness config dir under `cfg/<repo-key>/<run-id>/` with a copy of the bundle (`--ctx`). Claude: fresh `CLAUDE_CONFIG_DIR`, `--setting-sources user`, `--strict-mcp-config`, `CLAUDE_CODE_DISABLE_CLAUDE_MDS/_AUTO_MEMORY/_BUNDLED_SKILLS`, `--dangerously-skip-permissions`. OpenCode: fresh `OPENCODE_CONFIG_DIR`, generated config in `OPENCODE_CONFIG_CONTENT` with every permission `allow`, project config and `.claude`/external skills disabled. Repo agent files stay visible on disk; only the harness switches keep them out. |
| Credentials    | The Claude login is copied into each run's config and copied back afterwards only if the run's token expires later than the stored one (under an `flock`), so rotated refresh tokens never log other runs out. OpenCode's `auth.json` lives in a dir mounted into every box.                                                                                                                                                                                                                                                                                   |
| Toolchains     | Before the harness starts, the box runs `mise install` for the repo's `mise.toml`, `.mise.toml`, `.tool-versions` or `go.mod` (else a bundle `mise.toml`). Toolchains and caches live in shared volumes (`sandboxed_agent-mise`, `sandboxed_agent-cache`, `sandboxed_agent-gomod`), so they download once per machine and survive `sandboxed_agent down`.                                                                                                                                                                                                                                     |
| Services       | Not managed by the tool: the agent starts what it needs (`docker compose up`, testcontainers) on its own dockerd. After a run, root-owned workspace files are chowned back to you. |
| Idle watchdog  | The box stops after 60 min with nothing exec'd into it (`SANDBOXED_AGENT_IDLE_TIMEOUT` changes this for new boxes). While it runs, the box rewrites a heartbeat file in the volume it shares with its sidecar; the sidecar stops its dockerd once the heartbeat goes stale (about a minute), so it follows the box however it stopped. The next `sandboxed_agent` call starts both again. |
| Workspace lock | One `run` per workspace. A second one fails fast. `shell` and the TUIs don't take it.                                                                                                                                                                                                                                                                                                                                                                                                                                      |

Never mounted: host `~/.claude`, `~/.claude.json`, `~/.config/opencode`, `~/.ssh`, cloud credentials, the host docker
socket. No `git push` from the box: the agent commits, you push.

## Requirements

- Linux with rootful Docker (the user can run `docker` without sudo).
- Go 1.26+ to build.
- For Claude: a Claude subscription login (`sandboxed_agent login`). For OpenCode: `sandboxed_agent login opencode` or provider keys in
  `~/.config/sandboxed_agent/env`.

## Install

```bash
make install     # builds bin/sandboxed_agent and installs it to ~/.local/bin/sandboxed_agent
make build       # only builds bin/sandboxed_agent
```

The image is built on first use (takes a few minutes).

## Quick start

```bash
sandboxed_agent login                  # claude: open the printed URL, sign in, paste the code back
sandboxed_agent login opencode         # or: put OPENROUTER_API_KEY=... in ~/.config/sandboxed_agent/env

cd ~/Code/foo
sandboxed_agent run claude -m sonnet -p "fix the failing test in pkg/x"
```

With a prompt file, a system prompt, a JSON result and the raw log kept:

```bash
cat > fix.md <<'EOF'
Read TODO.md. Pick the first unchecked item, implement it with tests,
check it off and commit. When every item is checked, create .done.
EOF

sandboxed_agent run claude -m opus -f fix.md --system ~/rules.md --json --log /tmp/fix.jsonl
```

More:

```bash
sandboxed_agent run opencode -m openrouter/qwen/qwen3-coder --ctx ~/ctx/task -f task.md
sandboxed_agent run claude -m sonnet -f task.md -- --effort high   # args after -- go to the harness
cat p.md | sandboxed_agent run claude -f -
sandboxed_agent run claude -f plan.md && sandboxed_agent run claude -f build.md   # chain on exit code
```

## Commands

Full flags: [CLI reference](#cli-reference) (generated from the code).

| Command                                                                | Does                                                                                                   |
|------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------|
| `sandboxed_agent run HARNESS [flags] [-- args]`                        | Run the harness once. See [Run result](#run-result).                                                   |
| `sandboxed_agent login [claude\|opencode]`                             | First-time auth (default claude).                                                                      |
| `sandboxed_agent claude` / `sandboxed_agent opencode [-m M] [-- args]` | Interactive TUI in the box with a fresh bare config.                                                   |
| `sandboxed_agent shell`                                                | `bash -l` in this repo's box.                                                                          |
| `sandboxed_agent down [--all]`                                         | Remove the box and sidecar (`--all`: every sandboxed_agent box). Keeps the dind and toolchain volumes. |
| `sandboxed_agent gc [--older D]`                                       | Prune cfg dirs left by crashed runs and dind volumes of removed boxes.                                 |
| `sandboxed_agent dockerfile DIR`                                       | Write the image's Dockerfile and helper scripts into DIR, to extend (see [Custom image](#custom-image)). |
| `sandboxed_agent --rebuild`                                            | Rebuild the image with the latest harness versions.                                                    |

### Prompt, context and output flags

| Flag                                                          | Meaning                                                                                                 |
|---------------------------------------------------------------|---------------------------------------------------------------------------------------------------------|
| `-m, --model MODEL`                                           | Passed verbatim. OpenCode `provider/model#variant` is split into `-m provider/model --variant variant`. |
| `-p, --prompt TEXT`                                           | Prompt text.                                                                                            |
| `-f, --file FILE`                                             | Prompt file. `-f -` reads stdin.                                                                        |
| `--system FILE`                                               | System prompt addition. Claude: `--append-system-prompt-file`; OpenCode: `instructions`.                |
| `--ctx DIR`                                                   | Context bundle (see below). Default `./.sandboxed_agent`.                                               |
| `--timeout D`                                                 | Kill a hung harness after D (default `1h`, `0` never). Exit 3.                                          |
| `--json`                                                      | Print one JSON object instead of the final message.                                                     |
| `--log FILE`                                                  | Write the harness's raw JSON log to FILE (created or truncated; its dir must exist).                    |

### Run modes

| Flag               | Effect                                                                                                                                                                                         |
|--------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--dir PATH`       | Extra read-write dir at the same path in box and dind (repeatable). Claude gets `--add-dir`. A box created with other dirs is refused (`down` it first). |
| `--fresh`          | Throwaway box, sidecar and dind volume, removed when the run ends.                                                                                                                             |
| `--image TAG`      | Run the box on an image you built, usually `FROM sandboxed_agent:latest` (the base is built on first use). A box created on another image is refused (`down` it first). |
| `--dockerfile FILE` | Build FILE once (its dir is the context) as `sandboxed_agent-custom:<hash>` and run the box on it. Exclusive with `--image`. See [Custom image](#custom-image). |
| `-e, --env K=V`    | Extra env for the harness (repeatable).                                                                                                                                                        |
| `--docker on\|off` | dind sidecar (default `on`). `off`: no dockerd.                                                                                                                                                |

## Custom image

```bash
sandboxed_agent dockerfile ~/img            # Dockerfile, sandboxed_agent-exec, sandboxed_agent-idle
$EDITOR ~/img/Dockerfile                    # e.g. add apt packages
sandboxed_agent run claude --dockerfile ~/img/Dockerfile -p "..."
```

- The image is tagged `sandboxed_agent-custom:<16 hex of sha256(Dockerfile)>` and built only when that tag is missing,
  with `UID`/`GID` build args like the default image. Any edit to the Dockerfile builds a new image; a change only to
  files it `COPY`s does not (touch the Dockerfile, or `docker rmi` the tag).
- The box records the tag, so after an edit `run` refuses the old box: `sandboxed_agent down` first.
- The dump is a full copy: it does not follow later changes of the embedded Dockerfile. To only add layers, write
  `FROM sandboxed_agent:latest` instead; the base is built before the Dockerfile.
- Harness versions are fixed at build time; `--rebuild` does not touch custom images.

## Run result

The outcome comes from the harness's final JSON event plus its exit code. The final event wins: a clean result with a
non-zero exit still fails, and a rate limit is a rate limit whatever the exit code.

| Exit | Outcome      | When                                                                             |
|------|--------------|----------------------------------------------------------------------------------|
| 0    | `ok`         | a clean final result and exit 0                                                  |
| 1    | -            | the tool itself failed (setup, box, mise, bad flags); the error is on stderr     |
| 2    | `fail`       | `is_error`, no result event, or a non-zero harness exit                          |
| 3    | `timeout`    | `--timeout` killed the harness (process group: TERM, KILL after 10 s)            |
| 4    | `rate-limit` | a 429, a rate-limit event or a usage-limit message                               |
| 5    | `overloaded` | a 5xx or an overloaded message                                                   |
| 130  | -            | Ctrl-C or SIGTERM; the harness's process group is killed                         |

- **stdout**: the final message, or with `--json` one object (also for exits 2-5, never for 1 or 130):

  ```json
  {"outcome":"rate-limit","detail":"api 429","final_text":"...","exit_code":1,
   "cost_usd":0.12,"in_tokens":48213,"out_tokens":950,"reset_at":"2026-09-29T17:00:00Z",
   "harness":"claude","version":"2.1.3","model":"opus","duration_s":84.2,"run_id":"20260929-141500-a1b2"}
  ```

  `exit_code` is the harness's own; `reset_at` (UTC) is there only when the limit said when it resets; `version` is
  asked from the box only with `--json`.
- **stderr**: one progress line per tool call or message (`Bash: go test ./...`), the harness's stderr, and for any
  outcome but `ok` a line like `sandboxed_agent: rate-limit: api 429, resets at 2026-09-29T17:00:00Z`.
- **`--log FILE`**: the raw harness JSONL. Without it the log lives in the run's cfg dir and is removed with it.

## Calling from a ralph script

A loop is a script around `run`: iterations, stop conditions, retries and logs are yours.

```bash
#!/usr/bin/env bash
# ralph.sh: run the prompt until the agent creates .done, at most 50 times.
mkdir -p logs
for i in $(seq 1 50); do
  [ -e .done ] && exit 0
  sandboxed_agent run claude -m opus -f fix.md --json --log "logs/$i.jsonl" > "logs/$i.json"
  code=$?
  case $code in
    0 | 2 | 3) ;;                                       # ok, failed, timed out: go on
    4) reset=$(jq -r '.reset_at // empty' "logs/$i.json")   # rate or usage limit
       wait=900; [ -n "$reset" ] && wait=$(( $(date -d "$reset" +%s) - $(date +%s) ))
       sleep $(( wait > 0 ? wait : 60 )) ;;
    5) sleep 120 ;;                                     # overloaded
    *) exit "$code" ;;                                  # the tool failed, or Ctrl-C
  esac
done
exit 10
```

## Context bundle (`--ctx DIR`)

Defaults to `./.sandboxed_agent` in the current directory; a missing dir is an empty bundle. Every file is optional.
The prompt (`-p`/`-f`, required) and the system prompt (`--system`) are flags, never bundle files.

| File                                    | Claude Code                                                         | OpenCode                                                   |
|-----------------------------------------|---------------------------------------------------------------------|------------------------------------------------------------|
| `mcp.json` (Claude `mcpServers` format) | `--strict-mcp-config --mcp-config`                                  | converted to the `mcp` block                               |
| `skills/<name>/SKILL.md`                | copied into the config dir                                          | copied into the config dir                                 |
| `agents/*.md`                           | copied into the config dir                                          | `mode: subagent` agents (Claude's `tools`/`model` dropped) |
| `providers.json`                        | -                                                                   | the `provider` block                                       |
| `mise.toml`                             | toolchains, if the repo has no mise config                          | same                                                       |

The prompt, the system prompt and the whole bundle are copied into the run's cfg dir when it starts.

## Environment variables

| Variable                           | Where | Meaning                                                                             |
|------------------------------------|-------|-------------------------------------------------------------------------------------|
| `SANDBOXED_AGENT_IDLE_TIMEOUT`     | host  | Go duration (e.g. `30m`), idle timeout for boxes created from then on (default 60m) |
| `XDG_DATA_HOME`, `XDG_CONFIG_HOME` | host  | move the state and config roots                                                     |

The harness gets no `SANDBOXED_AGENT_*` vars; pass what it should see with `-e` (e.g. `-e ITER=$i`). Provider API keys go
in `~/.config/sandboxed_agent/env` (`K=V` lines, passed as `--env-file`).

## Files

`~/.config/sandboxed_agent/` (`$XDG_CONFIG_HOME/sandboxed_agent`):

| Path  | Content                                   |
|-------|-------------------------------------------|
| `env` | provider keys, `--env-file` for every box |

`~/.local/share/sandboxed_agent/` (`$XDG_DATA_HOME/sandboxed_agent`):

| Path                       | Content                                                                                                    |
|----------------------------|------------------------------------------------------------------------------------------------------------|
| `locks/<repo-key>.lock`    | workspace lock                                                                                             |
| `cfg/<repo-key>/`          | mounted into the box: generated `gitconfig`                                                                |
| `cfg/<repo-key>/<run-id>/` | one run's harness config, bundle copy, `harness.pgid`, raw log without `--log`; removed when the run ends |
| `home/`                    | box `/home/agent`                                                                                          |
| `claude-auth/`             | stored Claude login (`.credentials.json`) and its lock                                                     |
| `opencode/`                | OpenCode data (`auth.json`, sessions)                                                                      |

`<repo-key>` is `<basename>-<8 hex of sha256(abs path)>`; `<run-id>` is `YYYYMMDD-HHMMSS-xxxx`.

Docker objects: containers `sandboxed_agent-<key>`, `sandboxed_agent-<key>-dind`; volumes `sandboxed_agent-mise`,
`sandboxed_agent-cache`, `sandboxed_agent-gomod`, `sandboxed_agent-<key>-sock` (docker socket and heartbeat),
`sandboxed_agent-dind-<key>`.

## Upgrading

- **Image**: it carries a hash of the embedded Dockerfile and scripts (`sandboxed_agent.assets` label). An image built by
  another `sandboxed_agent` version is rebuilt on next use; a running box keeps its old image until `sandboxed_agent
  down`. Harness versions only
  change on `sandboxed_agent --rebuild` (auto-update is off); `run --json` reports them.
- **Box**: mounts, entrypoint and options are fixed at creation and hashed (`sandboxed_agent.mounts` label). When a new
  `sandboxed_agent` version or new `--dir`/`--image` changes that hash, `run` refuses the box. Run
  `sandboxed_agent down` once it is free. Also needed after creating
  `~/.config/sandboxed_agent/env`.
- **From the loop versions**: `loop`, `ps`, `logs`, `attach`, `stop`, `--hardened`, `--system`/`--mcp`/`--skills`/
  `--agents` and bundle `dirs.txt` are gone; `--image` takes a tag you built; `run` exits with its outcome's code
  (above), no longer the harness's. Sidecars are recreated (no mirror, a heartbeat), so existing boxes are refused once:
  `sandboxed_agent down --all`. Leftovers to delete by hand: `~/.local/share/sandboxed_agent/runs/`,
  `~/.config/sandboxed_agent/images/`, and `docker rm -f sandboxed_agent-mirror; docker volume rm sandboxed_agent-mirror;
  docker network rm sandboxed_agent`.
- `sandboxed_agent down` keeps toolchain volumes and the dind volume (inner images); `sandboxed_agent gc` removes dind
  volumes of boxes that no longer exist.

## Known limitations

- The dind sidecar is `--privileged`: an agent that tries can escape to host root. Accepted risk for own prompts and
  repos.
- No egress allowlisting (`--locked`), no Podman, no non-privileged nested daemon yet.
- OpenCode is the 1.x release: no `--standalone`, no `#variant` syntax (split into `--variant`). Its built-in
  `customize-opencode` skill is always offered. OpenCode can't use a Claude Pro/Max login; Claude models need an API key
  there.
- A workspace under `/tmp/claude-<uid>/` breaks Claude in the box (Docker creates the parent as root).
- The Claude bare-run env vars are undocumented; they were checked against specific harness versions only.
- Several end-to-end checks through a real model are still pending: the bare-context check, concurrent logins, OpenCode
  login, `plan && build` chaining, TUIs, and two parallel runs with postgres. The same code paths are covered by unit
  and integration tests.

## Development

| Command                   | Use                                                                                                                                                       |
|---------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------|
| `make check`              | The definition of done: tidy, gates, fmt, lint, build, test (race + shuffle + coverage), cover, crap. Ends with `CHECK OK` or `FAIL: <gate>`. No network. |
| `make test`               | Fast inner loop (no race, no coverage).                                                                                                                   |
| `make itest`              | `-tags integration` tests against real Docker (uses its own image tag and names).                                                                         |
| `make docs`               | Regenerate the [CLI reference](#cli-reference) in this README (a golden test in `cmd/sandboxed_agent`).                                                   |
| `make fmt`, `make tidy`   | Fix formatting / `go.mod`.                                                                                                                                |
| `make flaky`, `make vuln` | 10 shuffled runs; govulncheck (network).                                                                                                                  |

Tools are pinned in `go.mod` and run via `go tool`. Limits: cyclomatic 7, CRAP 7, cognitive 20, 60 lines / 40 statements
per function, coverage file 60 / package 75 / total 80. Gate files (`Makefile`, `.golangci.yml`, `.testcoverage.yml`,
`tools/**`, `internal/archtest/**`) are hash-guarded in `.gates.sha256`.

Layout: pure core packages (stdlib only, no `os/exec`) vs adapters (side effects in `*_exec.go`); only
`cmd/sandboxed_agent` and `driver` compose adapters.

| Package                          | Role                                                                                     |
|----------------------------------|------------------------------------------------------------------------------------------|
| `cmd/sandboxed_agent`            | CLI (cobra), composition root                                                            |
| `internal/paths`                 | state dirs, repo key, run ids (core)                                                     |
| `internal/clock`                 | injected clock (core)                                                                    |
| `internal/events`                | harness JSONL parsing, outcome classification, exit codes, JSON report, progress (core) |
| `internal/harness`               | adapter table (`adapters.json`), argv/env/config per harness (core)                      |
| `internal/bundle`, `bundle/plan` | resolve and copy the context bundle (`plan` is core)                                     |
| `internal/runs`, `runs/spec`     | workspace lock, stale run cfg dirs; the run's resolved options (`spec` is core)          |
| `internal/image`                 | embedded Dockerfile and scripts, build/rebuild                                           |
| `internal/box`                   | box, sidecar and its heartbeat, mounts, exec, gc                                         |
| `internal/auth`                  | Claude credential copy-in/back                                                           |
| `internal/gitx`                  | host git identity for the box                                                            |
| `internal/driver`                | runs the harness once, toolchains                                                        |
| `internal/archtest`              | architecture rules as tests                                                              |

## CLI reference

<!-- BEGIN REFERENCE (generated by `make docs`, do not edit) -->

### sandboxed_agent

```text
sandboxed_agent runs a coding-agent harness (claude, opencode) once, headless, in a Docker
box scoped to the current directory. Loops live in the calling script.

Usage:
  sandboxed_agent [flags]
  sandboxed_agent [command]

Available Commands:
  claude      Interactive claude TUI in this repo's box
  dockerfile  Write the image's Dockerfile and helper scripts into DIR, to extend it
  down        Remove this repo's box and its sidecar
  gc          Prune cfg dirs of crashed runs and dind volumes of removed boxes
  login       First-time auth for a harness (default claude)
  opencode    Interactive opencode TUI in this repo's box
  run         Run the harness once
  shell       Open bash in this repo's box (created with defaults if absent)

Flags:
      --rebuild   rebuild the image with the latest harness versions

Use "sandboxed_agent [command] --help" for more information about a command.
```

### sandboxed_agent claude

```text
Start the interactive claude TUI in this repo's box with a fresh bare config
(as runs get, all permissions granted), removed when it exits.

Usage:
  sandboxed_agent claude [-- harness args...] [flags]

Flags:
  -m, --model MODEL   MODEL id, passed to the harness verbatim
```

### sandboxed_agent dockerfile

```text
Write the Dockerfile the default image is built from, and the scripts it
copies, into DIR (created; existing files are never overwritten). Edit the
Dockerfile, then `run --dockerfile DIR/Dockerfile`: the image is built once, with
DIR as the build context, and rebuilt only when the Dockerfile changes.

Usage:
  sandboxed_agent dockerfile DIR
```

### sandboxed_agent down

```text
Remove this repo's box and its sidecar

Usage:
  sandboxed_agent down [flags]

Flags:
      --all   remove every sandboxed_agent box
```

### sandboxed_agent gc

```text
Prune, in every repo not running a run now, the run cfg dirs older than --older
(a run removes its own when it ends; crashed runs leave theirs). Then remove the
dind volumes (sandboxed_agent-dind-*) of boxes that no longer exist.

Usage:
  sandboxed_agent gc [flags]

Flags:
      --older D   prune only what is older than D, e.g. 720h (0: everything prunable)
```

### sandboxed_agent login

```text
Log a harness in once; every later run reuses the login.
claude: runs `claude auth login` in this repo's box: open the URL it prints,
sign in and paste the code back. The credential is stored in
~/.local/share/sandboxed_agent/claude-auth/ and copied into each run.
opencode: runs `opencode auth login` in this repo's box. Its auth.json lives in
~/.local/share/sandboxed_agent/opencode/, mounted into every box. Provider API keys can
instead go in ~/.config/sandboxed_agent/env (K=V lines), passed to the box as --env-file.

Usage:
  sandboxed_agent login [claude|opencode]
```

### sandboxed_agent opencode

```text
Start the interactive opencode TUI in this repo's box with a fresh bare config
(as runs get, all permissions granted), removed when it exits.

Usage:
  sandboxed_agent opencode [-- harness args...] [flags]

Flags:
  -m, --model MODEL   MODEL id, passed to the harness verbatim
```

### sandboxed_agent run

```text
Run the harness once, headless, with a fresh context. The final message goes to
stdout (--json: one JSON object instead), progress and the harness's stderr to
stderr. Exit codes: 0 ok, 1 the tool itself failed, 2 the harness failed,
3 --timeout, 4 rate or usage limit, 5 overloaded, 130 interrupted.

Usage:
  sandboxed_agent run HARNESS [flags] [-- harness args...]

Flags:
      --ctx DIR           context bundle DIR (mcp.json, skills/, agents/, ...; default ./.sandboxed_agent)
      --dir PATH          extra read-write PATH, mounted at the same path (repeatable)
      --docker on|off     run a dockerd sidecar: on|off (default "on")
      --dockerfile FILE   build FILE (its dir is the context) once and run the box on it; start from `sandboxed_agent dockerfile`
  -e, --env K=V           extra env var K=V (repeatable)
  -f, --file FILE         prompt FILE; - reads stdin
      --fresh             throwaway box for this run
      --image TAG         run the box on image TAG (built by you, FROM sandboxed_agent:latest)
      --json              print the result as one JSON object instead of the final message
      --log FILE          write the harness's raw JSON log to FILE (its dir must exist)
  -m, --model MODEL       MODEL id, passed to the harness verbatim
  -p, --prompt TEXT       prompt TEXT
      --system FILE       system prompt FILE, appended to the harness's own
      --timeout D         kill a hung harness after D (0: never) (default 1h0m0s)
```

### sandboxed_agent shell

```text
Open bash in this repo's box (created with defaults if absent)

Usage:
  sandboxed_agent shell
```
<!-- END REFERENCE -->
