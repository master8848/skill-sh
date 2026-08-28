# mycli — A Go-Native Replacement for `npx skills`

## A Fetch → Store → Link Skill Manager with Sparse Git Cache, Fail-Closed Security, and Human-Gated Trust

**Version:** 0.1.0-draft  
**Date:** 2026-08-28  
**Status:** Design Whitepaper (pre-implementation)  
**Authors:** mycli team  
**Stack:** Go · Cobra · Viper · Resty · Survey · `golang.org/x/crypto/bcrypt`  
**Upstream:** `skills@1.5.23` (`npx skills`), https://skills.sh, https://www.skills.sh/topic, https://www.skills.sh/official

---

### Abstract

`npx skills` (Vercel Labs, 91 releases, `skills@1.5.23`) is the de-facto installer for the open agent-skills ecosystem (300+ agents: Claude Code, Cursor, Codex, OpenCode, etc.). It is a Node.js CLI that resolves a shorthand (`owner/repo`, `github:`, `gitlab:`, `well-known`, `local`), shallow-clones the repo to `os.tmpdir()`, and symlinks/copies the discovered skill directories into per-agent locations (`~/.claude/skills`, `~/.agents/skills`, `.agents/skills`, etc.).

It works, but it has three structural deficits this whitepaper addresses:

1. **Node dependency + cold performance.** Every invocation pays Node + `simple-git` + `tar` startup. AI agents already shell out to Git; a static Go binary eliminates the runtime.
2. **No persistent cache.** Each `add` does `mkdtemp("skills-") → git clone --depth 1 → copy → rm -rf tmp`. No incremental fetch, no sparse checkout, no TTL/GC. Multi-skill repos re-clone the whole repo.
3. **Advisory-only security.** Audit (`https://add-skill.vercel.sh/audit?source=X&skills=Y`, 3s timeout) renders a table but never blocks. There is no topic/official-aware search, no `safe/unsafe` column, and no mechanism to prevent an agent (non-interactive) from installing a risky skill.

**mycli** re-implements the same user contract — **`mycli get user/repo/skill`** — as a pure Go binary that does exactly three things in a loop:

> **1. Resolve → 2. Cache → 3. Install/Link**
>
> No complex dependency resolution. No versioning hell. Just: *Fetch, Store, Link.*

On top of that minimal loop it adds: a **dot-folder sparse cache** with `git fetch --depth 1` incremental updates, a **fail-closed security gate** where `risky ⇒ password required` (with a one-time human `trust enable` that agents cannot flip), and **search that surfaces `SAFE / UNSAFE / UNKNOWN` inline** plus `--topic` and `--official` filters sourced directly from skills.sh.

The remainder of this paper is the executable spec: what we keep from upstream, what we change, and how the three phases interact.

---

### Table of Contents

1. [Goals & Non-Goals](#1-goals--non-goals)
2. [What Upstream Does Today (skills@1.5.23 Audit)](#2-what-upstream-does-today-skills1523-audit)
3. [Architecture: The Three-Phase Loop](#3-architecture-the-three-phase-loop)
4. [Stack Rationale: Why Go + Cobra/Viper/Resty/Survey](#4-stack-rationale-why-go--cobraviperrestysurvey)
5. [Phase 1 — Resolve](#5-phase-1--resolve)
6. [Phase 2 — Cache (Dot-Folder + Sparse Git)](#6-phase-2--cache-dot-folder--sparse-git)
7. [Phase 3 — Install/Link](#7-phase-3--installlink)
8. [Search: Safe/Unsafe, Topics, Official](#8-search-safeunsafe-topics-official)
9. [Security Model: Password-Gated Risky Installs & Human-Only Trust](#9-security-model-password-gated-risky-installs--human-only-trust)
10. [CLI Surface](#10-cli-surface)
11. [Config & Filesystem Layout](#11-config--filesystem-layout)
12. [API Contracts](#12-api-contracts)
13. [Failure Modes & Observability](#13-failure-modes--observability)
14. [Migration & Compatibility](#14-migration--compatibility)
15. [Scope of Improvement Beyond the Loop](#15-scope-of-improvement-beyond-the-loop)
16. [Implementation Roadmap](#16-implementation-roadmap)
17. [Open Questions](#17-open-questions)
18. [References](#18-references)
19. [Appendix A: Git Command Sequences](#appendix-a-git-command-sequences)
20. [Appendix B: Config Example](#appendix-b-config-example)

---

### 1. Goals & Non-Goals

**Goals**

- **Drop Node.** Single static binary (`mycli`), `go install` or `brew install`, no `npx`, no `node_modules`.
- **Preserve the 3-step mental model.** User types `mycli get vercel-labs/agent-skills/vercel-optimize`; CLI resolves via skills.sh, populates cache if needed, links into agent dirs. The loop is the product.
- **Own dot-folder cache with sparse incremental Git.** `~/.mycli` (config) + `~/.cache/mycli` (XDG) or `~/.mycli/cache` (fallback). `git clone --filter=blob:none --sparse --depth 1` on first hit, `git fetch --depth 1 && reset --hard FETCH_HEAD` thereafter, `sparse-checkout set` per skill. No `go-git`; only `exec git`.
- **Safe/unsafe at a glance.** Every `search` row shows `SAFE / UNSAFE / UNKNOWN` sourced from skills.sh audit, with color and compact single-line text optimized for both humans and agents (no JSON bloat).
- **Risky ⇒ password.** An agent running non-interactively cannot install a flagged skill without a human-provided password. Official status does not auto-bypass.
- **Topic + official filters.** `search --topic react --official` maps to https://www.skills.sh/topic and https://www.skills.sh/official.

**Non-Goals**

- Dependency graph / version SAT solver — skills are file trees, not npm packages.
- Versioning hell — tags/refs are Git refs; no semver range resolution.
- Replacing `skills.sh` — we are a client. Search, download, and audit remain server-side.
- Windows service / daemon — CLI only.

---

### 2. What Upstream Does Today (skills@1.5.23 Audit)

*Inspection: `/tmp/skills-inspect/package/dist/cli.mjs` (7726 lines), `package.json`, live https://skills.sh/api/search, audit, and download endpoints. Subagent audit on file.*

#### 2.1 Command surface

```
skills add <package>         # alias a
skills use <package>@<skill> # ephemeral prompt, no install
skills remove [skills]
skills list | ls [--json]
skills find [query] [--owner <owner>]
skills update [skills...]    # alias upgrade
skills init [name]
skills experimental_install  # restore from skills-lock.json
skills experimental_sync     # scan node_modules
```

All parsing is manual (`process.argv.slice(2)`), not commander/yargs.

**`add` flags:** `-g/--global`, `-a/--agent <list>`, `-s/--skill <list>`, `-l/--list`, `-y/--yes`, `--all` (= `--skill * --agent * -y`), `--copy`, `--full-depth`, `--metadata <json>`, `--subagent`.  
**`find` flags:** positional `query`, `--owner`. No `--topic`, no `--official`, no `--json` for search.  
**`list` flags:** `-g/--global`, `-a/--agent`, `--json`.

#### 2.2 Resolve (`parseSource:155–280`)

Priority chain:

1. Local path (`./`, `../`, absolute, `C:\`) → `type:local`
2. Fragment `#ref[@skillFilter]` (only if prefix looks like Git source)
3. Aliases (`coinbase/agentWallet` etc)
4. `github:` / `gitlab:` prefix
5. Hosted artifact URL (`raw.githubusercontent.com`, `codeload`, `github.com/*/archive|raw|releases/*`) → `type:download`
6. Enterprise `GH_HOST` handling
7. Regex chain for `github.com/owner/repo/tree/<ref>/<subpath>`, `/tree/<ref>`, bare repo, GitLab equivalents
8. Shorthand `owner/repo@filter` and `owner/repo[/subpath]` (uses `github.com` vs generic `git` per `GH_HOST`)
9. Well-known URL (`https://…/.well-known/(agent-)skills`) → `type:well-known`
10. Fallback `type:git`

Safety: `sanitizeSubpath` rejects `..`, `isPathSafe` family for archive extraction, size caps (10 MiB download, 25 MiB extract, 1000 files).

Private check: `fetch https://api.github.com/repos/{owner}/{repo}` → `private === true`; gates telemetry + audit.

#### 2.3 Cache / clone

**No persistent cache.** `cloneRepo():840` → `mkdtemp(join(tmpdir(),"skills-"))`, `simple-git` with `["--depth","1","--branch",ref]` or `["--depth","1"]`, 5 min timeout (`SKILLS_CLONE_TIMEOUT_MS`), `GIT_TERMINAL_PROMPT=0`, SSH fallback `GIT_SSH_COMMAND="ssh -o BatchMode=yes"`, `gh` fallback via `gh repo clone`. Cleanup via `rm -rf tmp`.  
Download path (`type:download` / well-known artifacts) uses `mkdtemp("skills-download-")` + `fetch` + zip/tar extraction (`tar` lib) + single-top-level-dir unwrapping.

Well-known discovery tries 4–8 URLs (`.well-known/agent-skills`, `.well-known/skills`, scoped + root) with 10s timeout.

`--full-depth` affects **discovery only** (recursive `findSkillDirs` depth 5) not clone depth.

#### 2.4 Install / link

`installSkillForAgent():2150`, `installWellKnownSkillForAgent():2334`, `installBlobSkillForAgent():2416` share:

- `sanitizeName` (lowercase, `[^a-z0-9._] → -`)
- `cleanAndCreateDirectory(canonicalDir)` (`rm -rf + mkdir`) then `copyDirectory` (dereference, chmod, skip `.git/__pycache__`)
- Mode: `symlink` default unless `--copy` or single target; `universal` agents (`.agents/skills`) stop at canonical dir; otherwise `createSymlink(canonical→agentDir)` (relative, `junction` on win32) with copy fallback.
- 75-entry `agents` map: Claude Code (`.claude/skills` + `~/.claude`), Codex, Cursor, Windsurf, Zed, OpenCode, etc. Universal = `.agents/skills` (global `~/.agents/skills`, project `.agents/skills`).
- Locks: global `.skill-lock.json` v3 (`$XDG_STATE_HOME/skills/.skill-lock.json` else `~/.agents/.skill-lock.json`) and project `skills-lock.json` v1 with `skillFolderHash` (sha256 over file list).

#### 2.5 Search

`SEARCH_API_BASE = https://skills.sh` (env `SKILLS_API_URL`), `GET /api/search?q=…&limit=20&owner=…`, no auth. Returns `{skills:[{id,skillId,name,installs,source}], count, duration_ms}`, sorted client-side by `installs`. Interactive prompt (`runSearchPrompt:5360`) debounced 150–350 ms, 8 visible, `find` with query prints `pkg@name  installs` + `https://skills.sh/<slug>` and exits. No pagination, single page of 20.

#### 2.6 Audit & download

`AUDIT_URL = https://add-skill.vercel.sh/audit` (`fetchAuditData:2683`, 3s `AbortController`, `?source=owner/repo&skills=slug1,slug2`, silent fail → `null`). Triggered in `runAdd:4768` only if repo is public. Display via `buildSecurityLines:4063` (Gen/Socket/Snyk table) — **informational only, never blocks**, still asks `confirm` unless `-y`.

`DOWNLOAD_BASE_URL = https://skills.sh`, `GET /api/download/{owner}/{repo}/{slug}` (with `zapier/connectors` override). Used in `tryBlobInstall:3946` for `vercel, vercel-labs, heygen-com` when no ref and no `fullDepth`: fetch tree via GitHub API (`fetchRepoTree:3794` → `fetchTreeBranch` with token→`gh` fallback), find `SKILL.md` paths, fetch each via `raw.githubusercontent.com`, parse frontmatter, parallel `fetchSkillDownload`. Blob path uses `raw` + `snapshotHash`.

Telemetry: `https://add-skill.vercel.sh/t`, fire-and-forget, suppressed for private repos, includes `v, ci, agent` (`@vercel/detect-agent`).

#### 2.7 What is missing

- No `--topic` / `--official` / `--json` for search; no pagination.
- No persistent cache → repeated clones, no sparse.
- No blocking security — advisory table only.
- No password or trust model — `-y` bypasses confirm, agents can do same.
- `go-git` not used; `simple-git` is the only Git layer.

---

### 3. Architecture: The Three-Phase Loop

```
User: mycli get user/repo/skill [--global] [--agent claude --copy] [--ref main]

┌──────────┐    ┌─────────┐    ┌──────────────┐
│  Resolve │───▶│  Cache  │───▶│ Install/Link │
│          │    │         │    │              │
│ skills.sh│    │ ~/.cache│    │ symlink/copy │
│ /api/*   │    │ + sparse│    │ to agent dir │
│ audit    │    │ git     │    │              │
└──────────┘    └─────────┘    └──────────────┘
      ▲               │               │
      └───────────────┴───────────────┘
              loop per skill
```

Each invocation runs the loop once per requested skill. `Resolve` is network (Resty). `Cache` is local FS + `exec git`. `Link` is local FS. No DAG, no SAT, no lockfile solver beyond the trivial `skillFolderHash` check for idempotence.

**Invariants**

- Resolve never writes to disk.
- Cache never contacts `skills.sh` (only Git remote).
- Link never contacts network.
- Overall failure is fail-closed for risky skills (see §9).

**Why not more phases?** Complexity budget is fixed. Upstream proved that discovery (`fullDepth`), plugin grouping, and lockfiles can live inside Link without a separate resolver. Versioning is Git refs, not ranges — out of scope.

---

### 4. Stack Rationale: Why Go + Cobra/Viper/Resty/Survey

| Need | Choice | Why |
|---|---|---|
| CLI framework | `spf13/cobra` | Subcommands, flag groups, `PersistentPreRun`, shell completion, man generation. De-facto standard; maps 1:1 to upstream commands (`add`, `get`, `search`, `list`, `remove`, `cache`, `trust`). |
| Config | `spf13/viper` | Dot-folder + XDG + env (`MYCLI_*`) + flag precedence. `viper.GetDuration("cache.ttl")`, `SetDefault`, `WriteConfigAs(...0600)` exactly matches spec's "own dot folder" requirement. |
| HTTP | `go-resty/resty/v2` | `SetTimeout`, `SetRetryCount`, interceptors, `SetBaseURL("https://skills.sh")`, `R().SetQueryParams` for search/download/audit. Cleaner than `net/http` raw for 3 endpoints. |
| Prompts | `AlecAivazis/survey/v2` | `survey.Select` / `survey.Password` / `survey.Confirm` / `survey.MultiSelect` for interactive search, risky-skill password, trust enable, agent picking. Respects TTY detection. |
| Hashing | `golang.org/x/crypto/bcrypt` | Password hashing for risky-skill gate; cost 12. No plaintext storage. |
| No `go-git` | `exec git` | Spec: "we dont need go git as we only use clone and pull operation try to clone sparse". `go-git` sparse/filter is incomplete; `exec git` gives `--filter=blob:none --sparse --depth 1` and `sparse-checkout` for free, plus credential helper integration. |

Binary: `CGO_ENABLED=0 go build -ldflags="-s -w"` → ~8–12 MiB static binary, <50 ms startup vs ~400 ms Node.

---

### 5. Phase 1 — Resolve

**Input:** `user/repo/skill`, `owner/repo`, `https://github.com/…/tree/<ref>/<subpath>`, `github:owner/repo`, `local:./path`, `well-known:https://example.com`.

**Parser:** Port of upstream `parseSource` precedence verbatim (see §2.2). Keep `sanitizeSubpath`, `SOURCE_ALIASES`, `GH_HOST`, `isHostedArtifactUrl`, `isLocalPath`, `getOwnerRepo`. Add `ParseSkillRef` that splits `source@skillFilter` and `#ref`.

**Network calls (Resty):**

1. **Metadata:** `GET /api/search?q=<filter>&limit=20` or direct `GET` for known slug? Simplest: reuse search with exact `q=skill` then filter exact match by `source+skillId`. Alternative: `GET /api/download/{owner}/{repo}/{slug}` already returns metadata + file list (see §12). Use that for `get`.
2. **Audit:** `GET https://add-skill.vercel.sh/audit?source=owner/repo&skills=slug1,slug2` (3s timeout, `Resty.SetTimeout`). Batch per source.
3. **Official list:** `GET https://www.skills.sh/official` (scrape or hardcode; refresh 24h).
4. **Private check:** `GET https://api.github.com/repos/{owner}/{repo}` (optional, for telemetry suppression; non-blocking).

**Output:** `Resolved{ CloneURL, Ref, Subpath, SkillFilter, Source, Slug, IsOfficial, AuditVerdict }`.

**Error handling:** 404 → "skill not found, try `mycli search <query>`"; private 401/404 → require token hint; timeout → audit `UNKNOWN` (fail-closed).

---

### 6. Phase 2 — Cache (Dot-Folder + Sparse Git)

Replaces `mkdtemp + --depth 1 + rm -rf` with persistent, incremental, sparse storage.

#### 6.1 Paths

```go
paths := config.ResolvePaths() // internal/config
// CacheDir = $XDG_CACHE_HOME/mycli else ~/.cache/mycli
// DotDir   = ~/.mycli (0700)
// ConfigFile = ~/.mycli/config.yaml (0600)
// TrustFile  = ~/.mycli/trust.json  (0600) — alternative to config.yaml field
```

Env overrides: `MYCLI_CACHE_DIR`, `MYCLI_DOT_DIR`, `MYCLI_CONFIG`, `GH_HOST`, `SKILLS_API_URL`, `SKILLS_DOWNLOAD_URL`.

#### 6.2 Directory layout

```
~/.cache/mycli/
├── repos/
│   └── github.com/
│       └── <owner>/
│           └── <repo>/
│               └── <refSlug>--<hash8>/   # e.g. main--a1b2c3d4
│                   ├── .git/             # blobless, shallow, sparse
│                   ├── <skill-path>/...  # only this skill's files
│                   ├── .mycli-meta.json
│                   └── .lock
└── tmp/
    └── clone-XXXX/                        # staging, then atomic rename

~/.mycli/
├── config.yaml                           # viper, 0600
└── trust.json                            # optional split file, 0600
```

Cache key: `sha256(host+owner+repo)[:8] + sanitized ref` (slash→dash). One `.git` per `repo@ref`, shared across skills from same repo.

`.mycli-meta.json`:

```json
{
  "host":"github.com","owner":"vercel-labs","repo":"agent-skills","ref":"main",
  "cloneUrl":"https://github.com/vercel-labs/agent-skills.git",
  "commitSha":"abc123...","sparsePaths":["skills/nextjs","skills/deploy"],
  "shallow":true,"depth":1,"filter":"blob:none",
  "lastFetch":"2026-08-28T10:00:00Z","lastAccess":"2026-08-28T10:05:00Z",
  "createdAt":"2026-08-28T09:00:00Z"
}
```

#### 6.3 First clone (cache miss)

Staging `tmp/clone-XXXX`, then `mv` (atomic same-FS).

```bash
git clone --filter=blob:none --sparse --depth 1 --single-branch \
  --branch <ref> <url> $tmp
# SHA ref: clone without --branch, then:
#   git -C $tmp fetch --depth 1 origin <sha> && git -C $tmp checkout --detach <sha>
git -C $tmp sparse-checkout set --cone <skill-path> [<skill-path-2> ...]
git -C $tmp rev-parse HEAD > meta.commitSha
mv $tmp $CACHE/repos/.../<ref>--<hash>/
```

`--cone` requires Git ≥2.25; nested non-cone paths use `--no-cone`.

#### 6.4 Cache hit (fresh)

```go
if time.Since(meta.LastFetch) < viper.GetDuration("cache.ttl") && !force {
    bump meta.LastAccess; return cacheHit // no network
}
```

Default `cache.ttl = 24h`, `background_update = true` (serve stale + async fetch) vs blocking when `--force`.

#### 6.5 Second skill from same repo@ref (sparse expansion)

No re-clone:

```bash
git -C $CACHE/... sparse-checkout set --cone <existing...> <new-skill>
# or: git -C $CACHE/... sparse-checkout add <new-skill>
```

#### 6.6 Incremental update (`--update` or stale TTL)

Never `rm -rf` then re-clone.

```bash
git -C $CACHE/... fetch --depth 1 origin <ref>
git -C $CACHE/... reset --hard FETCH_HEAD
git -C $CACHE/... rev-parse HEAD # → meta.commitSha
# refresh TTL
```

`fetch + reset --hard` is deterministic for shallow blobless clones (no upstream tracking needed, no merge). `git pull --ff-only --depth 1` is the branch-tracking alternative.

Tag pins: skip fetch if `commitSha` already equals tag SHA unless `--force`.

#### 6.7 Fallbacks

| Condition | Fallback |
|---|---|
| `unknown option --filter` (git <2.19) | Retry without `--filter`, `meta.filter=""` |
| `sparse-checkout` unsupported (<2.25) | Full checkout, warn |
| Network / 404 (branch deleted, private) | Serve stale if exists, else error |
| Corrupt `.git` (`not a git repo`) | `rm -rf cachePath` + re-clone under lock |
| Interrupted clone (`tmp` left) | GC removes `tmp/*` >1h on startup |

All `exec` wrapped as `runGit(ctx, dir, args...)` → `exec.CommandContext` with `GIT_TERMINAL_PROMPT=0`, `GIT_ALLOW_PROTOCOL=https:http:ssh:git:file`, `timeout = viper.GetDuration("git.timeout")` (default 60s).

#### 6.8 Concurrency & GC

- Per-cache-entry `flock` (`github.com/gofrs/flock` or `syscall.Flock`) on `$CACHE/.../.lock` with 30s timeout.
- Clone to `tmp` then `os.Rename` (atomic).
- GC: `mycli cache gc [--dry-run]` scans `repos/*/*/*`, reads `meta.lastAccess`, `du`, deletes if `now - lastAccess > max_age` (default 30d) or total > `max_size` (2 GiB, LRU by `lastAccess`). Only deletes when `.lock` try-locks.

**Viper keys:**

```yaml
cache: { dir, ttl, background_update, strategy: fetch, shallow, filter, gc: {enabled, interval, max_age, max_size} }
sparse: { enabled, cone }
git: { bin, timeout }
```

---

### 7. Phase 3 — Install/Link

**Destinations (priority, `survey.MultiSelect` in interactive):**

- Global: `~/.claude/skills/<skill>`, `~/.agents/skills/<skill>`, `~/.codex/skills`, `~/.cursor/skills`, … (75-entry map ported from upstream, plus `XDG` overrides `XDG_CONFIG_HOME`, `CLAUDE_HOME`, `CODEX_HOME`)
- Project: `./.agents/skills/<skill>` (and per-agent project dirs)
- Flags: `-g/--global` (user-level only), `-p/--project` (project only), `-a/--agent <list|*>`, `--copy` (force copy), `-y/--yes` (skip confirm).

**Mode:**

- Default: `symlink` (relative, `junction` on Windows) → instant updates on `fetch`; fallback to `copy` on `symlink` failure (Windows no privilege, cross-device).
- `auto` = symlink on Unix, copy on Windows; `viper.GetString("link.mode")` + `--copy` / `--link-mode`.
- If dest exists: `os.RemoveAll(dest)` then symlink/copy.

**Implementation:**

```go
copyDirectory(src, dst) // dereference, chmod, skip .git/__pycache__
createSymlink(cacheAbs, destAbs) // relative
```

On `universal` agents (`.agents/skills`) global install stops at canonical dir (no extra symlink) — same as upstream `isUniversalAgent`.

**Hash tracking:** `skillFolderHash = sha256(sorted file paths + contents)` stored in global `.skill-lock.json` v3 and project `skills-lock.json` v1 for `update` diffing — keep compatibility.

---

### 8. Search: Safe/Unsafe, Topics, Official

#### 8.1 API gap

`GET /api/search` supports only `q, limit, owner`. No `topic`, no `official`, no `safe`. The paper proposes **client-side filtering** until server adds params (with `limit*2` over-fetch to avoid empty pages).

#### 8.2 Flags

```
mycli search [query] [--topic <topic>] [--official] [--owner <owner>] [--limit 20]
mycli find   [query] …   # alias
```

No `--json`. Output is compact plain text (see §8.4) — readable in a terminal and parseable by agents via `cut`/`awk` without JSON overhead. Web parity: `--topic` mirrors https://www.skills.sh/topic and `--official` mirrors https://www.skills.sh/official so `mycli search --topic react`, `mycli search --official`, or `mycli search nextjs --topic nextjs --official` behave like the website filters.

Topics (from https://www.skills.sh/topic):

`react, nextjs, design, mobile, agent-workflows, databases, testing, marketing` plus `all`. Hardcode + `go generate` from `/topic` scrape, 24h cache in `~/.mycli/cache/topics.json`.

Official: `GET https://www.skills.sh/official` → curated owner/repo allowlist (scrape or hardcode `internal/official/list.go`, 24h cache `official.json`). Filter: `skill.Official == true` or `owner/repo ∈ allowlist`.

Owner: passthrough to API (`?owner=`), lowercased, validated `^[a-z0-9](?:[a-z0-9-]{0,38})$`.

#### 8.3 Safe/unsafe signal

Upstream has no `safe` field in search; audit is authoritative:

```
GET https://add-skill.vercel.sh/audit?source=owner/repo&skills=slug1,slug2
→ { "slug": { ath:{risk:"safe"}, socket:{risk:"safe",alerts:0,score:90}, snyk:{risk:"low"}, zeroleaks:{risk:"safe",score:93} } }
```

Combine to `Verdict{ Safe bool, Reason string, Unknown bool }`:

- `Safe=true` if all checks `safe/low` and `score ≥ threshold`.
- `Safe=false` if any `critical/high`.
- `Unknown=true` on timeout / non-200 / missing slug → treated as `UNSAFE` for install (fail-closed), `UNKNOWN` for display.

**Search-time batching:** Group results by `source` (`owner/repo`), parallel `Audit(source, slugs)` per group (bounded concurrency, 3s timeout each). For >10 sources, audit top-20 by `installs`.

#### 8.4 Table

```
NAME                          TOPIC          OFFICIAL  SAFE     INSTALLS  DESCRIPTION
vercel-labs/agent-skills      nextjs         ✓         SAFE     671k      Next.js App Router patterns…
some/risky-skill              unknown                  UNSAFE   1.2k      curl|bash postinstall
```

Colors: `SAFE` green, `UNSAFE` red bold, `UNKNOWN` yellow; `OFFICIAL ✓` green; disabled if `!isTTY || NO_COLOR`. Width = `process.stdout.columns` aware, truncate description to 60.

**Compact text format (no JSON):** One line per skill, tab-separated for machine parsing but still human-readable. Agents parse with `cut -f`/`awk` without JSON bloat.

```
# mycli search react --topic react --official
vercel-labs/agent-skills:vercel-react-best-practices  react  official  SAFE    671k  React performance best practices
vercel-labs/agent-skills:vercel-react-native-skills   mobile official  SAFE    197k  React Native + Expo patterns
some/risky-skill:risky-skill                           unknown          UNSAFE  1.2k  curl|bash postinstall (reason)

# columns: slug  topic  official|""  SAFE|UNSAFE|UNKNOWN  installs  description
# header is omitted by default for piping; add --header to include it
```

Why no `--json`: JSON repeats keys per row (`"slug":` 7 bytes × N) and escapes, wasting tokens for LLMs and bytes for piping. The TSV line is ~40% smaller and still losslessly conveys `safe/official/topic/installs`. If structured output is needed, agents can use `--header` + `awk -F'\t'`.

Survey fallback: if `query==""` and TTY, `survey.Input{Message:"Search skills:"}` then debounced live search (150 ms) like upstream; after select, call `add` with same gating.

Non-TTY with no query → error: "query required in non-interactive mode".

---

### 9. Security Model: Password-Gated Risky Installs & Human-Only Trust

#### 9.1 Threat model

- **Agent adversary:** An AI agent (Claude Code, Codex, Cursor, OpenCode) running as the same Unix UID, non-interactive (no TTY, `CI=1`, `GITHUB_ACTIONS`, etc.), tries to `mycli get risky/skill -y` or flip trust to bypass.
- **Assumption:** Same-UID file access cannot be blocked by `chmod` alone. Absolute isolation requires OS user separation or keychain — out of scope. We provide **best-effort hardening**: degrade to "requires human password proof" and make file-edit bypass detectable and non-trivial.

#### 9.2 When password is required

```
if verdict.Safe == false || verdict.Unknown == true {
    if isOfficial { // still requires password unless trusted — strict policy
        // show OFFICIAL ✓ + UNSAFE in red, do NOT auto-bypass
    }
    if IsTrustEnabled() {
        allow // global trust bypasses per-install prompt
    } else {
        requirePassword()
    }
}
```

Official exception: **strict** — `official` is curation, not security proof. A risky official still gates on password unless `trust enable`. Lenient mode (`official && severity != critical → allow`) is rejected.

#### 9.3 `requirePassword()` flow

**Non-interactive ⇒ fail-closed:**

```go
func isInteractiveTTY() bool {
    fi,_ := os.Stdin.Stat()
    return fi.Mode()&os.ModeCharDevice != 0 && term.IsTerminal(int(os.Stdin.Fd()))
}
func isAgentEnv() bool {
    // CI, GITHUB_ACTIONS, GITLAB_CI, AGENT, CLAUDECODE, CURSOR_AGENT, OPENCODE, VSCODE_AGENT
}
if !isInteractiveTTY() || isAgentEnv() || viper.GetBool("non-interactive") || viper.GetBool("yes") {
    return fmt.Errorf("UNSAFE skill %q requires human password. Re-run in TTY as human or `mycli trust enable` (TTY required).", slug)
}
```

Exit 1, no survey. Message guides to `trust enable`.

**Interactive:**

1. If `security.passwordHash == ""` → `survey.Password{Message:"Create install password (for risky skills):"}` + confirm, `bcrypt.GenerateFromPassword(pw, 12)`, save to `~/.mycli/config.yaml` (0600, dir 0700) via `viper.WriteConfigAs` + `os.Chmod`.
2. Else → `survey.Password{Message:"Enter password to install UNSAFE skill 'X' (reason: Y):"}` → `bcrypt.CompareHashAndPassword`. 3 retries then abort.

Storage:

```yaml
security:
  passwordHash: "$2a$12$..."
  passwordSetAt: "2026-08-28T15:00:00Z"
  trustEnabled: false
  trustEnabledAt: "2026-08-28T..."
```

`bcrypt` only, never plaintext. TTY-gated writes only.

#### 9.4 Global trust: `mycli trust`

```
mycli trust enable   # human once, TTY required, password proof
mycli trust disable  # requires password
mycli trust status   # prints Trust: enabled/disabled, Password: set/not set (never hash)
mycli trust reset    # delete hash+trust, requires TTY + type RESET
```

**`enable`:**

- Requires `isInteractiveTTY() && !isAgentEnv()` else `FATAL: trust change requires human TTY`.
- If `passwordHash==""` → create password.
- Else verify existing password.
- Set `trustEnabled=true`, `trustEnabledAt=now`, write 0600.

**Hardening beyond chmod (why chmod insufficient):**

Agent has same UID → can `read`/`write` `~/.mycli/config.yaml` regardless of 0600/0700. So:

1. **No env bypass:** `viper.BindEnv` must NOT bind `trustEnabled`/`passwordHash`. Only file.
2. **TTY-gated mutations:** Any `trustEnabled` write in code checks `isInteractiveTTY()`. Agent without TTY cannot call `trust enable` even as subprocess.
3. **Hash as capability:** Agent that directly edits `trustEnabled:true` without a valid bcrypt hash is ignored — code treats `trustEnabled && passwordHash=="" ⇒ trust==false`. Agent could also generate its own `bcrypt(agent123)` and set `trustEnabled:true`; that *does* bypass. Document the limit: with same-UID shell access, absolute prevention is impossible. Best-effort mitigations:
   - `trust status` warns if `config.yaml` mtime changed without recent TTY proof.
   - Recommend `chattr +i` or keychain for stronger isolation (out of scope).
   - `/trust` file alternative: store hash in OS keychain (`security` on macOS, `secret-tool` on Linux) — future.
4. **No `--yes` bypass:** `trust enable --yes` rejected.
5. **Forgot password:** `trust reset` requires TTY + typing `RESET`; no recovery (bcrypt one-way). Agent cannot reset without TTY.

---

### 10. CLI Surface

```
mycli [command] [args] [flags]

Commands:
  get, add      Resolve → Cache → Link a skill (alias: a)
                mycli get vercel-labs/agent-skills/vercel-optimize
                mycli get owner/repo --skill s1 --skill s2 --agent claude-code --global --copy
  search, find  Search with safe/unsafe signal (topic/official like web)
                mycli search react --topic react
                mycli search --topic nextjs --official
                mycli search nextjs --topic nextjs --official --owner vercel
  list, ls      List installed skills [--global --agent]
  remove, rm    Remove installed skills
  update        Update to latest (re-fetch + re-link) [--global --project -y]
  cache         Cache subcommands: gc, path, clean
  trust         Trust subcommands: enable, disable, status, reset
  version       Print version

Flags (global):
  --config <path>     config file (default ~/.mycli/config.yaml)
  --cache-dir <path>  cache dir (default ~/.cache/mycli)
  -y, --yes           skip confirm (still fails closed for risky without trust)
  --no-color          disable color
  -h, --help          help
  -v, --version       version
```

**`get/add` flags:** `-g/--global`, `-p/--project`, `-a/--agent <list|*>`, `-s/--skill <list|*>`, `-l/--list` (list skills without installing), `--ref <branch|tag|sha>`, `--copy`, `--link-mode symlink|copy|auto`, `--force` (refresh cache), `--full-depth`, `--subagent`.

**`search` flags:** `[query]`, `--topic` (react|nextjs|design|mobile|agent-workflows|databases|testing|marketing|all), `--official` (only https://www.skills.sh/official), `--owner`, `--limit`, `--header` (print TSV header). No `--json` — output is compact text by design.

Keep upstream aliases (`a`, `ls`, `rm`) for muscle memory.

---

### 11. Config & Filesystem Layout

**Precedence:** `flag > env (MYCLI_*) > config.yaml > default`.

**Files:**

```
~/.mycli/config.yaml          # 0600, viper, primary
~/.cache/mycli/repos/...      # 0755, sparse clones
~/.cache/mycli/tmp/           # staging
~/.agents/skills/<skill>      # global canonical (universal)
~/.claude/skills/<skill>      # per-agent global
./.agents/skills/<skill>      # project
~/.agents/.skill-lock.json    # global lock v3
./skills-lock.json             # project lock v1
```

**Select viper defaults:**

```yaml
cache: { dir: "", ttl: 24h, background_update: true, strategy: fetch, shallow: true, filter: "blob:none", gc: {enabled:true, interval:24h, max_age:30d, max_size:2GB} }
sparse: { enabled:true, cone:true }
link: { mode: auto, targets: [claude, agents, project] }
git: { bin: git, timeout: 60s }
security: { require_password_for_risky:true, trust_all:false, password_hash:"", salt:"" }
```

`GITHUB_TOKEN` / `GH_TOKEN` / `gh auth token` used for private repos (resty + git credential).

---

### 12. API Contracts

| Endpoint | Method | Params | Used For |
|---|---|---|---|
| `https://skills.sh/api/search` | GET | `q, limit, owner` | Search (client filters topic/official) |
| `https://skills.sh/api/download/{owner}/{repo}/{slug}` | GET | — | Metadata + file list for `get` (blob path) |
| `https://add-skill.vercel.sh/audit` | GET | `source, skills=csv` | Safe/unsafe verdict (3s timeout) |
| `https://api.github.com/repos/{owner}/{repo}` | GET | — | Private check (optional) |
| `https://www.skills.sh/topic` | GET (HTML) | — | Topic scrape for `--topic` validation |
| `https://www.skills.sh/official` | GET (HTML/JSON) | — | Official allowlist |

All tunneled via `resty` with `SetTimeout`, `SetRetryCount(2)`, `SetRetryWaitTime`.

---

### 13. Failure Modes & Observability

| Failure | Detection | Response |
|---|---|---|
| Skill not found | 404 from search/download | "not found, try `mycli search <query> --topic`" |
| Private repo 401/404 | GitHub API / git clone 128 | "private repo, set GITHUB_TOKEN or `gh auth login`" |
| Audit timeout | 3s context | `UNKNOWN` → fail-closed for install, yellow for search |
| Git shallow/filter unsupported | stderr `unknown option` | Retry without filter, warn |
| Sparse path not in repo | `sparse-checkout set` exit 1 | "skill not found at ref <ref>" |
| No TTY for risky install | `isInteractiveTTY==false` | Error, guide to TTY or `trust enable` |
| Agent tries `trust enable` | `isAgentEnv||!isTTY` | `FATAL: trust change requires human TTY` |
| Symlink no privilege (Windows) | `os.Symlink` error | Fallback copy, warn |
| Cache corrupt | `git status` fails | `rm -rf` + re-clone under lock |
| Network fetch fails (stale) | `fetch` 128 | Serve stale if exists |

Observability: `mycli --verbose` (`viper.GetBool("verbose")`) logs resty requests (redacted token), git commands (stderr), audit latency. `mycli cache path` prints resolved dirs for debugging.

---

### 14. Migration & Compatibility

- **From `npx skills`:** `mycli` accepts same shorthand (`owner/repo`, `owner/repo@skill`, `github:`, `well-known`, `local`) and same flags (`-g, -a, -s, -y, --copy`). Help text mirrors upstream for familiarity. No `node_modules` scan — `experimental_sync` is dropped (out of loop); use `mycli get` explicitly.
- **To `mycli`:** Users alias `alias skills=mycli` or `mycli get` directly. AI agents use `mycli search <q> --topic <t> --official` like the web and pipe compact TSV (`cut -f1`) vs `npx skills add`.
- **Lockfiles:** Keep reading/writing `skills-lock.json` v1 and `.skill-lock.json` v3 for interop; both tools can coexist.
- **Tokens:** Reuse `GITHUB_TOKEN` / `gh` credential — no new auth.
- **Install:** `go install skill.sh/mycli@latest`, `brew install mycli`, or `curl -fsSL https://skills.sh/install.sh | sh` (future).

---

### 15. Scope of Improvement Beyond the Loop

The three-phase loop is intentionally small. Improvements outside it but within scope:

1. **Performance:** Go binary <50 ms startup vs ~400 ms Node; sparse cache avoids re-clones (repo 100 MB → skill 50 KB, 2000× savings); `fetch --depth 1` vs full clone.
2. **Disk:** GC (`max_age`/`max_size` LRU) + tmp staging cleanup; upstream tmpdirs linger on crash.
3. **UX:** Compact text over JSON (agents save tokens/bytes, `cut`/`awk` parseable); pagination (`--limit/--page`); `cache gc --dry-run`; `trust status`; `--header` for TSV.
4. **Security:** Fail-closed audit, password gate, human-only trust, `UNKNOWN` treated as unsafe. Upstream advisory table never blocks.
5. **Discoverability:** `--topic` / `--official` make https://www.skills.sh/topic and https://www.skills.sh/official first-class; upstream `owner` filter only.
6. **Windows:** `symlink` fallback + `auto` mode detection.
7. **Future (out of v1):** Keychain-backed trust, `SKILLS_API_URL` mock server for offline tests, `mycli doctor` (git version, token, cache health), `mycli audit <skill>` standalone.

**Explicitly out of scope:** Dependency solving, semver ranges, plugin marketplace, `node_modules` sync, well-known schema migration.

---

### 16. Implementation Roadmap

**Phase 0 — Scaffolding (week 1)**

- `go mod init skill.sh/mycli`, `go get cobra viper resty survey`, `golang.org/x/crypto`
- `cmd/root.go` (viper init, paths, `PersistentPreRun`), `cmd/get.go`, `cmd/search.go`, `cmd/trust.go`, `cmd/cache.go`
- `internal/config`, `internal/api`, `internal/cache`, `internal/git`, `internal/link`, `internal/security`, `internal/ui`
- `runGit` helper, `isInteractiveTTY`, `isAgentEnv`

**Phase 1 — Resolve + Cache (week 2)**

- Port `parseSource` + tests against upstream fixtures
- `api.Search` (resty), `api.Download`, `api.Audit`
- Sparse cache: `Cache.Ensure`, `Cache.CloneOrFetch`, `sparse-checkout set`, `meta.json`, flock

**Phase 2 — Link (week 2)**

- `link.Install` (symlink/copy, 75-agent map, `skillFolderHash`)
- Lockfile read/write compat, `copyDirectory`/`createSymlink`

**Phase 3 — Search + Security (week 3)**

- `search.Render` table/TSV + `SAFE/UNSAFE/UNKNOWN` + `--topic/--official` (web parity, no JSON)
- `security.RequirePassword`, `security.IsTrustEnabled`, `trust enable/disable/status/reset`
- 0600/0700 enforcement, `bcrypt` cost 12, 3-retries, TTY + agentEnv gates

**Phase 4 — Polish (week 4)**

- GC (`cache gc`), `cache path`, `--verbose`, shell completions (`cobra.GenBashCompletion`)
- `go vet` / `golangci-lint` / `brew` formula / `goreleaser`
- Whitepaper → README, `--help` copy, integration tests (`TestFetchStoreLink` with `httptest` + temp git repo)

---

### 17. Open Questions

1. **Server-side topic/official API:** Should `GET /api/search` natively support `?topic=&official=` instead of client-side filtering? Proposal to upstream: add query params to avoid over-fetch.
2. **Audit scoring:** Current `socket/snyk/zeroleaks` aggregation has no documented threshold. Define `SAFE` as `all == safe/low && score ≥ 80`? Need upstream spec or empirical calibration.
3. **Keychain vs file:** Is `~/.mycli/config.yaml` 0600 sufficient for trust, or should v1 ship with keychain (`github.com/zalando/go-keyring`) from day one?
4. **Sparse cone vs non-cone:** Skills nested under `skills/category/name` require `--no-cone` which is slower. Benchmark cone depth vs correctness.
5. **Git version floor:** Sparse + `filter=blob:none` requires Git ≥2.25 / 2.19. What is the minimum supported version and fallback UX?

---

### 18. References

- `skills@1.5.23` — `https://registry.npmjs.org/skills/-/skills-1.5.23.tgz` — `dist/cli.mjs` (7726 lines), `bin/cli.mjs`, `package.json` (`tar@^7.5.20`, `yaml@^2.8.3`)
- https://skills.sh — search API (`/api/search?q=&limit=&owner=`), download API (`/api/download/{owner}/{repo}/{skill}`)
- https://add-skill.vercel.sh/audit — audit (`?source=&skills=`), telemetry (`/t`)
- https://www.skills.sh/topic — Frontend (React, Next.js, Design, Mobile), Agent workflows, Databases, Testing, Marketing
- https://www.skills.sh/official — ~150 official owners (Anthropic, Vercel, Cloudflare, Supabase, … 1500+ repos)
- https://github.com/vercel-labs/skills — upstream source, `SKILL.md` discovery, `.well-known/agent-skills`
- `spf13/cobra` — https://github.com/spf13/cobra
- `spf13/viper` — https://github.com/spf13/viper
- `go-resty/resty` — https://github.com/go-resty/resty
- `AlecAivazis/survey` — https://github.com/AlecAivazis/survey
- `x/crypto/bcrypt` — https://pkg.go.dev/golang.org/x/crypto/bcrypt
- Git sparse-checkout — `git clone --filter=blob:none --sparse`, `git sparse-checkout set --cone`
- XDG Base Dir — https://specifications.freedesktop.org/basedir-spec/

---

### Appendix A: Git Command Sequences

**First install (miss):**

```bash
tmp=$(mktemp -d $CACHE/tmp/clone-XXXX)
git clone --filter=blob:none --sparse --depth 1 --single-branch --branch <ref> <url> $tmp
git -C $tmp sparse-checkout set --cone <skill-path>
git -C $tmp rev-parse HEAD > .mycli-meta.json:commitSha
mv $tmp $CACHE/repos/github.com/<owner>/<repo>/<ref>--<hash>/
```

**Second skill same repo@ref (sparse expansion):**

```bash
git -C $CACHE/... sparse-checkout set --cone <existing...> <new-skill>
```

**Update (incremental, no re-clone):**

```bash
git -C $CACHE/... fetch --depth 1 origin <ref>
git -C $CACHE/... reset --hard FETCH_HEAD
git -C $CACHE/... rev-parse HEAD
```

**Fallback (old git):**

```bash
git clone --depth 1 --sparse <url> $tmp   # no --filter
git clone --depth 1 <url> $tmp            # no sparse
```

---

### Appendix B: Config Example

`~/.mycli/config.yaml` (0600, `~/.mycli` 0700):

```yaml
cache:
  dir: "" # default ~/.cache/mycli
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

Env overrides: `MYCLI_CACHE_DIR`, `MYCLI_DOT_DIR`, `GITHUB_TOKEN`, `SKILLS_API_URL`, `SKILLS_DOWNLOAD_URL`, `GH_HOST`.

---

*End of whitepaper. Next step: `go mod tidy && mycli --help`.*
