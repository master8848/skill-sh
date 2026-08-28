# CLI Reference

Source: whitepaper §10. Help text is normative; flags mirror upstream (`-g/-a/-s/-y/--copy`) plus `mskill`-only additions (`--topic`, `--official`, `--show`, `--file`, `cache`, `trust`).

```
mskill [command] [args] [flags]

Commands:
  get, add      Resolve → Cache → Link a skill (alias: a)
  show, cat     View cached skill files without installing
  search, find  Search with safe/unsafe signal (topic/official like web)
  list, ls      List installed skills
  remove, rm    Remove installed skills
  update        Update to latest (re-fetch + re-link)
  cache         Cache subcommands: gc, path, clean
  trust         Trust subcommands: enable, disable, status, reset
  version       Print version

Flags (global):
  --config <path>     config file (default ~/.mskill/config.yaml)
  --cache-dir <path>  cache dir (default ~/.cache/mskill)
  -y, --yes           skip confirm (still fails closed for risky without trust)
  --no-color          disable color
  -h, --help          help
  -v, --version       version
```

## `mskill get` / `mskill add` (`a`)

Resolve → Cache → Link. One loop per skill. Resolve never writes; Cache never contacts `skills.sh`; Link never contacts network.

```
Usage:
  mskill get <skill-ref> [flags]
  mskill get owner/repo --skill <name> [--skill <name>...] [flags]
  mskill get owner/repo/skill --show [--file <path>...] [flags]

Aliases: add, a

Flags:
  -g, --global              install to user-level dirs only
  -p, --project             install to project ./.agents/skills only
  -a, --agent <list|*>      comma-separated agents or * for all (default: claude,agents,project)
  -s, --skill <list|*>      filter skills from repo (repeatable, * = all)
  -l, --list                list discoverable skills without installing
      --show                fetch+cache, print SKILL.md to stdout, no link
      --file <path>         with --show: which file(s) to print (repeatable, default SKILL.md)
      --ref <branch|tag|sha> Git ref (default: default branch)
      --copy                force copy instead of symlink
      --link-mode <mode>    symlink|copy|auto (default auto)
      --force               bypass cache.ttl and re-fetch
      --full-depth          recursive skill discovery depth 5 (discovery only)
      --subagent            subagent mode
  -y, --yes                 skip confirm (risky still fails closed)
  -h, --help                help

Examples:
  mskill get vercel-labs/agent-skills/vercel-optimize
  mskill get owner/repo --skill s1 --skill s2 --agent claude-code --global --copy
  mskill get owner/repo/skill --show
  mskill get owner/repo --skill s1 --show --file SKILL.md
  mskill get vercel-labs/agent-skills --skill vercel-optimize --ref main --force
```

Cache-view (`--show`) reuses the same security gate as install (audit → password if `UNSAFE/UNKNOWN` unless `trust enable`), then cats from `~/.cache/mskill/repos/...` without linking.

## `mskill show` / `mskill cat`

Cache-view without installing. Same Resolve → Cache path as `get --show`.

```
Usage:
  mskill show <skill-ref> [flags]
  mskill cat  <skill-ref> [flags]

Flags:
      --file <path>   file to print, repeatable (default SKILL.md)
      --list          list files in cached skill instead of printing
      --ref <ref>     Git ref
      --force         re-fetch even if cached
  -h, --help          help

Examples:
  mskill show vercel-labs/agent-skills/vercel-optimize
  mskill show vercel-labs/agent-skills/vercel-optimize --file README.md --file scripts/setup.sh
  mskill show vercel-labs/agent-skills/vercel-optimize --list
  mskill show owner/repo/skill --ref v1.2.0 --file SKILL.md
```

Paths are sanitized against `..` and must be inside the cached skill directory.

## `mskill search` / `mskill find`

Compact TSV output (no JSON). Colors: `SAFE` green, `UNSAFE` red bold, `UNKNOWN` yellow, `OFFICIAL ✓` green; disabled if `!isTTY` or `NO_COLOR`.

```
Usage:
  mskill search [query] [flags]
  mskill find   [query] [flags]

Flags:
      --topic <topic>   react|nextjs|design|mobile|agent-workflows|databases|testing|marketing|all
      --official        only https://www.skills.sh/official
      --owner <owner>   filter by owner (validated ^[a-z0-9](?:[a-z0-9-]{0,38})$)
      --limit <n>       results per page (default 20)
      --header          print TSV header (omitted by default for piping)
  -h, --help            help

Output columns (TSV, no header by default):
  slug  topic  official|""  SAFE|UNSAFE|UNKNOWN  installs  description
```

Web parity: `--topic` mirrors https://www.skills.sh/topic and `--official` mirrors https://www.skills.sh/official. Client-side filtering (over-fetch `limit*2`) until server adds `?topic=&official=`; `--owner` is passed through to `GET /api/search?q=&limit=&owner=`.

Topic/official examples:

```sh
# mirrors https://www.skills.sh/topic (react) + https://www.skills.sh/official
mskill search react --topic react --official
mskill search --topic nextjs --official
mskill search nextjs --topic nextjs --official --owner vercel
mskill search --topic databases --limit 10 --header

# machine-parseable
mskill search react --topic react --official | cut -f1,4
mskill search --official --header | awk -F'\t' '$4=="SAFE"'
```

Interactive fallback: if `query==""` and TTY, `survey.Input{Message:"Search skills:"}` with debounced live search (150 ms); non-TTY with no query → `query required in non-interactive mode`. Audits are batched per `source` (owner/repo) with 3s timeout, top-20 by installs for >10 sources.

## `mskill list` / `mskill ls`

```
Usage:
  mskill list [flags]
  mskill ls   [flags]

Flags:
  -g, --global      only global installs
  -a, --agent <name> filter by agent
  -h, --help        help

Examples:
  mskill list
  mskill list --global --agent claude
```

## `mskill remove` / `mskill rm`

```
Usage:
  mskill remove [skills...] [flags]
  mskill rm     [skills...] [flags]

Flags:
  -g, --global      only global
  -a, --agent <name> filter by agent
  -h, --help        help

Examples:
  mskill remove vercel-optimize
  mskill remove --global vercel-optimize
```

## `mskill update` (`upgrade`)

Re-fetch + re-link. Uses `fetch --depth 1 origin <ref> && reset --hard FETCH_HEAD`; tag pins skip fetch unless `--force`.

```
Usage:
  mskill update [skills...] [flags]

Aliases: upgrade

Flags:
  -g, --global      only global
  -p, --project     only project
  -y, --yes         skip confirm
      --force       force re-fetch even if tag SHA matches
  -h, --help        help

Examples:
  mskill update
  mskill update vercel-optimize --global -y
```

## `mskill cache`

```
Usage:
  mskill cache <subcommand> [flags]

Subcommands:
  gc      garbage-collect stale entries (LRU by lastAccess, respects .lock)
  path    print resolved cache/dot/config paths
  clean   remove all cached repos (alias for gc with max_age=0)

Flags for gc:
      --dry-run     show what would be deleted
  -h, --help        help

Examples:
  mskill cache path
  mskill cache gc --dry-run
  mskill cache gc
```

GC defaults: `max_age 30d`, `max_size 2GB`, respects per-entry `flock` on `.lock` and only deletes when try-lock succeeds; `tmp/*` >1h is also pruned on startup.

## `mskill trust`

TTY-gated. `enable`/`disable`/`reset` refuse if `!isInteractiveTTY() || isAgentEnv()` or `--yes`.

```
Usage:
  mskill trust <subcommand> [flags]

Subcommands:
  enable    enable global trust (requires TTY + password proof)
  disable   disable global trust (requires password)
  status    print Trust: enabled/disabled, Password: set/not set
  reset     delete hash+trust (requires TTY + type RESET)

Flags:
  -h, --help  help

Examples:
  mskill trust status
  mskill trust enable
  mskill trust disable
  mskill trust reset
```

`enable`: if `passwordHash==""` → `survey.Password` create + `bcrypt.GenerateFromPassword(...,12)` → write 0600; else verify via `bcrypt.CompareHashAndPassword` (3 retries) → set `trustEnabled=true`. `reset`: no recovery (bcrypt one-way).

## `mskill version`

```
Usage:
  mskill version [flags]
  mskill --version
  mskill -v
```
