# mskill — Go-native replacement for `npx skills`

> **Fetch → Store → Link.** Sparse Git cache, fail-closed security, human-gated trust. Single static binary, no Node.

`npx skills` (`skills@1.5.23`) shallow-clones to `os.tmpdir()` and symlinks into agent dirs on every `add`. **mskill** preserves the same contract — `mskill get owner/repo/skill` — but replaces the Node cold-start and ephemeral clone with a persistent dot-folder sparse cache (`git clone --filter=blob:none --sparse --depth 1` → `fetch --depth 1 + reset --hard`), surfaces `SAFE/UNSAFE/UNKNOWN` inline on search, and blocks risky installs behind a password that agents cannot bypass.

## Install

```sh
# go
go install skill.sh/mskill@latest

# brew
brew install mskill

# curl (future install.sh)
curl -fsSL https://skills.sh/install.sh | sh
```

Requires Go ≥1.25 and Git ≥2.25 for sparse cone (falls back to full checkout on older Git).

## Quickstart

```sh
# search with web-parity filters (mirrors https://www.skills.sh/topic + /official)
mskill search --topic react --official

# preview without installing — Resolve → Cache only, no link
mskill get owner/repo/skill --show

# print additional files from cache (sanitized, must be inside skill)
mskill show owner/repo/skill --file README.md --file scripts/setup.sh

# install (Resolve → Cache → Link)
mskill get vercel-labs/agent-skills/vercel-optimize

# trust state (never prints hash)
mskill trust status
```

Output is compact TSV (no JSON) — `cut -f1` / `awk -F'\t'` parseable, ~40% smaller than JSON, color-aware (`NO_COLOR`, non-TTY disables color).

```sh
mskill search react --topic react --official --header | cut -f1,4,5
# slug<TAB>topic<TAB>official<TAB>SAFE|UNSAFE|UNKNOWN<TAB>installs<TAB>description
```

## CLI Surface

| Command | Alias | Purpose |
|---------|-------|---------|
| `mskill get <ref>` | `add`, `a` | Resolve → Cache → Link a skill |
| `mskill show <ref>` | `cat` | View cached skill files without installing |
| `mskill search [query]` | `find` | Search with `SAFE/UNSAFE/UNKNOWN` + topic/official filters |
| `mskill list` | `ls` | List installed skills |
| `mskill remove [skills]` | `rm` | Remove installed skills |
| `mskill update [skills]` | `upgrade` | Re-fetch + re-link to latest |
| `mskill cache <sub>` | — | `gc`, `path`, `clean` |
| `mskill trust <sub>` | — | `enable`, `disable`, `status`, `reset` |
| `mskill version` | — | Print version |

Global flags: `--config <path>`, `--cache-dir <path>`, `-y/--yes`, `--no-color`, `-h/--help`, `-v/--version`.

Key per-command flags:

- `get`: `-g/--global`, `-p/--project`, `-a/--agent <list|*>`, `-s/--skill <list|*>`, `--ref <branch|tag|sha>`, `--copy`, `--link-mode symlink|copy|auto`, `--show`, `--file <path>`, `--force`, `--full-depth`
- `show`: `--file <path>` (repeatable, default `SKILL.md`), `--list`, `--ref`, `--force`
- `search`: `[query]`, `--topic <topic>`, `--official`, `--owner <owner>`, `--limit 20`, `--header` (no `--json` by design)

See [docs/CLI.md](docs/CLI.md) for full reference and `cache-view` examples.

## Config & Layout

**Precedence:** `flag > env (MSKILL_*) > config.yaml > default` (Viper).

```
~/.mskill/                  0700
└── config.yaml             0600  # primary; viper WriteConfigAs 0600
~/.cache/mskill/            # $XDG_CACHE_HOME/mskill else ~/.cache/mskill
└── repos/github.com/<owner>/<repo>/<ref>--<hash8>/
    ├── .git/               # blobless, shallow, sparse
    ├── <skill-path>/...
    └── .mskill-meta.json
~/.agents/skills/<skill>   # global canonical (universal)
~/.claude/skills/<skill>   # per-agent global
./.agents/skills/<skill>   # project
```

Env overrides: `MSKILL_CACHE_DIR`, `MSKILL_DOT_DIR`, `MSKILL_CONFIG`, `SKILLS_API_URL`, `SKILLS_DOWNLOAD_URL`, `GH_HOST`, `GITHUB_TOKEN`/`GH_TOKEN` (private repos + `gh` fallback).

Full file reference: [docs/CONFIG.md](docs/CONFIG.md).

## Security

**`risky ⇒ password required`**, fail-closed.

- Audit via `GET https://add-skill.vercel.sh/audit?source=owner/repo&skills=slug` (3s timeout). `UNKNOWN` (timeout/non-200) is treated as `UNSAFE` for install, yellow for search.
- Non-interactive (no TTY, `CI`/`GITHUB_ACTIONS`/`AGENT`/`CLAUDECODE`/`CURSOR_AGENT`/`OPENCODE`, or `-y`) → install fails with `UNSAFE skill "X" requires human password. Re-run in TTY or mskill trust enable`.
- Interactive: if `security.passwordHash == ""` → create with `survey.Password` + `bcrypt` cost 12 → save 0600; else verify (3 retries). Official status does **not** auto-bypass.
- `mskill trust enable` is **TTY-gated** (`isInteractiveTTY() && !isAgentEnv()`). No env bypass for `trustEnabled`/`passwordHash`, no `--yes` bypass. `trustEnabled && passwordHash=="" ⇒ trust==false`. `trust status` never prints hash; `trust reset` requires TTY + typing `RESET`.

> Same-UID agents can still edit `~/.mskill/config.yaml` (chmod is not a sandbox). For stronger isolation use `chattr +i` or a future OS-keychain backend.

## mskill vs `npx skills`

|  | `npx skills` | `mskill` |
|--|--------------|----------|
| Runtime | Node + `simple-git` + `tar` (~400 ms) | Static Go binary, `exec git` only (~50 ms) |
| Cache | `mkdtemp("skills-") → clone --depth 1 → rm -rf` (re-clones multi-skill repos) | Persistent sparse cache, `fetch --depth 1 + reset --hard`, `sparse-checkout set --cone` per skill |
| Search | `find [query] --owner`, no topic/official, no SAFE | `search --topic --official --owner --limit` + `SAFE/UNSAFE/UNKNOWN` inline, TSV |
| Security | Advisory table only, `-y` bypasses confirm | Fail-closed: risky requires password; `trust enable` TTY-only, bcrypt cost 12 |
| Output | Prompt-heavy | Compact TSV for humans + agents (no JSON) |
| Lockfiles | Writes `skills-lock.json` v1 / `.skill-lock.json` v3 | Reads/writes same lockfiles for interop |
| Compat | — | Same shorthand (`owner/repo`, `github:`, `gitlab:`, `well-known`, `local`), same flags (`-g/-a/-s/-y/--copy`) |

Whitepaper (source of truth): [docs/whitepaper.md](docs/whitepaper.md).
