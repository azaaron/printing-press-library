---
title: "fix(setlist-fm): repair auth set-token, doctor env-vars, and signup hint"
type: fix
status: active
created: 2026-05-20
depth: standard
target_repo: printing-press-library
origin:
  bug_report: /Users/mvanhorn/Downloads/setlist-fm-pp-cli-bugs.md
  related_pr: https://github.com/mvanhorn/printing-press-library/pull/724
---

# fix(setlist-fm): repair auth set-token, doctor env-vars, and signup hint

## Summary

The setlist-fm CLI (shipped via PR #724) has a broken auth flow. Three related bugs make the documented setup path fail silently:

1. `auth set-token` writes the API key to a dead OAuth field (`access_token`); the HTTP layer reads from `fm_api_key`, so the saved token is never used and every request returns HTTP 403.
2. `doctor` always emits `FAIL Env Vars` when the env var is unset, even when `fm_api_key` is correctly configured in `config.toml`.
3. The `doctor` hint only mentions the env var path; it does not mention `auth set-token` (the more durable config path) and does not say the key is free.

This plan ships a single library-only patch (no upstream generator change) that fixes all three bugs, drops the unused OAuth-shaped fields from the config schema (Setlist.fm has no OAuth flow), adds "free" language to the signup hint, and records the patch in `.printing-press-patches.json` following the existing `greptile-review-feedback` precedent.

## Problem Frame

Setlist.fm's API uses only the static `x-api-key` header. The generated `Config` struct was emitted from an OAuth-shaped template and carries five unused fields plus a dead `auth_header` field. The `auth set-token` command was wired to `Config.SaveTokens(clientID, clientSecret, accessToken, refreshToken, expiry)` instead of writing to the `fm_api_key` field that `Config.AuthHeader()` actually reads.

The `doctor` env-vars check treats the env var as a hard requirement instead of one of multiple valid auth sources, so a correctly-configured config-only user sees `FAIL` and assumes setup failed.

## Scope Boundaries

In scope:
- Fix `auth set-token` to persist to `fm_api_key`
- Remove unused OAuth-shaped fields from `Config` (`AccessToken`, `RefreshToken`, `TokenExpiry`, `ClientID`, `ClientSecret`, `AuthHeaderVal`) and the associated `SaveTokens` / `applyAuthFormat` helpers
- Fix `doctor` env-vars check: downgrade to `OK` when config-based auth is configured, only `FAIL` when no auth source exists at all
- Update `doctor` hint to mention `auth set-token` as the primary path and add "free" language
- Add a tracked patch entry in `.printing-press-patches.json`

Outside this PR:
- Upstream generator fix in `cli-printing-press` (the templates still emit the OAuth-shaped struct). Follow-up issue; library-side patch is the documented mechanism for fixes like this.
- Migration tooling for users with populated dead fields. `go-toml/v2` is lenient about unknown keys on read, and the next save will simply omit them; existing config files keep working.

### Deferred to Follow-Up Work
- Upstream printing-press generator template fix so future API-key-only CLIs do not emit OAuth-shaped configs

## Key Technical Decisions

1. **Library-only patch, not generator fix.** The `.printing-press-patches.json` mechanism exists for exactly this case (post-generation fixes). Precedent: the existing `greptile-review-feedback` patch entry. Upstream template fix can come later.

2. **Drop the dead OAuth fields, not just re-route the write.** The bug report explicitly recommends this. `go-toml/v2` ignores unknown TOML keys by default, so users with populated `access_token` / `refresh_token` rows keep loading without error; on next save those fields are simply dropped. This is the path with the least long-term confusion.

3. **`auth set-token` writes via a new `SaveAPIKey` method on `Config`.** Cleaner than inlining a `c.SetlistFmApiKey = ...; c.save()` block in the command file. Mirrors the existing `ClearTokens` / `save` pattern.

4. **`doctor` env-vars verdict gates on `cfg.AuthHeader()`.** When the config provides auth, env_vars status becomes `OK config provides auth` (informational, not FAIL). When neither env var nor config provides auth, the existing `ERROR missing required` message stays.

5. **Hint copy.** Replace the current `auth_hint` with a two-line block that mentions both paths and explicitly calls out that the key is free:
   ```
   Get a free API key at https://www.setlist.fm/settings/api, then run:
     setlist-fm-pp-cli auth set-token <key>   (or export SETLISTFM_API_KEY=<key>)
   ```

## Implementation Units

### U1. Re-route `auth set-token` to `fm_api_key` and drop dead OAuth fields

**Goal:** Make `auth set-token <key>` persist to `fm_api_key` so the saved token is actually read by the HTTP layer. Remove the unused OAuth-shaped fields from the `Config` struct so the config schema reflects the API's actual auth model.

**Dependencies:** None (starting point).

**Files:**
- `library/media-and-entertainment/setlist-fm/internal/config/config.go` (modify)
- `library/media-and-entertainment/setlist-fm/internal/cli/auth.go` (modify)
- `library/media-and-entertainment/setlist-fm/internal/config/config_test.go` (create)

**Approach:**
- Remove from `Config` struct: `AuthHeaderVal`, `AccessToken`, `RefreshToken`, `TokenExpiry`, `ClientID`, `ClientSecret`. Keep `BaseURL`, `AuthSource` (computed), `Path` (computed), and `SetlistFmApiKey`.
- Replace `SaveTokens` and `ClearTokens` with a single `SaveAPIKey(key string) error` method and a `ClearAPIKey() error` method. Both delegate to the existing `save()` helper.
- Simplify `AuthHeader()` to `return c.SetlistFmApiKey`. Remove `applyAuthFormat` and the trailing `var _ = strings.ReplaceAll` since neither is used anymore.
- In `auth.go`:
  - `newAuthSetTokenCmd` calls `cfg.SaveAPIKey(args[0])` and removes the legacy `AuthHeaderVal = ""` clear (now unnecessary).
  - `newAuthLogoutCmd` calls `cfg.ClearAPIKey()`.
- Verify the `AuthSource` field is set when the value comes from config (currently only set from env vars). Add an `AuthSource = "config"` assignment in `Load()` when `cfg.SetlistFmApiKey != ""` after TOML unmarshal AND env vars did not override.

**Patterns to follow:** Mirror the existing `save()` private method pattern (already in `config.go`). Mirror the test layout in `internal/cli/since_test.go` and `internal/cli/which_test.go` (package-level, standard-library `testing`, no third-party assertion lib).

**Test scenarios:**
- Happy path: `SaveAPIKey("abc123")` writes `fm_api_key = "abc123"` to disk and a subsequent `Load()` returns a Config with `SetlistFmApiKey == "abc123"` and `AuthHeader() == "abc123"`.
- Backward-compat read: a TOML file containing legacy keys (`access_token = "..."`, `auth_header = "..."`, `refresh_token = "..."`) loads without error and is treated as unauthenticated when `fm_api_key` is empty.
- Round-trip drops dead fields: after `SaveAPIKey`, re-reading the file shows `fm_api_key` only; the legacy field rows are not re-emitted.
- `ClearAPIKey` empties `fm_api_key` and a subsequent `AuthHeader()` returns `""`.
- Env-var precedence preserved: when `SETLISTFM_API_KEY` is set, `Load()` overrides `SetlistFmApiKey` from env and sets `AuthSource = "env:SETLISTFM_API_KEY"`.
- Config-source AuthSource: when only `fm_api_key` is set in TOML and no env var is exported, `AuthSource == "config"`.

**Verification:** `go test ./internal/config/... ./internal/cli/...` passes. Manual smoke: fresh `~/.config/setlist-fm-pp-cli/config.toml`, run `setlist-fm-pp-cli auth set-token TESTKEY`, then `cat` the file and confirm `fm_api_key = "TESTKEY"` and no `access_token` / `auth_header` lines.

---

### U2. Fix `doctor` env-vars verdict when config provides auth

**Goal:** Stop reporting `FAIL Env Vars` when `fm_api_key` is set in config. The env var is one of multiple valid auth sources, not a hard requirement.

**Dependencies:** U1 (so `cfg.AuthHeader()` is the canonical check).

**Files:**
- `library/media-and-entertainment/setlist-fm/internal/cli/doctor.go` (modify)
- `library/media-and-entertainment/setlist-fm/internal/cli/doctor_test.go` (create)

**Approach:**
- In `newDoctorCmd`, after the existing env-var detection block, branch on whether `cfg != nil && cfg.AuthHeader() != ""` (i.e. some auth source is configured):
  - If an env var is set, keep the current `OK %d/%d available` message.
  - If no env var is set but config has `fm_api_key`, set `report["env_vars"] = "OK config provides auth (env var not required)"`.
  - If neither is set, keep the current `ERROR missing required: ...` message.
- The existing color-mapping `switch` in the human-readable block already turns `OK`-prefixed strings green and `ERROR`-prefixed strings red, so no extra mapping is needed.

**Patterns to follow:** Match the existing `switch` style and message format inside `newDoctorCmd`. Tests should construct a `*cobra.Command` and capture stdout via `cmd.SetOut(&buf)`, following the style in `since_test.go`.

**Test scenarios:**
- Happy path (config-only): with `fm_api_key` set and no env var, `doctor` produces `OK Env Vars: OK config provides auth (env var not required)` and exit code 0.
- Env-var path unchanged: with `SETLISTFM_API_KEY` set, `doctor` produces `OK Env Vars: OK 1/1 available`.
- Both unset: with neither configured, `doctor` still produces `FAIL Env Vars: ERROR missing required: SETLISTFM_API_KEY (or SETLIST_FM_API_KEY)`.
- `--fail-on error` does not trip on the config-only happy path.
- JSON output: `env_vars` field has the new `OK config provides auth ...` string in the config-only case.

**Verification:** `go test ./internal/cli/...` passes. Manual smoke: with `fm_api_key` set in config and `unset SETLISTFM_API_KEY SETLIST_FM_API_KEY`, `setlist-fm-pp-cli doctor` shows no `FAIL` line.

---

### U3. Update `doctor` hint copy: free key + `auth set-token` primary path

**Goal:** Replace the current single-line env-var-only hint with a two-line block that puts `auth set-token` first, mentions the env var as the alternative, and explicitly says the key is free.

**Dependencies:** None on U1/U2 in code terms, but logically belongs with the doctor fix.

**Files:**
- `library/media-and-entertainment/setlist-fm/internal/cli/doctor.go` (modify)
- `library/media-and-entertainment/setlist-fm/internal/cli/doctor_test.go` (extend from U2)

**Approach:**
- Change `report["auth_hint"]` to the two-line form, e.g.:
  ```
  Get a free API key at https://www.setlist.fm/settings/api, then run:
    setlist-fm-pp-cli auth set-token <key>   (or export SETLISTFM_API_KEY=<key>)
  ```
- Render the multi-line hint in the human-readable output by emitting each line on its own indented row, so it visually nests under the `Auth` indicator. The existing `fmt.Fprintf(w, "  hint: %v\n", hint)` line takes a single string; split on `\n` before emitting to keep the formatting clean.
- Leave the JSON `auth_hint` field as a single string with embedded `\n` so machine consumers see the full text.

**Patterns to follow:** Existing hint rendering at the end of `newDoctorCmd`.

**Test scenarios:**
- Hint text contains both `setlist-fm-pp-cli auth set-token` and `SETLISTFM_API_KEY`.
- Hint text contains the word `free`.
- Hint text contains `https://www.setlist.fm/settings/api`.
- Hint is only emitted when `auth` is `missing` (not when configured).

**Verification:** `go test ./internal/cli/...` passes. Manual smoke: run `doctor` with no auth configured and confirm both lines render with the word "free".

---

### U4. Record the patch in `.printing-press-patches.json`

**Goal:** Append a patch entry so the Printing Press tooling re-applies these fixes after future regenerations. Mirrors the existing `greptile-review-feedback` entry.

**Dependencies:** U1, U2, U3 (the entry's `files` list must match the actual changed paths).

**Files:**
- `library/media-and-entertainment/setlist-fm/.printing-press-patches.json` (modify)

**Approach:**
- Append a new object to the `patches` array:
  ```json
  {
    "id": "auth-flow-fix",
    "summary": "Re-route auth set-token to fm_api_key, drop dead OAuth fields, and fix doctor env-vars verdict.",
    "reason": "Generated CLI emitted an OAuth-shaped config but Setlist.fm uses only the static x-api-key header, so auth set-token wrote to a dead field and doctor falsely reported FAIL Env Vars when config-based auth was valid.",
    "files": [
      "internal/cli/auth.go",
      "internal/cli/doctor.go",
      "internal/cli/doctor_test.go",
      "internal/config/config.go",
      "internal/config/config_test.go"
    ],
    "validated_outcome": "go test ./... passed; manual smoke confirmed auth set-token persists to fm_api_key and doctor no longer reports FAIL Env Vars when config provides auth."
  }
  ```
- Leave `schema_version`, `applied_at`, `base_run_id`, `base_printing_press_version` unchanged — those describe the base generation, not this patch.

**Patterns to follow:** Existing `greptile-review-feedback` entry shape.

**Test scenarios:**
- Test expectation: none -- pure metadata file, validated by the actual code changes in U1-U3 and by Printing Press tooling later re-applying patches.

**Verification:** File parses as JSON (`jq . .printing-press-patches.json`). `patches` array has two entries.

---

## System-Wide Impact

- **Config schema:** `config.toml` files written by old versions remain readable (lenient TOML unmarshal). On the next save, the legacy `access_token` / `refresh_token` / `token_expiry` / `client_id` / `client_secret` / `auth_header` keys are dropped from the file. No data loss because those fields were never read.
- **`auth status` command:** already calls `cfg.AuthHeader()`, so it picks up the U1 simplification automatically. No code change needed.
- **HTTP layer:** the X-Api-Key header is set from `cfg.AuthHeader()`, which after U1 returns `cfg.SetlistFmApiKey` directly. No change needed.
- **README / SKILL.md:** already say "Get a free API key" — no change required.
- **MCP binary (`setlist-fm-pp-mcp`):** shares the same `internal/config` package, so it picks up the schema change transparently.

## Risks and Mitigations

- **Risk:** `config.go` is generated; renumbering struct fields could conflict with a future regeneration. **Mitigation:** the `.printing-press-patches.json` entry exists exactly to flag this — Printing Press tooling re-applies patches after regeneration.
- **Risk:** `applyAuthFormat` / `AuthHeaderVal` might be referenced elsewhere in the generated CLI (e.g., a per-resource client). **Mitigation:** during U1 implementation, grep `library/media-and-entertainment/setlist-fm/` for `AuthHeaderVal` / `applyAuthFormat` / `AccessToken` / `RefreshToken` / `TokenExpiry` / `ClientID` / `ClientSecret`. The greptile-style review precedent shows the package compiles cleanly when only `AuthHeader()` is used externally, but verify before removing.
- **Risk:** Migrating users with the legacy fields populated. **Mitigation:** `go-toml/v2` Unmarshal ignores unknown keys by default; on save the marshaller writes only the remaining fields. Existing users keep working.

## Verification Strategy

1. `cd ~/printing-press-library/library/media-and-entertainment/setlist-fm && go test ./...` — full module test suite passes.
2. `go vet ./...` — no warnings.
3. `go build ./cmd/setlist-fm-pp-cli` — binary builds.
4. Manual smoke:
   - Wipe `~/.config/setlist-fm-pp-cli/config.toml`.
   - `setlist-fm-pp-cli auth set-token <real-key>` — exits 0, file contains `fm_api_key = "<real-key>"` and no `access_token` line.
   - `unset SETLISTFM_API_KEY SETLIST_FM_API_KEY && setlist-fm-pp-cli doctor` — all five health-check lines start with `OK` (no `FAIL Env Vars`).
   - `setlist-fm-pp-cli artist resolve "Phish"` — succeeds (HTTP 200, not 403).
   - `setlist-fm-pp-cli auth logout` — clears `fm_api_key`.
   - Run `doctor` with no auth configured and confirm the hint mentions `auth set-token` first and contains the word `free`.

## Branch and PR Plan

1. From a clean `~/printing-press-library` checkout on `origin/main`: `git checkout -b fix/setlist-fm-auth-flow origin/main`.
2. Apply U1, U2, U3 changes, run the verification strategy.
3. Add U4 patch entry, re-run `go test ./...`.
4. Commit with a single message: `fix(setlist-fm): repair auth set-token, doctor env-vars, and signup hint`.
5. `git push -u origin fix/setlist-fm-auth-flow`.
6. `gh pr create --repo mvanhorn/printing-press-library --base main` with a body that:
   - Names the three bugs and links the bug report (paste content, since it lives in `~/Downloads/`).
   - References PR #724 as the contribution that shipped the affected CLI.
   - Lists the verification steps that were run.
   - No process narrative, no AI-tool disclosure beyond the standard footer (per global feedback).
