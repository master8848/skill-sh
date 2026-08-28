# Config Reference

Viper precedence: `flag > env (MSKILL_*) > config.yaml > default`.

## Files & Permissions

```
~/.mskill/                  0700  # dot dir
~/.mskill/config.yaml       0600  # primary config (viper WriteConfigAs 0600, os.Chmod 0600)
~/.mskill/trust.json        0600  # optional split file for security fields
~/.cache/mskill/            0755  # $XDG_CACHE_HOME/mskill else ~/.cache/mskill
~/.cache/mskill/repos/...         # sparse clones + .mskill-meta.json + .lock
```

`internal/config.ResolvePaths()` resolves `CacheDir`, `DotDir`, `ConfigFile`, `TrustFile`. Per-entry `flock` on `.../.lock` (30s timeout) and atomic `tmp/clone-XXXX → mv` for concurrency.

## Example `~/.mskill/config.yaml` (Appendix B)

```yaml
cache:
  dir: "" # default ~/.cache/mskill (overridden by MSKILL_CACHE_DIR or --cache-dir)
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

Viper keys: `cache.dir`, `cache.ttl` (`GetDuration`), `cache.background_update`, `cache.strategy`, `cache.shallow`, `cache.filter`, `cache.gc.enabled`, `cache.gc.interval`, `cache.gc.max_age`, `cache.gc.max_size`, `sparse.enabled`, `sparse.cone`, `link.mode`, `link.targets`, `git.bin`, `git.timeout`, `security.require_password_for_risky`, `security.trust_all`, `security.password_hash`, `security.password_set_at`, `security.trust_enabled_at`, plus `verbose` for `mskill --verbose` (resty + git stderr).

## Env Overrides

| Env | Viper key / flag | Notes |
|-----|------------------|-------|
| `MSKILL_CACHE_DIR` | `cache.dir` / `--cache-dir` | Overrides XDG fallback |
| `MSKILL_DOT_DIR` | `dotDir` | Default `~/.mskill` |
| `MSKILL_CONFIG` | `--config` | Full path to `config.yaml` |
| `SKILLS_API_URL` | `api.base` | Default `https://skills.sh` |
| `SKILLS_DOWNLOAD_URL` | `download.base` | Default `https://skills.sh` |
| `GH_HOST` | `github.host` | Enterprise host handling (upstream parity) |
| `GITHUB_TOKEN` / `GH_TOKEN` | — | Private repo access (resty + git credential; `gh auth token` fallback) |
| `NO_COLOR` | — | Disables color (also non-TTY) |
| `CI`, `GITHUB_ACTIONS`, `GITLAB_CI`, `AGENT`, `CLAUDECODE`, `CURSOR_AGENT`, `OPENCODE`, `VSCODE_AGENT` | `isAgentEnv()` | Forces fail-closed / blocks `trust enable` |

`security.password_hash` and `security.trustEnabled` are **not** bound to env — file-only, TTY-gated writes.

## Precedence Details

1. **Flag** — `--config`, `--cache-dir`, `-y/--yes`, `--no-color`, per-command flags.
2. **Env** — `MSKILL_*` via `viper.BindEnv` + `AutomaticEnv` with prefix `MSKILL` and `.` → `_` replacer.
3. **Config file** — `~/.mskill/config.yaml` (or `MSKILL_CONFIG`). `viper.SetConfigName("config")`, `SetConfigType("yaml")`, `AddConfigPath(DotDir)`.
4. **Default** — `viper.SetDefault(...)` as in the YAML above. Missing file is not an error.

Writes use `viper.WriteConfigAs(ConfigFile)` followed by `os.Chmod(ConfigFile, 0600)` and `os.Chmod(DotDir, 0700)` to enforce permissions even when Viper creates the file with looser defaults. `security.password_hash` is `bcrypt` cost 12, never plaintext.

## XDG & Fallbacks

- `CacheDir = $XDG_CACHE_HOME/mskill` if set, else `~/.cache/mskill`.
- `DotDir = ~/.mskill` (0700) unless `MSKILL_DOT_DIR` is set.
- `ConfigFile = $DotDir/config.yaml` unless `MSKILL_CONFIG` is set.
- `link.mode=auto` → symlink on Unix, copy on Windows; `GH_HOST` defaults to `github.com`.
