# Competitors & Multi-Registry Plan — mskill Reference

> Status: **Reference only — do not implement now.** Review later. Collects what each marketplace/Intent solves, what mskill should borrow/improve, how to add, and detailed backlog. Source of truth for later PRs.

Related: `docs/CLI.md:1`, `docs/CONFIG.md:1`, `docs/whitepaper.md:1`, `README.md:1`

---

## 1. What TanStack Intent solves (and does not)

**URL:** https://tanstack.com/intent/latest/docs/overview

**Core idea:** Skills as **npm package artifacts** versioned with library releases, not git clones. Consumer `list|load`, maintainer `scaffold|validate|stale`.

| Command | What it does | Scan? |
|---|---|---|
| `npx @tanstack/intent list` | Discovers `skills/` from `node_modules` + workspaces + Yarn PnP (`.pnp.cjs` runtime, no code exec) | full scan `scanForIntents` |
| `load @pkg#skill [--path]` | Resolves `SKILL.md` for installed version (fast-path direct `node_modules/<pkg>/package.json` ancestor + single `SKILL.md` read, `IntentFsCache`; full scan on miss) | fast-path else full |
| `install` | Writes lightweight `<!-- intent-skills:start -->` guidance to `AGENTS.md`/`CLAUDE.md`/`.cursorrules`/`.github/copilot-instructions.md` — default **no scan**; `--map` adds `id/run/for` mappings with scan | no / yes |
| `hooks install [--scope project|user] [--agents claude,codex,copilot]` | `SessionStart` skill catalog + `PreToolUse` edit gate that records `intent list|load` observation | at session start |
| `scaffold`/`validate`/`stale` | Domain interview + `SKILL.md` format + package coverage + version drift | local only |
| `exclude`/`intent.skills`/`intent.exclude` | Allowlist `intent.skills` (`pkg`, `workspace:pkg`, `@scope/*`, `*`) + blocklist `intent.exclude` (`pkg`, `pkg#skill`, `*#glob*`) — `Trust model` 6 stages, static discovery, `git:` reserved | post-filter |

**Strengths to borrow:**
- **Versioned with code:** `skills/` ships in npm tarball `files:["skills/"]` — no drift between docs and installed package.
- **Static discovery invariant:** Never `import`/`require` package code; ESLint `intent/static-discovery`.
- **Policy before scan:** `checkLoadAllowed` fails before any FS scan for `intent.exclude`/non-allowlisted pkg — saves cost.
- **Fast-path load:** `depDirCache` + `fsIdentity` cache makes `load` `O(1)` probes, not `O(node_modules)` walk.
- **Split `resolve --path` vs `read`:** Agents probe existence cheaply.
- **Explicit global:** `--global` opt-in, local precedence.
- **Zero-scan `install`:** Guidance block is cheap.

**Weaknesses / not solving:**
- No git sparse clone (relies on package manager fetch) — cannot install arbitrary `owner/repo/skill`.
- No audit `SAFE/UNSAFE` (relies on npm trust + allowlist).
- Node-only; no Go binary, no fail-closed password, no `sparse`/`blob:none`.
- Workspace resolution Yarn PnP specific; no `bun`/`pnpm` store special-case.
- No TSV agent-parseable search ranking by installs.

**What mskill should steal (low-cost):** `intent.skills`/`exclude` semantics for npm discovery, `validate`/`stale` cmds, `hooks` session catalog pattern, `fast-path load` + `IntentFsCache`, `install` guidance block.

---

## 2. Full competitor map

### 2.1 skills.sh legacy (current mskill backend)

- **URL:** `https://skills.sh` (Vercel Labs), API `GET {SKILLS_API_URL}/api/search?q=&owner=&limit=20`
- **Auth:** none + optional `Bearer $GITHUB_TOKEN|$GH_TOKEN`
- **Ref:** `owner/repo[/skill][@filter][#ref]` → `https://github.com/owner/repo.git` + sparse checkout `SKILL.md` depth 5 (`internal/cache/cache.go:878`)
- **Format:** `agentskills.io` `SKILL.md` (yaml frontmatter `name,description`, `scripts/,references/,assets/`)
- **Fetch:** `git clone --filter=blob:none --sparse --depth 1 --single-branch` (`internal/cache/cache.go:199`)
- **Audit:** `GET {AUDIT_URL}/audit?source=owner/repo&skills=slug` (`internal/api/api.go:155`, 3s timeout) → `SAFE/UNSAFE/UNKNOWN`
- **Solves:** Central discovery + install ranking + audit signal.
- **Gap:** Single tenant, no multi-registry, `SKILLS_DOWNLOAD_URL` documented (`README.md:89`, `CONFIG.md:59`) but dead code (0 hits), `api.base` viper key documented but `internal/config/config.go:112` never sets it.
- **Add cost:** **1** — env swap only.

### 2.2 skills.sh v1 (documented evolution)

- **Endpoints:** `GET /api/v1/skills/search?q=&limit=&owner=` → `{data:[{id,slug,name,source,sourceType,installUrl,installs}],query,searchType,durationMs,pagination}`, `GET /api/v1/skills/{id}` → `{id,source,slug,hash,files:[{path,contents}]}`, `/audit`
- **Auth:** `Bearer Vercel OIDC` (`@vercel/oidc` `getVercelOidcToken()`, 600 req/min, `x-vercel-oidc-token` alt)
- **Solves:** `files[]` direct HTTP content (no git needed), version hash, well-known installUrl.
- **Weakness:** Still `owner/repo` model; no ZIP versioning.
- **Add cost:** **1** — new adapter mapping `data→Skill`, add `VERCEL_OIDC_TOKEN`, optionally use `hash/files` for cache invalidation without git.

### 2.3 mastra-ai/skills-api (self-host fork)

- Same as v1 but paginated `query,owner,repo,sortBy,page,pageSize`, `GET /:id/content`. Solves self-hosting. Cost **1**.

### 2.4 ClawHub (OpenClaw)

- **URL:** `https://clawhub.ai` (`openclaw/clawhub`), discovery `/.well-known/clawhub.json`, CLI `clawhub install @openclaw/demo`.
- **API:** `GET /api/v1/search` (vector `text-embedding-3-small`), `GET /api/v1/skills`, `GET /api/v1/skills/{slug}`, `POST /api/v1/skills` multipart; `Authorization: Bearer` from `~/Library/App Support/clawhub/config.json` (`CLAWHUB_REGISTRY` override)
- **Ref:** `@publisher/slug` or `slug` (global namespace), versioned `semver+tags[latest]`
- **Format:** `SKILL.md` + `openclaw.compat.pluginApi` nix pointer
- **Fetch:** **ZIP per version** `/api/v1/skills/{slug}/download` (hosted, not git)
- **Solves:** Versioned registry, vector search, namespaced publishing, ZIP distribution.
- **Gap:** Different identity (not `owner/repo`), needs ZIP unpack path.
- **Add cost:** **2-3** — ZIP resolver reusing `internal/resolve/resolve.go:203` `download` type + `cache.Ensure` artifact branch.

### 2.5 Hermes Registry (`hermesonehq/hermes-registry`)

- **URL:** `https://registry.hermesone.org/index.json` (built by `scripts/build_index.py`), `hermes skills search --source skills-sh|well-known|github|clawhub|lobehub|browse.sh`
- **Ref:** `category/skill-name` (`skills/<category>/<name>/SKILL.md`)
- **Format:** `agentskills.io` verbatim (`metadata.hermes.*` ignored)
- **Fetch:** git if `sourceUrl` GitHub else raw `SKILL.md` URL
- **Solves:** Curated curated index + federation via `skills_hub.py`; solves marketplace aggregator role Hermes Hub currently fills.
- **Add cost:** **2** — fetch `index.json`, client-side filter, reuse `cache.Ensure` if git.

### 2.6 Hermes Hub (Nous Research)

- CLI wraps all sources as adapters (skills.sh, well-known, github, clawhub, lobehub, browse.sh) via `tools/skills_hub.py`. Proves federation works with thin adapter layer. Cost **2** delegated.

### 2.7 Cloudflare Well-Known (`agent-skills-discovery` RFC 0.2)

- **URL:** `GET https://{domain}/.well-known/agent-skills/index.json` (fallback `/.well-known/skills/index.json` v0.1), `GET /.../skills/<name>/SKILL.md`, `llms.txt`
- **Ref:** `well-known:https://mintlify.com/...` or bare domain
- **Format:** same `SKILL.md` + sibling `scripts/`
- **Fetch:** Direct HTTP (site hosts)
- **Solves:** Decentralized discovery without central registry; Mintlify docs use this.
- **Status in mskill:** Already stubbed `internal/resolve/resolve.go:222` `IsWellKnownUrl` + `Type=well-known` (`resolve.go:720/853`) but `cache.Ensure` has no fetcher.
- **Add cost:** **2** — HTTP fetcher writing to `CacheDirPath` + `ListSkills` walk fallback (`cache.go:912`) already covers non-git.

### 2.8 browse.sh

- **URL:** `https://browse.sh`, `GET /api/skills/<slug>` → `{skillMdUrl, sourceUrl}`, catalog `llms.txt`
- **Ref:** `domain/path` (`facebook.com/search-marketplace...`)
- **Fetch:** `skillMdUrl` per file
- **Add cost:** **2**

### 2.9 LobeHub

- **URL:** `https://market.lobehub.com`, `npx @lobehub/market-cli skills search --q --sort createdAt|installCount`, `skills install <identifier> --version`
- **API:** `GET` paginated `{currentPage,items:[{identifier,name,author,installCount}],totalCount}`, register bearer (rate 5/30m)
- **Ref:** `identifier=owner-repo` slug
- **Fetch:** **ZIP**
- **Solves:** Category marketplace + rating + installCount ranking.
- **Add cost:** **3** token flow + pagination mapping + ZIP.

### 2.10 anthropics/skills + Claude Plugin + Claude API

- **URLs:** `github.com/anthropics/skills` (plugin `install document-skills@anthropic-agent-skills`), `POST/GET /v1/skills` `Header anthropic-beta: skills-2025-10-02` + `x-api-key`
- **Ref:** `type=anthropic|custom, skill_id=skill_xxx, version=latest`
- **Format:** `.skill` bundle multipart `files=[SKILL.md,…]`
- **Solves:** Managed bundle for Claude.
- **Gap:** Not `SKILL.md` link into `~/.agents/skills`; different artifact (`mcp.json` vs `~/.claude/skills`).
- **Add cost:** **4** — new marketplace identity, avoid for now.

### 2.11 Smithery / Glama / Official MCP Registry

- **URLs:** `https://api.smithery.ai`, `https://glama.ai/mcp`, `https://registry.modelcontextprotocol.io`
- **Artifact:** **MCP servers** (`npx -p pkg`/`uvx`/Docker, `deploymentUrl,bundleUrl,configSchema`), not `SKILL.md`.
- **Add cost:** **5** — different domain, would confuse `SkillFolderHash`/`CopyDirectory`/`link.Install`. **Do not merge** into mskill skill manager; keep separate `mcp.json` concern.

### 2.12 npm tarball distribution (Intent's world)

- **URL:** `https://registry.npmjs.org/-/v1/search?text=keywords:skills` → tarball `dist.tarball`
- **Solves:** Versioned with package, offline `node_modules` read.
- **Add cost:** **4** if full tarball unpack; but `Intent`-style **read-only `node_modules/skills/**/SKILL.md` scan** is **cost 2** and fits cache walk fallback.

### 2.13 Skyll (`api.skyll.app` / `find-skill`)

- Aggregated `skills.sh`+GitHub proxy, `GET /search?q=&owner=&limit`, `id=owner/repo/skill`, git. Alias of skills.sh. Cost **2**.

---

## 3. What mskill solves today (keep)

- **Fetch→Store→Link** `owner/repo/skill` sparse `blob:none` persistent dot cache (`~/.cache/mskill/repos/github.com/<owner>/<repo>/<ref>--<hash8>/.mskill-meta.json` `internal/cache/cache.go:42`), `fetch --depth 1 + reset --hard`, `sparse-checkout set --cone` per skill.
- **Security fail-closed:** audit `GET add-skill.vercel.sh/audit?source=...&skills=...` 3s → `SAFE/UNSAFE/UNKNOWN`, risky requires TTY `bcrypt cost 12` password via `survey.Password` (`internal/security/security.go:1`), `trust enable` TTY-gated, `CI/AGENT/CLAUDECODE` blocked, `trustEnabled && passwordHash=="" ⇒ trust==false`.
- **Search web parity:** `search --topic --official --owner --limit --header` TSV `slug topic official SAFE|UNSAFE|UNKNOWN installs description`, color-aware, `cut -f`/`awk -F'\t'` parseable (`internal/search/search.go:225`, `cmd/search.go:133`).
- **Link interop:** Same `lockfiles` v1/v3, same shorthand (`owner/repo`, `github:`, `gitlab:`, `well-known`, `local`, aliases `coinbase/agentWallet` etc `internal/resolve/resolve.go:29`), same flags `-g/-a/-s/-y/--copy`, `link.mode auto|symlink|copy` (`internal/link/link.go:28/452`).
- **Non-network:** `Resolve` never writes, `Cache` never contacts `skills.sh`, `Link` never networks.

**Preserve invariants:** startup `<5ms` (`cmd/root.go:28`), `get`/`show`/`search` hot path no `node_modules` scan, `list`/`cache` explicit.

---

## 4. What to improve (borrow, not rewrite)

### 4.1 Discovery / distribution

- **npm local discovery** (Intent fast-path) — read already-installed `node_modules/*/skills/**/SKILL.md` + workspaces `pnpm` store, depth 3, pre-filter `Stat(package.json)` has `skills` field. Solves offline + versioned-with-package for JS repos.
- **Well-known fetch** — complete `Type=well-known` fetcher (HTTP `index.json` + `SKILL.md`+`references/`) — mskill already parses it, just lacks fetcher.
- **Multi-registry federation** — pluggable search/audit behind `Registry` interface (see §6). Enables `hermes`/`clawhub`/`browse.sh` as `--registry` args without competing with git path.
- **Skills.sh v1 `files[]`** — use `hash`/`files` to validate cache without git roundtrip; supports `installUrl` well-known.
- **Hermes `index.json`** — federation proof without new API.

### 4.2 Security / trust

- **Allowlist `intent.skills` mode** for npm discovery: `package.json#mskill.skills` (or honor `intent.skills`) nearest-wins, `intent.exclude` merged root→cwd, `*` wildcard only, source-kind aware `npm` vs `workspace:` (Intent `Configuration` spec). Fail-closed like current `trust` but policy-based rather than password-only.
- **Static discovery ESLint-equivalent:** Formalize `scanner never imports package code` (load `.pnp.cjs` only if Yarn PnP) as invariant.

### 4.3 Maintainer workflow

- **`validate`**: `SKILL.md` frontmatter + packaging (`files: ["skills/"]` present) before publish.
- **`stale`**: version drift (`Meta.CommitSHA` vs `origin/HEAD` or npm `dist-tags`), source/artifact/package coverage signals.
- **`scaffold`**: guided `skill` authoring (low-priority, can defer to `npx @tanstack/intent scaffold`).

### 4.4 Consumer UX

- **`install` guidance block:** `mskill install` writes `<!-- mskill-skills:start -->` with `mskill list`/`load <pkg>#<skill>` hints into `AGENTS.md`/`.cursor/rules`/`.github/copilot-instructions.md` (Intent default no-scan).
- **`hooks` session catalog:** `SessionStart` `skill-id: description` catalog filtered by allowlist + audit; `PreToolUse` edit gate observing `mskill list|load` (not proof of activation — lifecycle 5/6 remain agent-side).
- **`load --path` split:** path-resolve without `readFileSync` for cheap probes.
- **TSV consistency:** Keep `search` TSV, add `list --json` variant parity with `intent list --json` for agent consumption.

---

## 5. Does it affect performance? (when done right: no)

| Path | Current cost | With lazy design | Naive cost |
|---|---|---|---|
| `PersistentPreRunE` any cmd | `<5ms` | same — npm scan never in root | `300ms-4s` if `Walk(node_modules)` unconditional |
| `mskill get owner/repo/skill` | network-bound `0.3-3s` | same (`if --npm {scan}` else unchanged) | `+Walk 1k-200k` |
| `mskill search "react"` | `0.5-2s` (API+audit) | same; `search --local` adds `~50ms` Walk depth 3 scope-limited | `+500ms` |
| `mskill npm discover` | — | `Walk 2-level` `ReadDir+Stat(pkg.json)` `~30-80ms` for 300 pkgs, cached `~0.5ms` via `~/.cache/mskill/npm-scan/<hash(lockfile)>.json` (mtime check) | `Walk full 4s` |
| `mskill list --npm` | `ReadDir` 26 dirs `~2ms` | + cached npm scan | same |
| `cache gc/list` | double walk `50ms-5s` | unchanged | — |

**Guards:**
- Depth budget 3 inside pkg, skip `.bin/.cache/.pnpm` unless `pnpm` via `.modules.yaml` `storeDir`.
- Per-command `FsCache` `Map<fsIdentity,parsed>` + `skillFilesCache` (Intent `discovery/fs-cache.ts`).
- Policy check before scan (`isSourcePermitted` / `isPackageExcluded`).
- Concurrency via `conc` (already indirect `go.mod:23`) capped at `GOMAXPROCS` only if `>10k` pkgs.

---

## 6. How to add — seamless, minimal change

### 6.1 Which should be new command vs flag

**Ship both (primary command + flag sugar):**

| Surface | Feels like | Scope |
|---|---|---|
| **`mskill npm discover [--depth 2] [--json]`** + alias `mskill discover --npm` | `npm ls` / `intent list` | **New command** — zero hot-path impact, cacheable, documents well |
| **`mskill npm link [--all] [--agent X] [--dry-run]`** | `link.Install` reuse with local path | **New subcommand** under `npm` |
| **`mskill get --npm` / `show --npm` / `list --npm` / `search --local`** | Sugar over above | **Flag on existing cmds** (`if Changed("npm"){discover}else{existing}`) |
| **`mskill install [--map]` / `mskill hooks catalog`** | `intent install`/`hooks` | **New commands** `install` (guidance block) + `hooks` (session catalog) |
| **`mskill validate` / `mskill stale`** | `intent validate`/`stale` | **New commands** |
| **`--registry <url>` / `MSKILL_REGISTRY` / `SKILLS_API_URL`** | `GH_HOST` precedent | **Global persistent flag** `cmd/root.go:88` + `viper` `registry.urls []string` |

### 6.2 Tiered implementation

**Tier 0 — single-registry env switch (~45 LOC, 4 files, 1 day):**

- `internal/config/config.go:113` `SetDefault("registry.url","https://skills.sh")`, `SetDefault("registry.audit_url","https://add-skill.vercel.sh")`, `BindEnv("registry.url","MSKILL_REGISTRY","SKILLS_API_URL","SKILLS_AUDIT_URL","AUDIT_URL")`
- `cmd/root.go:88` `StringVar(&registryOverride,"registry","")`, `PersistentPreRunE:28` `viper.Set("registry.url", registryOverride)` + `os.Setenv` propagation
- `internal/api/api.go:60` refactor `skillsAPIBase()`/`auditBase()` to read `viper.GetString("registry.url")` before `os.Getenv` fallback (keep compat), add `SearchWithBase(base,...)` helper
- `cmd/search.go:228` + `cmd/get.go:872` per-cmd `--registry` override, `docs/CONFIG.md:58` precedence `flag > env MSKILL_REGISTRY > SKILLS_API_URL > default`

**Tier 1 — pluggable git-backed multi-registry (~340 LOC, 7 files, 2-3 days):**

- **NEW** `internal/registry/registry.go:1` `Registry` interface + `Provider{[]Registry}` + `NewFromConfig()` reading `viper.GetStringSlice("registry.urls")` (default `["skills.sh"]`):
  ```go
  type Registry interface {
    Name() string
    BaseURL() string
    Search(ctx context.Context, q, owner string, limit int) ([]api.Skill, error)
    Audit(ctx context.Context, source string, slugs []string) (map[string]Verdict, error)
    Resolve(input string) (*resolve.Resolved, bool)
    Fetch(ctx context.Context, r *resolve.Resolved, dest string) (string,error)
  }
  ```
- **NEW** `internal/registry/skills_sh.go` — `SkillsShRegistry` wrapping `api.Search/Audit/DownloadMeta` + `resolve.ParseSkillRef` delegation; `internal/registry/skills_sh_v1.go`, `clawhub.go`, `hermes.go`, `well_known.go`, `browsesh.go` later.
- `internal/api/api.go:78` extract `SearchWithBase`, `AuditWithBase` injection points; keep thins for compat.
- `cmd/search.go:133` fan-out `registry.Provider.SearchAll` (parallel `errgroup`), merge dedup by `id`, sort by `installs`, group audit by `Skill.Registry`.
- `cmd/get.go:138` `resolve.Provider.Resolve(input)` selecting registry by prefix/host before fallback `resolve.ParseSkillRef`.
- `internal/cache/cache.go:42` `CacheKey` already `repos/<host>/owner/repo/<ref>--<hash8>` — partitions by registry host; add `Meta.Registry` field (`cache.go:25`) for GC display.
- `internal/search/search.go:93` `isOfficialSkill` → `registry.IsOfficial(Skill)` per-registry allowlist.

**Tier 2 — heterogeneous artifact (+80 LOC, 1 day):**

- Branch `internal/cache/cache.go:199` `Ensure` on `resolved.Type=="download"|"registry"|"well-known"`:
  ```go
  if resolved.Type=="well-known" || resolved.Type=="download" {
    return fetchArtifact(ctx, resolved, cachePath) // GET files[]/ZIP/raw SKILL.md, untar to CacheDirPath, SaveMeta Filter="http"
  }
  // keep existing git clone path
  ```
- Reuse `download`/`well-known` types (`resolve.go:203/222`), add `Type="registry"` if needed.
- `ListSkills` fallback walk `cache.go:912` already covers non-git unpacks (depth 5 `SKILL.md` scan).

**Total:** Tier 0 `~45` + Tier 1 `~340` + Tier 2 `~80` = ~465 LOC across `internal/registry/*`, `internal/api/api.go`, `internal/cache/cache.go`, `internal/config/config.go`, `cmd/root.go`, `cmd/search.go`, `cmd/get.go`, `internal/search/search.go` + new cmds `cmd/validate.go`, `cmd/stale.go`, `cmd/npm.go`, `cmd/install_guidance.go`, `cmd/hooks.go` (~400 LOC if added together).

---

## 7. Detailed backlog — review then schedule

### P0 — No-regret, low-risk (do first)

| # | Item | Files | Effort | Notes |
|---|---|---|---|---|
| P0-1 | **Tier 0 registry env/flag** | `config.go:113`, `root.go:88`, `api.go:60`, `search.go:228`, `get.go:872`, `docs/CONFIG.md:58` | ~45 LOC | Behind `MSKILL_REGISTRY`, `SKILLS_API_URL` alias compat |
| P0-2 | **`skills.sh v1` adapter** | `internal/registry/skills_sh_v1.go`, `api.go:78` | ~60 LOC | Map `data→Skill`, handle `VERCEL_OIDC_TOKEN`, use `hash` for `stale` |
| P0-3 | **`well-known` fetcher complete** | `resolve.go:222`, `cache.go:199`, `registry/well_known.go` | ~80 LOC | `GET index.json` + `SKILL.md`+`references/` mirroring `/.well-known/agent-skills` RFC 0.2 |
| P0-4 | **`validate` cmd** | **NEW** `cmd/validate.go`, `internal/search/search.go:342` | ~80 LOC | Check `SKILL.md` frontmatter `name,description`, `≤1MB`, path inside skill (`SanitizeSubpath` `resolve.go:46`) |
| P0-5 | **`load --path` split** | `cmd/show.go:1`, `cache.go:874` | ~20 LOC | `realpathSync` + `isResolvedPathInsidePackageRoot` guard without `readFile` |

### P1 — Local discovery (seamless JS interop)

| # | Item | Files | Effort | Notes |
|---|---|---|---|---|
| P1-1 | **`mskill npm discover` + `--npm` flag** | **NEW** `cmd/npm.go`, `internal/npm/discover.go`, `cmd/get.go:748` `discoverSkillInCache` reuse, `cache.go:912` Walk | ~180 LOC | Scope Walk `node_modules/<scope>/<pkg>` depth 3, pre-filter `package.json` has `skills`, skip `.bin/.cache`, cache by `hash(package-lock.json.mtime)`, FsCache, `TSV` output mirroring `search` |
| P1-2 | **`mskill install` guidance block** | **NEW** `cmd/install_guidance.go` | ~90 LOC | Write `<!-- mskill-skills:start -->` to `AGENTS.md:1`, `.cursor/rules`, `.github/copilot-instructions.md`, `CLAUDE.md` — default no-scan, `--map` triggers P1-1 scan |
| P1-3 | **`stale` cmd** | **NEW** `cmd/stale.go`, `cache.go:25` `Meta.CommitSHA` | ~90 LOC | Compare `LastFetch` vs `origin/HEAD` + npm `dist-tags` + artifact `hash`; report `version drift / source / artifact / package coverage` (Intent signals) |
| P1-4 | **Allowlist `intent.skills` honor** | `internal/config/config.go:112`, `internal/npm/policy.go` | ~70 LOC | Nearest-wins `intent.skills` (replace parent), `exclude` merged `root→cwd` + caller, `*` wildcard only, `workspace:` vs bare `npm` kind-aware, `hiddenSourceCount` agent-hidden as Intent |

### P2 — Federation (hermes et al.)

| # | Item | Files | Effort | Notes |
|---|---|---|---|---|
| P2-1 | **Registry interface + fan-out search** | `internal/registry/registry.go`, `api.go:133`, `search.go:121` | ~120 LOC | Parallel `SearchAll`, audit grouping by `registry`, dedup `id` |
| P2-2 | **Hermes `index.json` + ClawHub ZIP** | `registry/hermes.go`, `registry/clawhub.go`, `cache.go:199` artifact branch | ~120 LOC | `GET index.json` client filter + `GET /download` ZIP unpack to `CacheDirPath` then `link.Install:452` |
| P2-3 | **browse.sh + LobeHub** | `registry/browsesh.go`, `registry/lobehub.go` | ~80 LOC | HTTP fetch / register flow (defer LobeHub until demand) |
| P2-4 | **Yarn PnP + pnpm store handling** | `internal/npm/pnp.go` | ~60 LOC | Load `.pnp.cjs` via patched `fs` (Intent sanctioned), `pnpm` read `.modules.yaml` `storeDir` |

### P3 — Defer (prove value first)

| # | Item | Reason |
|---|---|---|
| P3-1 | `scaffold` (AI domain interview) | Use `npx @tanstack/intent scaffold` upstream; duplicate low ROI |
| P3-2 | MCP Smithery/Glama integration | Different artifact (`mcp.json`), confuse `SKILL.md` linking; keep separate |
| P3-3 | `hooks install` blocking `PreToolUse` | Requires agent `settings.json` write + OS trust gate (Codex needs review); ship `install` catalog first, blocking later if requested |

---

## 8. Decision log for reviewer

- **Do not scan `node_modules` on hot path** — only `mskill npm discover` / `--npm` / `search --local` / `install --map`. Keep `get`/`show`/`search` hot path `<5ms`+network.
- **Prefer `npm discover` command over pure flag** — discoverability + cacheability; flags as sugar keep power users seamless.
- **Prefer Tier 0 before Tier 1** — 45 LOC proves `Registry` env plumbing, unblocks v1/well-known without full interface.
- **Do not implement MCP** — Intent also reserves `git:` until pinned ref/hash; same discipline for non-skill artifacts.
- **Reuse existing walk depth 5 + `git ls-tree` hybrid** (`cache.go:878`, `search.go:342`, `get.go:806`) for any unpacked artifact — no new discovery algorithm needed.

---

## 9. References to inspect

- `cmd/root.go:28/88` — startup + global flags
- `internal/config/config.go:24/94/112/161` — paths/viper/write
- `internal/resolve/resolve.go:13/29/46/104/157/203/222/232/320/440/793/853/872` — `Resolved`, aliases, sanitizers, `ParseSkillRef` precedence 1-10
- `internal/cache/cache.go:24/42/52/114/165/199/715/844/878/954` — `Meta`, `CacheKey`, `MetaPath`, `dirSize`, `copyDir`, `Ensure`, `GC`, `List`, `ListSkills`, `Clean`
- `internal/api/api.go:35/60/67/78/155/245` — `Skill`/`AuditResult`, bases, `Search`, `Audit`, `DownloadMeta`
- `internal/search/search.go:28/93/121/225/342` — topics, `isOfficialSkill`, filters, `Render`, `ListSkillsInRepo`
- `cmd/search.go:28/125/133/156` — topic aliases, `fetchLimit*2`, `Search` call, audit grouping
- `cmd/get.go:138/336/445/520/599/748/806` — resolve→cache→link→audit→hash→discover helpers
- `cmd/show.go:19` — `stripYAMLFrontmatter`
- `internal/link/link.go:28/93/190/258/403/452/612` — `Agents`, `ResolveDestinations`, `SkillFolderHash`, `CopyDirectory`, `CreateSymlink`, `Install`, `UpdateLockfile`
- `internal/security/security.go:1`, `internal/git/git.go:16` — password gate, `git timeout 60s`
- `internal/search/search.go:28` topic allowlist vs `skills.sh/topic` parity

