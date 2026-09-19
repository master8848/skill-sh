# mskill — Complete Documentation

> **Fetch → Store → Link.** Go-native replacement for `npx skills` (`skills@1.5.23`) with persistent sparse Git cache, fail-closed security, and human-gated trust. Single static binary, no Node.

**Module:** `skill.sh/mskill` · **Go:** 1.25.5 · **CLI framework:** `cobra`+`viper`+`survey`+`bcrypt` · **Git:** `exec git` only  
**Whitepaper (source of truth):** `docs/whitepaper.md:1` · **CLI help is normative:** `docs/CLI.md:1` · **Config reference:** `docs/CONFIG.md:1`

---

## Table of Contents

1. [Overview & Motivation](#1-overview--motivation)
2. [Installation](#2-installation)
3. [Quickstart](#3-quickstart)
4. [Architecture — The Three-Phase Loop](#4-architecture--the-three-phase-loop)
5. [Project Structure](#5-project-structure)
6. [Configuration & Filesystem Layout](#6-configuration--filesystem-layout)
7. [Phase 1 — Resolve](#7-phase-1--resolve)
8. [Phase 2 — Cache (Dot-Folder + Sparse Git)](#8-phase-2--cache-dot-folder--sparse-git)
9. [Phase 3 — Install / Link](#9-phase-3--install--link)
10. [Search — SAFE/UNSAFE/UNKNOWN + Topic/Official](#10-search--safeunsafeunknown--topicofficial)
11. [Security Model — Password-Gated Risky Installs](#11-security-model--password-gated-risky-installs)
12. [CLI Surface — Complete Reference](#12-cli-surface--complete-reference)
13. [API Contracts](#13-api-contracts)
14. [Failure Modes & Observability](#14-failure-modes--observability)
15. [mskill vs npx skills](#15-mskill-vs-npx-skills)
16. [Development, Testing & Release](#16-development-testing--release)

---

## 1. Overview & Motivation

`npx skills` installs agent skills by shallow-cloning to `os.tmpdir()` and symlinking into agent dirs on every `add`. It has three structural deficits addressed here (`docs/whitepaper.md:15`):

| Deficit | `npx skills` | `mskill` |
|---|---|---|
| **Runtime** | Node + `simple-git` + `tar` (~400 ms cold start) | Static Go binary, <50 ms (`main.go:1`, `go.mod:1`) |
| **Cache** | `mkdtemp → clone --depth 1 → rm -rf` (re-clones multi-skill repos) | Persistent `~/.cache/mskill` with `clone --filter=blob:none --sparse --depth 1` → `fetch --depth 1 + reset --hard` (`internal/cache/cache.go:243`) |
| **Security** | Advisory audit table only, `-y` bypasses confirm | Fail-closed: `UNSAFE/UNKNOWN → password required`, `trust enable` TTY-only, `bcrypt` cost 12 (`internal/security/security.go:110`) |

Core invariant (`docs/whitepaper.md:185`):

> **Resolve** never writes · **Cache** never contacts `skills.sh` · **Link** never contacts network

---

## 2. Installation

```sh
# go
go install skill.sh/mskill@latest

# brew (tap)
brew install mskill

# curl (future)
curl -fsSL https://skills.sh/install.sh | sh

# from source
just build          # → ./mskill  (justfile: build)
just vet            # go vet ./...
just test           # go test ./...
just install        # build + install to $GOBIN / $GOPATH/bin
```

**Requirements:** Go ≥1.25, Git ≥2.25 for `sparse-checkout --cone` (falls back to full checkout on older Git, `internal/cache/cache.go:542`).

**Cross-compile:** `goreleaser` builds `linux/darwin/windows × amd64/arm64` (`goreleaser.yaml:8`).

---

## 3. Quickstart

```sh
# search — TSV, mirrors /topic + /official
mskill search --topic react --official --header | cut -f1,4,5
# slug<TAB>topic<TAB>official<TAB>SAFE|UNSAFE|UNKNOWN<TAB>installs<TAB>description

mskill search react --topic react --official          # web-parity
mskill search --topic nextjs --official --owner vercel --limit 10

# preview without installing — Resolve → Cache only
mskill get owner/repo/skill --show
mskill show owner/repo/skill --file README.md --file scripts/setup.sh
mskill show owner/repo/skill --list

# install (Resolve → Cache → Link)
mskill get vercel-labs/agent-skills/vercel-optimize
mskill get owner/repo --skill s1 --skill s2 --agent claude-code --global --copy
mskill get owner/repo --skill "*"            # all skills from repo
mskill get owner/repo --list                 # discover without installing

# local path
mskill get ./my-skill --global
mskill show ./my-skill --file SKILL.md

# trust
mskill trust status
mskill trust enable   # TTY required
mskill trust disable
mskill trust reset    # type RESET

# cache
mskill cache path
mskill cache list --header | cut -f1,6       # source, installed flag
mskill cache gc --dry-run
mskill cache clean

# list / remove / update
mskill list --global --agent claude
mskill remove vercel-optimize --global
mskill update vercel-labs/agent-skills/vercel-optimize --force
```

Output is **compact TSV** (no JSON by design, `docs/CLI.md:42`, `internal/search/search.go:181`). Color-aware: disabled if `!isTTY` or `NO_COLOR` (`internal/search/search.go:117`).

---

## 4. Architecture — The Three-Phase Loop

```
User: mskill get user/repo/skill [--global] [--agent claude --copy] [--ref main]

┌──────────┐    ┌─────────┐    ┌──────────────┐
│  Resolve │───▶│  Cache  │───▶│ Install/Link │
│          │    │         │    │              │
│ skills.sh│    │ ~/.cache│    │ symlink/copy │
│ + audit  │    │ + sparse│    │ to agent dir │
│          │    │ git     │    │              │
└──────────┘    └─────────┘    └──────────────┘
      ▲               │               │
      └───────────────┴───────────────┘
               loop per skill
```

*Source: `docs/whitepaper.md:165`, `cmd/get.go:22`, `internal/cache/cache.go:243`, `internal/link/link.go:437`*

- One loop per requested skill (`cmd/get.go:116` iterates `raws`).
- `get --show` / `show` run **Resolve → Cache → cat** only, no Link (`cmd/get.go:261`, `cmd/show.go:64`).
- Failure is **fail-closed** for `UNSAFE/UNKNOWN` (`docs/whitepaper.md:187`, `internal/security/security.go:110`).

**Stack rationale (`docs/whitepaper.md:195`):**

| Need | Choice | Why |
|---|---|---|
| CLI | `spf13/cobra` (`cmd/root.go:7`) | Subcommands, flag groups, `PersistentPreRunE`, completions |
| Config | `spf13/viper` (`internal/config/config.go:88`) | `flag > env(MSKILL_*) > config.yaml > default` |
| Prompts | `AlecAivazis/survey/v2` (`cmd/get.go:11`) | `Input/Select/MultiSelect/Password` + TTY detection |
| Hashing | `golang.org/x/crypto/bcrypt` (`internal/security/security.go:13`) | Cost 12, no plaintext |
| HTTP | std `net/http` (`internal/api/api.go:98`) | 10s search, 3s audit (`internal/api/api.go:174`) |
| Git | `exec git` (`internal/git/git.go:15`) | `go-git` sparse/filter incomplete; `--filter=blob:none --sparse` for free |

---

## 5. Project Structure

```
.
├── main.go                        # entry → cmd.Execute()           (main.go:1)
├── go.mod / go.sum / justfile     # go 1.25.5, build/vet/test       (go.mod:1)
├── .goreleaser.yaml               # cross-compile + brew            (.goreleaser.yaml:1)
├── mskill                         # built binary (ignored)
├── skills-lock.json               # project lock v1 (skills-lock.json:1)
├── docs/
│   ├── whitepaper.md              # executable spec, 833 lines       (docs/whitepaper.md:1)
│   ├── CLI.md                     # CLI help reference               (docs/CLI.md:1)
│   ├── CONFIG.md                  # config & env reference           (docs/CONFIG.md:1)
│   └── DOCUMENTATION.md           # this file
├── cmd/
│   ├── root.go                    # PersistentPreRunE: ResolvePaths, EnsureDirs, InitViper (cmd/root.go:28)
│   ├── get.go                     # Resolve→Cache→Link [+ local/multi-skill/show/list] (cmd/get.go:22)
│   ├── show.go                    # Resolve→Cache→cat, front-matter strip (cmd/show.go:64)
│   ├── search.go                  # TSV + audit batching             (cmd/search.go:15)
│   ├── list.go                    # installed scan                   (cmd/list.go:12)
│   ├── remove.go                  # rm installed                     (cmd/remove.go:13)
│   ├── update.go                  # fetch --depth1 + re-link        (cmd/update.go:16)
│   ├── cache.go                   # gc/path/clean/list              (cmd/cache.go:15)
│   ├── trust.go                   # enable/disable/status/reset     (cmd/trust.go:10)
│   ├── version.go                 # version print                    (cmd/version.go:5)
│   └── cmd_test.go
├── internal/
│   ├── config/config.go           # ResolvePaths/EnsureDirs/InitViper/WriteConfig (config/config.go:22)
│   ├── resolve/resolve.go         # ParseSkillRef 10-step precedence (resolve/resolve.go:230)
│   ├── api/api.go                 # Search/Audit/DownloadMeta       (api/api.go:78)
│   ├── cache/cache.go             # Ensure/CloneOrFetch/GC/List     (cache/cache.go:243)
│   ├── git/git.go                 # Run(ctx,dir,args) with timeout  (git/git.go:15)
│   ├── link/link.go               # Install/CopyDirectory/CreateSymlink (link/link.go:437)
│   ├── search/search.go           # FilterByTopic/Official + Render (search/search.go:81)
│   ├── security/security.go       # RequirePassword/EnableTrust     (security/security.go:110)
│   └── e2e/e2e_test.go            # Fetch→Store→Link integration    (e2e/e2e_test.go:72)
└── .agents/skills/                # project-installed skills (universal)
```

---

## 6. Configuration & Filesystem Layout

### 6.1 Precedence

`flag > env (MSKILL_*) > config.yaml > default` — via `viper.AutomaticEnv` + `SetEnvPrefix("MSKILL")` (`internal/config/config.go:89`, `cmd/root.go:75`, `docs/CONFIG.md:3`).

### 6.2 Paths

Resolved by `config.ResolvePaths()` (`internal/config/config.go:22`):

```
~/.mskill/                  0700  (MSKILL_DOT_DIR override)
├── config.yaml             0600  primary (viper WriteConfigAs 0600, Chmod 0600)
└── trust.json              0600  optional split file

~/.cache/mskill/            0755  ($XDG_CACHE_HOME/mskill else ~/.cache/mskill, MSKILL_CACHE_DIR)
├── repos/github.com/<owner>/<repo>/<ref>--<hash8>/
│   ├── .git/               # blobless, shallow, sparse
│   ├── <skill-path>/...    # only sparsely checked out skill files
│   ├── .mskill-meta.json   # Meta (cache/cache.go:26)
│   └── .lock               # flock (30s timeout)
└── tmp/clone-XXXX/         # staging, then atomic rename

~/.agents/skills/<skill>   # global canonical (universal)
~/.claude/skills/<skill>   # per-agent global
./.agents/skills/<skill>   # project
~/.agents/.skill-lock.json # global lock v3
./skills-lock.json          # project lock v1
```

Global flags: `--config <path>` → `MSKILL_CONFIG`, `--cache-dir <path>` → `MSKILL_CACHE_DIR` (`cmd/root.go:69`).

### 6.3 Example `~/.mskill/config.yaml`

```yaml
cache:
  dir: "" # default ~/.cache/mskill
  ttl: 24h
  background_update: true
  strategy: fetch
  shallow: true
  filter: "blob:none"
  gc:
    enabled: true
    interval: 24h
    max_age: 30d
    max_size: 2GB
sparse:
  enabled: true
  cone: true
link:
  mode: auto # auto|symlink|copy
  targets: [claude, agents, project]
git:
  bin: git
  timeout: 60s
security:
  require_password_for_risky: true
  trust_all: false
  password_hash: "$2a$12$..."
  password_set_at: "2026-08-28T15:00:00Z"
  trust_enabled_at: "2026-08-28T15:05:00Z"
```

Viper keys: `cache.dir/ttl/background_update/strategy/shallow/filter/gc.*`, `sparse.enabled/cone`, `link.mode/targets`, `git.bin/timeout`, `security.require_password_for_risky/trust_all/password_hash/password_set_at/trust_enabled_at` (`docs/CONFIG.md:49`).

### 6.4 Env Overrides

| Env | Viper key / flag | Notes |
|-----|------------------|-------|
| `MSKILL_CACHE_DIR` | `cache.dir` / `--cache-dir` | Overrides XDG |
| `MSKILL_DOT_DIR` | `dotDir` | Default `~/.mskill` |
| `MSKILL_CONFIG` | `--config` | Full path to `config.yaml` |
| `SKILLS_API_URL` | `api.base` | Default `https://skills.sh` |
| `SKILLS_DOWNLOAD_URL` | `download.base` | Default `https://skills.sh` |
| `GH_HOST` | `github.host` | Enterprise host |
| `GITHUB_TOKEN` / `GH_TOKEN` | — | Private repos (`gh auth token` fallback) |
| `NO_COLOR` | — | Disables color |
| `CI`, `GITHUB_ACTIONS`, `GITLAB_CI`, `AGENT`, `CLAUDECODE`, `CURSOR_AGENT`, `OPENCODE`, `VSCODE_AGENT`, `CODEX` | `isAgentEnv()` | Fail-closed / blocks `trust enable` (`internal/security/security.go:29`) |

`security.password_hash` and `security.trust_enabled` are **not** bound to env — file-only, TTY-gated (`docs/CONFIG.md:65`).

### 6.5 Permissions & Atomicity

- Dot dir `0700`, config `0600` enforced after `viper.WriteConfigAs` + `os.Chmod` (`internal/config/config.go:146`, `docs/CONFIG.md:74`).
- Per-entry `flock` on `.lock` with 30s timeout (`internal/cache/cache.go:125`).
- Clone to `tmp/clone-XXXX` then `os.Rename` (atomic on same FS, copy fallback for cross-device, `internal/cache/cache.go:660`).
- `tmp/*` >1h pruned on `GC` (`internal/cache/cache.go:764`).

---

## 7. Phase 1 — Resolve

**Parser:** `resolve.ParseSkillRef(input)` (`internal/resolve/resolve.go:230`) — verbatim port of upstream `parseSource:155–280` (`docs/whitepaper.md:105`). Priority chain (`docs/whitepaper.md:107`):

1. **Local path** (`./`, `../`, absolute, `C:\`) → `Type=local` (`resolve/resolve.go:237`)
2. **Fragment** `#ref[@skillFilter]` (only if prefix looks like Git source) (`resolve/resolve.go:255`)
3. **Aliases** `SOURCE_ALIASES` (`coinbase/agentWallet` etc) (`resolve/resolve.go:29`)
4. **`github:` / `gitlab:` prefix** (`resolve/resolve.go:318`)
5. **Hosted artifact URL** (`raw.githubusercontent.com`, `codeload`, `/archive|raw|releases`) → `Type=download` (`resolve/resolve.go:414`)
6. **Enterprise `GH_HOST`** handling (`resolve/resolve.go:438`)
7. **Regex chain** for `github.com/owner/repo/tree/<ref>/<subpath>`, `/tree/<ref>`, bare repo, GitLab equivalents (`resolve/resolve.go:455`)
8. **Shorthand** `owner/repo@filter` and `owner/repo[/subpath]` (uses `github.com` vs generic `git` per `GH_HOST`) (`resolve/resolve.go:792`)
9. **Well-known URL** (`https://…/.well-known/(agent-)skills`) → `Type=well-known` (`resolve/resolve.go:851`)
10. **Fallback** `Type=git` (`resolve/resolve.go:870`)

**Output:** `Resolved{ Host, Owner, Repo, SkillPath, Subpath, Slug, Ref, CloneURL, Source, Type, IsLocal }` (`resolve/resolve.go:14`).

Helpers: `SanitizeSubpath` rejects `..` (`resolve/resolve.go:46`), `SanitizeName` lowercases `[^a-z0-9._]→-` (`resolve/resolve.go:85`), `GetOwnerRepo` strips host/prefix/fragment (`resolve/resolve.go:102`), `IsLocalPath` checks filesystem patterns (`resolve/resolve.go:173`), `GetGHHost` reads `GH_HOST` (`resolve/resolve.go:156`).

**Colon normalization:** `owner/repo:skill` → `owner/repo/skill` (`cmd/get.go:702`, `cmd/show.go:42`).

**Discovery for nested skills:** When sparse checkout omits subdir contents, fallback search via `discoverSkillInCache` (filesystem walk depth 5) and `discoverSkillViaGit` (`git ls-tree -r --name-only HEAD`) (`cmd/get.go:723`, `cmd/show.go:302`, `internal/cache/cache.go:922`).

---

## 8. Phase 2 — Cache (Dot-Folder + Sparse Git)

Replaces `mkdtemp + --depth 1 + rm -rf` with persistent, incremental, sparse storage (`docs/whitepaper.md:230`).

### 8.1 Cache Key & Meta

```go
CacheKey(host,owner,repo,ref) = sanitizedRef + "--" + sha256(host/owner/repo/ref)[:8]
  // e.g. main--a1b2c3d4  (internal/cache/cache.go:43)

MetaPath = $CacheDir/repos/<host>/<owner>/<repo>/<key>/.mskill-meta.json
```

`.mskill-meta.json` (`internal/cache/cache.go:26`):

```json
{
  "host":"github.com","owner":"vercel-labs","repo":"agent-skills","ref":"main",
  "cloneUrl":"https://github.com/vercel-labs/agent-skills.git",
  "commitSha":"abc123...","sparsePaths":["skills/nextjs"],
  "shallow":true,"depth":1,"filter":"blob:none",
  "lastFetch":"2026-08-28T10:00:00Z","lastAccess":"2026-08-28T10:05:00Z",
  "createdAt":"2026-08-28T09:00:00Z"
}
```

### 8.2 First Clone (cache miss)

Staging `tmp/clone-XXXX`, then `mv` atomic (`internal/cache/cache.go:504`):

```bash
git clone --filter=blob:none --sparse --depth 1 --single-branch \
  --branch <ref> <url> $tmp
# SHA ref:
#   git -C $tmp fetch --depth 1 origin <sha> && git -C $tmp checkout --detach <sha>
git -C $tmp sparse-checkout set --cone <skill-path> [<skill2> ...]
git -C $tmp rev-parse HEAD > meta.commitSha
mv $tmp $CACHE/repos/github.com/<owner>/<repo>/<ref>--<hash>/
```

`--cone` requires Git ≥2.25; nested non-cone paths use `--no-cone` (`internal/cache/cache.go:633`). Fallbacks (`internal/cache/cache.go:542`):

| Condition | Fallback |
|---|---|
| `unknown option --filter` (git <2.19) | Retry without `--filter` |
| `sparse-checkout` unsupported (<2.25) | Full checkout, warn |
| Network / 404 | Serve stale if exists (`internal/cache/cache.go:375`) |
| Corrupt `.git` (`not a git repo`) | `rm -rf cachePath` + re-clone (`internal/cache/cache.go:349`) |
| Interrupted clone (`tmp` left) | GC removes `tmp/*` >1h |

All `exec` via `git.Run(ctx, dir, args...)` → `exec.CommandContext` with `GIT_TERMINAL_PROMPT=0`, `GIT_ALLOW_PROTOCOL=https:http:ssh:git:file`, `timeout = viper.GetDuration("git.timeout")` default 60s (`internal/git/git.go:15`).

### 8.3 Cache Hit (fresh)

```go
if time.Since(meta.LastFetch) < viper.GetDuration("cache.ttl") && !force {
    bump meta.LastAccess; return cacheHit // no network
}
```

Default `cache.ttl = 24h` (`internal/config/config.go:94`). Background update flag present but blocking is default unless `--force` (`cmd/get.go:831`).

### 8.4 Second Skill Same repo@ref (sparse expansion)

No re-clone — expands in-place under lock (`internal/cache/cache.go:282`):

```bash
git -C $CACHE/... sparse-checkout set --cone <existing...> <new-skill>
# fallback: --no-cone or sparse-checkout disable + read-tree -mu HEAD
```

### 8.5 Incremental Update

Never `rm -rf` then re-clone (`internal/cache/cache.go:368`):

```bash
git -C $CACHE/... fetch --depth 1 origin <ref>
git -C $CACHE/... reset --hard FETCH_HEAD
git -C $CACHE/... rev-parse HEAD # → meta.commitSha
```

Tag pins skip fetch if SHA matches unless `--force` (logic scaffolded `internal/cache/cache.go:366`).

### 8.6 GC & Listing

- `GC(paths, dryRun)` (`internal/cache/cache.go:759`): deletes if `now - LastAccess > max_age` (30d) or total > `max_size` (2 GiB LRU by `LastAccess`), only when `.lock` try-locks. Prunes `tmp/*` >1h.
- `List(paths)` (`internal/cache/cache.go:888`): walks `repos/*` reading `.mskill-meta.json`, computes `dirSize`, sorted by `LastAccess` desc.
- `ListSkills(ctx, cachePath)` (`internal/cache/cache.go:922`): `git ls-tree -r --name-only HEAD` (sparse-aware) + filesystem walk fallback depth 5 to find `SKILL.md`/`skill.md`, returns sorted unique relative dirs.

---

## 9. Phase 3 — Install / Link

**Destinations** (`internal/link/link.go:94`):

- `ResolveDestinations(agentFilter, globalOnly, projectOnly)` — parses comma-separated, `*` = all, `""` → `viper.GetStringSlice("link.targets")` default `["agents"]` (`internal/link/link.go:94`).
- **75-entry agent map** ported from upstream, canonical keys include `claude`, `claude-code`, `agents`/`project` (universal), `codex`, `cursor`, `windsurf`, `zed`, `opencode`, etc. (`internal/link/link.go:28`).
- Universal agents (`~/.agents/skills`) use `Universal: true` → `auto` mode resolves to `copy` (`internal/link/link.go:528`).

**Mode** (`internal/link/link.go:459`):

- Flag priority: `--copy` → `copy` > `--link-mode` > `viper.GetString("link.mode")` default `auto`.
- `auto` = symlink on Unix non-universal, copy on Windows or universal (`internal/link/link.go:523`).
- `symlink` → relative symlink (`filepath.Rel`), `junction` on Windows, **fallback to copy** on failure (cross-device, no privilege) with warning (`internal/link/link.go:401`, `internal/link/link.go:549`).
- If dest exists: `os.RemoveAll(dest)` then symlink/copy (`internal/link/link.go:540`).

**Install** (`internal/link/link.go:437`):

```go
Install(cacheSkillPath, skillName, InstallOpts{Global,Project,Agents,LinkMode,Copy})
  → ResolveDestinations → for each agent × {global?, project?}:
      create parent, RemoveAll, symlink or CopyDirectory
```

`CopyDirectory(src,dst)` dereferences symlinks, skips `.git`/`__pycache__`/`.mskill-meta.json`/`.lock`, chmod 0644/0755 (`internal/link/link.go:259`). `CreateSymlink(target,link)` relative symlink with Windows handling (`internal/link/link.go:403`).

**Cache-view (no install):** Because `Phase 2` leaves sparse checkout in place, `Phase 3` is optional. `mskill get --show` and `mskill show/cat` reuse Resolve → Cache only, then `cat` from cache (`cmd/show.go:68`). Paths sanitized against `..` and must be inside skill dir (`cmd/show.go:150`).

**Hash & Lockfiles** (`internal/link/link.go:191`, `internal/link/link.go:584`):

- `SkillFolderHash(dir)` = `sha256(sorted relPath + 0x00 + content + 0x00)` skipping `.git`/`__pycache__` (`internal/link/link.go:191`).
- `UpdateLockfile(isGlobal, skillName, hash)` writes global `~/.agents/.skill-lock.json` v3 (`$XDG_STATE_HOME/skills/.skill-lock.json` else) and project `./skills-lock.json` v1 for interop (`internal/link/link.go:584`). Both tools can coexist (`docs/whitepaper.md:672`).

---

## 10. Search — SAFE/UNSAFE/UNKNOWN + Topic/Official

### 10.1 Flags

```
mskill search [query] [--topic <topic>] [--official] [--owner <owner>] [--limit 20] [--header]
mskill find   [query] …   # alias   (cmd/search.go:15)
```

- No `--json` by design — TSV ~40% smaller, `cut -f`/`awk -F'\t'` parseable (`docs/CLI.md:42`, `internal/search/search.go:181`).
- `--topic` mirrors `https://www.skills.sh/topic` and `--official` mirrors `https://www.skills.sh/official` (`docs/whitepaper.md:400`).

Topics allowed: `react|nextjs|design|mobile|agent-workflows|databases|testing|marketing|all` (`cmd/search.go:33`). Hardcoded + future scrape, 24h cache in spec (`docs/whitepaper.md:417`).

### 10.2 Query Handling

- Validates `query` ≥2 chars (API requires `q≥2` else 400) (`cmd/search.go:58`).
- Auto-fill `q` from `--owner` / `--topic` / `--official` when query short (`cmd/search.go:66`).
- Over-fetch `limit*2` for client-side `topic/official` filtering (`cmd/search.go:93`).

### 10.3 Client-Side Filtering

```go
filtered := FilterByTopic(skills, topic)    // exact match, "" or "all" → no filter (search/search.go:82)
filtered = FilterByOfficial(filtered, official) // check s.Official or allowlist (search/search.go:104)
```

Official allowlist hardcoded (~22 owners: `vercel`, `vercel-labs`, `anthropic`, `cloudflare`, …) (`internal/search/search.go:28`).

### 10.4 SAFE Signal

No `safe` field in search; audit is authoritative (`docs/whitepaper.md:426`):

```
GET https://add-skill.vercel.sh/audit?source=owner/repo&skills=slug1,slug2
→ { "slug": { ath:{risk:"safe"}, socket:{risk:"safe",alerts:0,score:90}, snyk:{risk:"low"}, zeroleaks:{risk:"safe",score:93} } }
```

Combine to `Verdict{ Safe, Unknown, Reason }` (`internal/api/api.go:54`):

- `Safe=false` if any `critical/high` or `socket.alerts>0` or `score<80` (`internal/api/api.go:225`).
- `Unknown=true` on timeout (3s, `internal/api/api.go:174`) / non-200 / missing slug → **treated as UNSAFE for install** (fail-closed), `UNKNOWN` yellow for display (`docs/whitepaper.md:438`).

`Audit(ctx, source, slugs)` groups by `source` (`owner/repo`), parallel per group, 3s timeout, top-20 by installs if >10 sources (`cmd/search.go:117`).

### 10.5 Rendering

`search.Render(skills, verdicts, header, noColor, w)` (`internal/search/search.go:184`):

- Columns (TSV, no header by default): `slug<TAB>topic<TAB>official<TAB>SAFE|UNSAFE|UNKNOWN<TAB>installs<TAB>description` (truncated 60 chars, `internal/search/search.go:129`).
- `slug` = `Source:SkillID` or fallback to `ID`/`Name` (`internal/search/search.go:209`).
- Colors: `SAFE` green `\x1b[32m`, `UNSAFE` red bold `\x1b[31;1m`, `UNKNOWN` yellow `\x1b[33m`, `OFFICIAL ✓` green (`internal/search/search.go:153`). Disabled if `!isTTY` or `NO_COLOR` or `TERM=dumb` (`internal/search/search.go:189`).
- Interactive TTY uses `tabwriter` to align columns; pipes stay raw TSV so `cut -f1` works (`internal/search/search.go:197`). Documented: `| column -t -s $'\t'` for pretty (`cmd/search.go:19`).

### 10.6 Interactive Fallback

Spec: if `query==""` and TTY, `survey.Input{Message:"Search skills:"}` with debounced live search (150 ms); non-TTY with no query → error (`docs/CLI.md:129`). Currently enforced as argument validation (`cmd/search.go:78`); live prompt path matches upstream `runSearchPrompt:5360` (`docs/whitepaper.md:145`).

---

## 11. Security Model — Password-Gated Risky Installs

### 11.1 Threat Model

> **Agent adversary:** AI agent (Claude Code, Cursor, OpenCode) same UID, non-interactive (`CI=1`, no TTY), tries `mskill get risky/skill -y` or `trust enable` to bypass (`docs/whitepaper.md:473`).
> Same-UID `chmod` alone cannot block file edits — absolute isolation requires OS user separation or keychain (`docs/whitepaper.md:479`).

### 11.2 When Password Is Required

```go
if !verdict.Safe || verdict.Unknown {
    // official does NOT auto-bypass (strict policy, docs/whitepaper.md:493)
    if IsTrustEnabled(paths) { allow } else { requirePassword() }
}
```

Implemented per-skill in `cmd/get.go:496`, `cmd/show.go:198` before cache/link.

### 11.3 `RequirePassword()` Flow

**Non-interactive ⇒ fail-closed** (`internal/security/security.go:110`):

```go
if !IsInteractiveTTY() || IsAgentEnv() || viper.GetBool("yes") || viper.GetBool("non-interactive") {
    return fmt.Errorf("UNSAFE skill %q requires human password…", slug)
}
```

- `IsInteractiveTTY()` = `os.Stdin` is `ModeCharDevice` + `term.IsTerminal` (`internal/security/security.go:17`).
- `IsAgentEnv()` checks `CI`, `GITHUB_ACTIONS`, `GITLAB_CI`, `AGENT`, `CLAUDECODE`, `CURSOR_AGENT`, `OPENCODE`, `VSCODE_AGENT`, `CODEX`, etc. (`internal/security/security.go:29`). Also checked in `cmd/get.go:55` for agent prompt suppression.

**Interactive:**

1. If `security.password_hash == ""` → `promptPassword("Create install password…")` + confirm → `bcrypt.GenerateFromPassword(..., cost 12)` → `viper.Set` + `config.WriteConfig` 0600 (`internal/security/security.go:119`, `internal/config/config.go:137`).
2. Else → `promptPassword("Enter password to install UNSAFE skill…")` → `bcrypt.CompareHashAndPassword` with 3 retries (`internal/security/security.go:148`).

Storage (`docs/CONFIG.md:44`):

```yaml
security:
  password_hash: "$2a$12$..."
  password_set_at: "2026-08-28T15:00:00Z"
  trust_enabled: true
  trust_enabled_at: "2026-08-28T..."
```

`bcrypt` only, never plaintext. TTY-gated writes only (`internal/security/security.go:165`).

### 11.4 Global Trust

```
mskill trust enable   # TTY required, password proof
mskill trust disable  # requires password
mskill trust status   # prints Trust: enabled/disabled, Password: set/not set (never hash)
mskill trust reset    # delete hash+trust, requires TTY + type RESET
```

- `EnableTrust` (`internal/security/security.go:165`): requires `IsInteractiveTTY() && !IsAgentEnv()` else `FATAL: trust change requires human TTY`; rejects `--yes`. If hash exists verifies with 3 retries, then `trust_enabled=true`.
- `DisableTrust` (`internal/security/security.go:223`): same TTY gate, verifies password if hash set.
- `ResetTrust` (`internal/security/security.go:254`): requires TTY + typing `RESET`; bcrypt one-way, no recovery.
- `IsTrustEnabled(paths)` (`internal/security/security.go:55`): `hash != "" && (trust_enabled || trust_enabled_at != "")`; `trust_enabled && hash=="" ⇒ trust==false`.

**Hardening beyond chmod** (`docs/whitepaper.md:547`):

1. No env bypass for `trust_enabled`/`password_hash` — file-only.
2. TTY-gated mutations.
3. `trust_enabled && hash==""` ignored.
4. No `--yes` bypass.

> Same-UID agent that edits `~/.mskill/config.yaml` directly *can* bypass by generating its own bcrypt and setting `trust_enabled:true`. Documented limit; for stronger isolation use `chattr +i` or future OS-keychain backend (`docs/whitepaper.md:553`).

---

## 12. CLI Surface — Complete Reference

Global flags (`cmd/root.go:69`): `--config <path>`, `--cache-dir <path>`, `--verbose`, `--no-color`, `-h/--help`, `-v/--version`.

| Command | Alias | Purpose | Key Flags |
|---------|-------|---------|-----------|
| `mskill get <ref>` | `add`, `a` | Resolve → Cache → Link | `-g/--global`, `-p/--project`, `-a/--agent <list\|*>`, `-s/--skill <list\|*>`, `--ref <branch\|tag\|sha>`, `--copy`, `--link-mode symlink\|copy\|auto`, `--show`, `--file <path>`, `--force`, `--full-depth`, `-y/--yes` (`cmd/get.go:818`) |
| `mskill show <ref>` | `cat` | View cached files without installing | `--file <path>` repeatable (default `SKILL.md`), `--list`, `--ref`, `--force` (`cmd/show.go:322`) |
| `mskill search [query]` | `find` | TSV search with topic/official | `--topic <topic>`, `--official`, `--owner <owner>`, `--limit 20`, `--header` (`cmd/search.go:190`) |
| `mskill list` | `ls` | List installed | `-g/--global`, `-a/--agent <name>` (`cmd/list.go:93`) |
| `mskill remove [skills]` | `rm` | Remove installed | `-g/--global`, `-a/--agent <name>` (`cmd/remove.go:66`) |
| `mskill update [skills]` | `upgrade` | Re-fetch + re-link | `-g/--global`, `-p/--project`, `-y/--yes`, `--force` (`cmd/update.go:86`) |
| `mskill cache <sub>` | — | Cache ops | `gc [--dry-run]`, `path`, `clean`, `list|ls [--header]` (`cmd/cache.go:204`) |
| `mskill trust <sub>` | — | Human-gated trust | `enable`, `disable`, `status`, `reset` (all TTY-gated, `cmd/trust.go:80`) |
| `mskill version` | — | Print version | — (`cmd/version.go:5`) |

**`--show` semantics:** Reuses same security gate as install (audit → password if `UNSAFE/UNKNOWN` unless trust), then cats from `~/.cache/mskill/repos/...` without linking. Paths sanitized against `..` and must be inside skill (`cmd/get.go:430`, `cmd/show.go:275`).

**`--list` semantics:** For both `get` and `show`, enumerates discoverable skills without installing via `cache.ListSkills` (git `ls-tree` + walk depth 5) (`cmd/get.go:312`, `cmd/show.go:135`).

**`--header` semantics:** TSV header `slug<TAB>topic<TAB>official<TAB>SAFE<TAB>installs<TAB>description` omitted by default for piping (`cmd/search.go:185`, `cmd/cache.go:88`).

See `docs/CLI.md:1` for full flag tables and `cache gc` / `trust` examples.

---

## 13. API Contracts

All tunneled via `internal/api/api.go` with `SetTimeout`/`RetryCount(2)` equivalents (manual retry, `api/api.go:100`):

| Endpoint | Method | Params | Used For | Client |
|---|---|---|---|---|
| `https://skills.sh/api/search` | GET | `q, limit, owner` | Search (client filters topic/official) | `api.Search` (`api/api.go:78`) |
| `https://skills.sh/api/download/{owner}/{repo}/{slug}` | GET | — | Metadata + file list for `get` (blob path, `zapier/connectors` override) | `api.DownloadMeta` (`api/api.go:246`) |
| `https://add-skill.vercel.sh/audit` | GET | `source, skills=csv` | Safe/unsafe verdict (3s timeout) | `api.Audit` (`api/api.go:155`) |
| `https://api.github.com/repos/{owner}/{repo}` | GET | — | Private check (optional, non-blocking) | — (`docs/whitepaper.md:221`) |
| `https://www.skills.sh/topic` | GET (HTML) | — | Topic scrape for `--topic` validation | `search.FilterByTopic` (`search/search.go:82`) |
| `https://www.skills.sh/official` | GET (HTML/JSON) | — | Official allowlist | `search.FilterByOfficial` (`search/search.go:104`) |

Bases overridable: `SKILLS_API_URL` (default `https://skills.sh`, `api/api.go:61`), `AUDIT_URL` / `SKILLS_AUDIT_URL` (default `https://add-skill.vercel.sh`, `api/api.go:67`), `GH_HOST` (`resolve/resolve.go:156`).

Tokens: `GITHUB_TOKEN` / `GH_TOKEN` → `Authorization: Bearer` header (`api/api.go:105`), plus `gh auth token` fallback for `git` credential (spec, `docs/whitepaper.md:634`).

Telemetry: upstream `https://add-skill.vercel.sh/t` fire-and-forget, suppressed for private repos — not implemented in `mskill` (out of loop, `docs/whitepaper.md:152`).

---

## 14. Failure Modes & Observability

| Failure | Detection | Response |
|---|---|---|
| Skill not found | 404 from search/download | "not found, try `mskill search <query> --topic`" (`docs/whitepaper.md:659`) |
| Private repo 401/404 | GitHub API / `git clone` 128 | "private repo, set GITHUB_TOKEN or `gh auth login`" |
| Audit timeout | 3s context (`api/api.go:174`) | `UNKNOWN` → fail-closed for install, yellow for search |
| Git shallow/filter unsupported | stderr `unknown option` | Retry without filter, warn (`cache/cache.go:542`) |
| Sparse path not in repo | `sparse-checkout set` exit 1 | "skill not found at ref <ref>" (`cache/cache.go:637`) |
| No TTY for risky install | `IsInteractiveTTY==false` (`security/security.go:17`) | Error, guide to TTY or `trust enable` (`security/security.go:115`) |
| Agent tries `trust enable` | `IsAgentEnv\|\|!isTTY` (`security/security.go:166`) | `FATAL: trust change requires human TTY` |
| Symlink no privilege (Windows) | `os.Symlink` error (`link/link.go:564`) | Fallback copy, warn |
| Cache corrupt | `git status` fails (`cache/cache.go:349`) | `rm -rf` + re-clone under lock |
| Network fetch fails (stale) | `fetch` 128 (`cache/cache.go:375`) | Serve stale if exists |
| Colon ref `owner/repo:skill` | `normalizeColonRef` check (`get/get.go:702`) | Mapped to `owner/repo/skill` |

Observability:

- `mskill --verbose` (`viper.GetBool("verbose")`, `cmd/root.go:53`) logs `resolve: %+v` and `cache: <path> commit <sha>` to stderr (`cmd/get.go:122`, `cmd/show.go:116`).
- `mskill cache path` prints resolved `cache/dot/config` (`cmd/cache.go:50`).
- `mskill trust status` never prints hash (`cmd/trust.go:46`, `security/security.go:284`).
- `GIT_TERMINAL_PROMPT=0` + `GIT_ALLOW_PROTOCOL` for deterministic git (`git/git.go:32`).

---

## 15. mskill vs `npx skills`

|  | `npx skills` (`skills@1.5.23`) | `mskill` |
|--|--------------|----------|
| Runtime | Node + `simple-git` + `tar` (~400 ms) | Static Go binary, `exec git` only (~50 ms, `docs/whitepaper.md:106`) |
| Cache | `mkdtemp("skills-") → clone --depth 1 → rm -rf` (re-clones multi-skill repos) | Persistent sparse cache, `fetch --depth 1 + reset --hard`, `sparse-checkout set --cone` per skill (`cache/cache.go:243`) |
| Search | `find [query] --owner`, no topic/official, no SAFE | `search --topic --official --owner --limit` + `SAFE/UNSAFE/UNKNOWN` inline, TSV (`search/search.go:184`) |
| Security | Advisory table only, `-y` bypasses confirm (`docs/whitepaper.md:149`) | Fail-closed: risky requires password; `trust enable` TTY-only, bcrypt cost 12 (`security/security.go:13`) |
| Output | Prompt-heavy | Compact TSV for humans + agents (no JSON), color-aware (`search/search.go:153`) |
| Lockfiles | Writes `skills-lock.json` v1 / `.skill-lock.json` v3 | Reads/writes same lockfiles for interop (`link/link.go:584`) |
| Compat | — | Same shorthand (`owner/repo`, `github:`, `gitlab:`, `well-known`, `local`), same flags (`-g/-a/-s/-y/--copy`) (`resolve/resolve.go:230`) |
| Fallbacks | — | Old git (`--filter`/`sparse`), stale serve, `tmp/*` GC, symlink→copy |

---

## 16. Development, Testing & Release

### 16.1 Commands

```sh
just build     # go build -o mskill .          (justfile: build)
just vet       # go vet ./...                  (justfile: vet)
just test      # go test ./...                 (justfile: test)
just clean     # rm -f mskill                  (justfile: clean)
just install   # build + install to $GOBIN     (justfile: install)
```

### 16.2 Tests

- **Resolve:** `internal/resolve/resolve_test.go` — parses 10-step precedence, `SanitizeSubpath`/`SanitizeName`, `IsLocalPath`, `IsHostedArtifactUrl`.
- **API:** `internal/api/api_test.go` — `Search`/`Audit`/`evaluate` with `httptest`, 3s audit timeout, `UNKNOWN` on non-200.
- **Cache:** `internal/cache/cache_test.go`, `cache_coverage_test.go` — `CacheKey`, `MetaPath`, `SaveMeta/LoadMeta`, `dirSize/parseSize`, `missingPaths/dedup`.
- **Git:** `internal/git/git_test.go` — `Run` with timeout, `GIT_TERMINAL_PROMPT=0`.
- **Link:** `internal/link/link_test.go`, `link_coverage_test.go` — `SanitizeName`, `ResolveDestinations`, `CopyDirectory`, `SkillFolderHash`, `Install` symlink/copy.
- **Search:** `internal/search/search_test.go`, `search_coverage_test.go` — `FilterByTopic/Official`, `isOfficialSkill`, `Render` TSV, `truncateDesc`.
- **Security:** `internal/security/security_test.go`, `security_coverage_test.go` — `IsInteractiveTTY`, `IsAgentEnv`, `HashPassword` bcrypt cost 12, `IsTrustEnabled`.
- **Config:** `internal/config/config_test.go`, `paths_test.go` — `ResolvePaths` XDG/`MSKILL_*` overrides, `EnsureDirs` perms, `InitViper` defaults.
- **E2E:** `internal/e2e/e2e_test.go:72` — real temp git repos:
  - `TestFetchStoreLink_SparseAndTTL` — sparse clone, TTL cache hit, sparse expansion same `repo@ref`.
  - `TestFetchStoreLink_IncrementalUpdateAndLinkSymlink` — `fetch --depth1` update `v1→v2`, symlink vs copy.
  - `TestCacheGCRemovesExpiredAndPreservesFresh` — `max_age 30d` + `dry-run`.
  - `TestListAndRemoveFlow` — `Install` → `RemoveAll`.
  - `TestResolveSanitization` — rejects `../evil`.

Run: `go test ./...` (respects `MSKILL_CACHE_DIR`, `MSKILL_DOT_DIR` isolation via `t.Setenv`).

### 16.3 Config & Paths Testing

`viper.Reset()` + `t.Setenv("MSKILL_DOT_DIR")` pattern (`e2e/e2e_test.go:46`) isolates tests. Commit SHA via `git rev-parse HEAD` asserted non-empty (`e2e/e2e_test.go:99`).

### 16.4 Release

`goreleaser.yaml:1` — `version: 2`, `go mod tidy` hook, builds `mskill` for `linux/darwin/windows × amd64/arm64` with `ldflags -s -w -X skill.sh/mskill/cmd.version={{.Version}}`, archive `mskill_{{.Version}}_{{.Os}}_{{.Arch}}` + `checksums.txt`, brew formula to `skill-sh/homebrew-tap`.

### 16.5 Roadmap (from whitepaper §16)

- **Phase 0 (week 1):** Scaffolding — `cmd/root`, `internal/*`, `runGit`, TTY gates (done).
- **Phase 1 (week 2):** Resolve + Cache — `ParseSkillRef`, `api.Search/Audit`, sparse cache (done).
- **Phase 2 (week 2):** Link — 75-agent map, lockfiles, `CopyDirectory`/`CreateSymlink` (done).
- **Phase 3 (week 3):** Search + Security — TSV + `SAFE/UNSAFE/UNKNOWN`, `trust` (done).
- **Phase 4 (week 4):** Polish — `cache gc`, `--verbose`, completions, `golangci-lint`, brew (done).

Open questions (`docs/whitepaper.md:732`): server-side `?topic=&official=` API, audit scoring threshold `≥80`, keychain vs file, cone vs non-cone, Git version floor.

### 16.6 References

- `skills@1.5.23` — `dist/cli.mjs` (7726 lines), `package.json` (`tar@^7.5.20`, `yaml@^2.8.3`) — `docs/whitepaper.md:744`
- https://skills.sh — `/api/search`, `/api/download/{owner}/{repo}/{skill}` — `docs/whitepaper.md:745`
- https://add-skill.vercel.sh/audit — `?source=&skills=` — `docs/whitepaper.md:746`
- https://www.skills.sh/topic / `/official` — topics + ~150 official owners — `docs/whitepaper.md:747`
- `spf13/cobra`, `spf13/viper`, `go-resty/resty`, `AlecAivazis/survey`, `x/crypto/bcrypt`, `golang.org/x/term` — `go.mod:1`

---

*End. Generated from source. Next step: `go mod tidy && mskill --help`.*
