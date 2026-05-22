# IMPLEMENTATION_NOTES

## What is implemented

- Standalone Go service scaffold
- SQLite-backed proposal storage
- SQLite-backed quota probe cache for reorder reuse
- HTTP API:
  - `GET /`
  - `GET /healthz`
  - `GET /status`
  - `GET /issues`
  - `GET /review/401`
  - `GET /proposals`
  - `GET /progress/full`
  - `POST /auth/login`
  - `POST /auth/logout`
  - `POST /scan/issues`
  - `POST /scan/full?mode=quota`
  - `POST /scan/full?mode=reorder`
  - `POST /accounts/:name/disable`
  - `POST /accounts/:name/delete`
  - `POST /proposals/:id/apply`
- Built-in management page served from `GET /`
- Browser login flow protected by shared CPA management key + HttpOnly session cookie
- Script/API access still supported via shared management-key header
- CLIProxyAPI Management API client for:
  - `GET /v0/management/auth-files`
  - `GET /v0/management/logs`
  - `DELETE /v0/management/auth-files?name=...`
  - `PATCH /v0/management/auth-files/status`
  - `PATCH /v0/management/auth-files/fields`
  - `POST /v0/management/api-call`
- `auth-files` parsing compatible with both top-level `files` and `auth_files`
- Full reconcile priority initialization when current priority is zero / unset
- Current reorder path only reorders enabled accounts
- Full quota scan writes probe cache; reorder can reuse recent cached probe data instead of probing again
- Dockerfile + docker-compose for iterative container deployment
- Additional pull-and-run compose file for published GHCR image usage

## Scan behavior

### `POST /scan/issues`
Lightweight issue scan:
- fetches auth-files
- optionally reads management logs
- identifies explicit bad signals such as 401 / invalid / quota exhausted
- proposes targeted actions like disable
- does **not** perform full quota polling or full priority reorder

Operationally this is the fast, low-cost scan suitable for frequent background checks.

### `POST /scan/full?mode=quota`
Quota-focused full scan:
- fetches auth-files
- probes quota / refresh data through `wham/usage`
- writes probe results into local cache
- computes disable / enable proposals from real quota state
- identifies explicit 401 and generates disable + manual-review proposals
- does **not** generate reorder actions

### `POST /scan/full?mode=reorder`
Reorder-focused full scan:
- fetches auth-files
- does **not** perform live quota probe
- reuses recent cached probe data when available
- falls back to `NextRetryAfter` / name ordering when cache is absent or stale
- only reorders currently enabled accounts
- does **not** generate quota / 401 actions

Operationally, `mode=quota` is the expensive fact-gathering path and `mode=reorder` is the lighter ordering path.

## Current limitations

1. Log matching still uses account-name substring matching.
   This should be upgraded to auth_index/request metadata matching if available from CPA-Manager or CPA logs.
2. The current auth model is still a single shared management key plus browser session, not a full multi-user permission system.
3. Gin is still running in debug mode.
4. The built-in UI/admin ergonomics are better than before but still not fully split into maintainable static assets.
5. Go dependency locking/caching still needs cleanup: `go.sum` should be committed and Docker build should consume `go.mod` + `go.sum` directly instead of relying on build-time `go mod tidy`.
6. The management page should continue being localized in Chinese for easier day-to-day use.
7. Probe-cache freshness is currently fixed in code and not yet configurable.

## Recommended next implementation slice

- Split the built-in UI into separate static assets instead of keeping all HTML/JS inline in Go source
- Commit a stable `go.sum` and simplify Docker dependency layers
- Add stronger audit metadata around auto-actions and manual actions
- Add event deduplication / idempotency tables
- Consider a configurable probe-cache TTL
- Switch Gin to release mode in production

## Safe rollout

1. Start with `APP_MODE=dry-run`
2. Run one `POST /scan/full?mode=quota` to establish recent probe facts
3. Inspect `GET /proposals`, `/review/401`, and logs
4. Optionally run `POST /scan/full?mode=reorder`
5. Only then switch to `apply` in a controlled window
