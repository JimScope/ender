# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.4.1] - 2026-05-13

### Fixed
- **Webhook circuit breaker** — three failure modes uncovered by production logs:
  - A single failing destination tripped delivery for **all** users. Replaced the global breaker with a per-hostname `HostCircuitBreaker` so one bad URL no longer poisons unrelated webhooks.
  - The failure counter had no time decay, so a slow trickle of unrelated failures could trip the breaker over hours. Added a sliding failure window (5 minutes by default); failures older than the window reset the counter.
  - Half-open state could lock permanently if the probe vanished (e.g. `FireAndForget` panic). Added a probe timeout that re-arms the breaker after `cooldown` if no `RecordSuccess` / `RecordFailure` arrives.
- Webhook destinations differing only in case (`Example.com` vs `example.com`) now share one breaker (`webhookHost` lowercases the hostname).
- Unparseable webhook URLs fall back to a shared sentinel bucket instead of bypassing the breaker.

### Changed
- Per-webhook `Allow()` gating moved **inside** the delivery loop in `TriggerWebhooks` and `RetryFailedWebhooks`; the prior outer gate could short-circuit healthy hosts when a single host was failing.
- Breaker-open log lines lowered from `Warn` to `Debug` so outages don't drown the log stream.
- `HostCircuitBreaker` caps the per-host map at 1024 entries with closed-state-preferred eviction to prevent unbounded growth.

### Dependencies
- Bumped `github.com/pocketbase/pocketbase` from 0.37.4 → **0.38.0** (adds internal multi-process state watcher, Superuser IP/CIDR whitelist, rate-limit IP exclusion).
- Frontend: bumped `react` 19.2.4 → 19.2.6, `tailwindcss` 4.2.2 → 4.3.0, `@tailwindcss/vite` 4.2.2 → 4.3.0, `@tanstack/react-query` 5.96.1 → 5.100.9, `@tanstack/router-devtools` 1.166.11 → 1.166.13, `react-i18next` 17.0.4 → 17.0.7, `@biomejs/biome` 2.4.13 → 2.4.15, `dotenv` 17.4.0 → 17.4.2.

## [0.4.0] - 2026-04-29

### Added
- `GET /api/devices` — paginated list of the authenticated user's registered devices. Supports `page`, `per_page` (max 200), and `device_type` filters. Auth: JWT or `vk_` API key.
- `GET /api/sms/messages` — paginated SMS message history. Supports `page`, `per_page`, `status`, `device_id`, `batch_id`, `recipient`, `from`, and `to` (ISO8601) filters. Auth: JWT or `vk_` API key.
- `GET /api/plans/quota` extended with `scheduled_sms_count`, `integrations_count`, `max_scheduled_sms`, and `max_integrations` so clients can show the four resource counters consistently.

### Removed
- Deprecated quota response keys `scheduled_sms_active` and `integrations_created`. Clients should switch to the new `scheduled_sms_count` and `integrations_count` names introduced in this release.

## Earlier versions

For releases prior to 0.4.0 (`v0.1.0-beta.x`, `v0.2.0`, `v0.3.0`, `v0.3.1`, `v0.3.2`), see the published GitHub releases at <https://github.com/JimScope/vendel/releases>.
