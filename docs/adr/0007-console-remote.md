# ADR 0007: a console web remote

## Status

Accepted (2026-10-24).

## Context

The warehouse console is a Module Federation shell: it owns the login-free
entry page, the top navigation and the design tokens, and lazy-loads one
remote per bounded context. last-mile-delivery had no remote, so its data was
reachable only through the REST API or the MCP tools. The fleet already has a
proven remote shape (task-interleaving's `web/`, and the slotting and
inbound-receiving remotes before it): a small read-only React SPA, exposed as
`<container>/App`, served by its own nginx pod behind the Nginx web gateway at
`/mfes/<context>/`, never routed by Kong and never talking to any API but its
own context's.

## Decision

- **`web/` is a Module Federation remote** (`@module-federation/vite`,
  container name `last_mile_mfe`, exposes `./App`, no props) with the fleet's
  fixed conventions: Vite `base` `/mfes/last-mile-delivery/` on build,
  dev/preview port `4014` (unique on the fleet's ladder,
  `strictPort`), and a vite config kept as a STATIC object — never the
  callback form, which Vite's `mergeConfig` cannot merge and which has killed
  test suites elsewhere in the fleet.
- **It is READ-ONLY and calls only this context.** The screens use the three
  GETs of `apis/openapi.yaml` (`GET /waves`, `GET /waves/{waveId}/stops`,
  `GET /routes/{routeId}`) through one API base resolved at runtime from the
  shell's `/config.json` (`window.__WAREHOUSE_CONFIG__.apiOrigin` +
  `/api/last-mile-delivery`); there is no write call, no sibling context and
  no fallback origin in production (a missing origin throws rather than
  silently talking to nothing).
- **Three screens.** Waves (a station + service date search listing each
  wave's state, cutoff, window and stop counters), Wave stops (the packages
  of one wave grouped by the opaque `stopKey`, with the destination postal
  prefix when the label data had it — no address is ever shown), and Routes
  (a route lookup showing the route's stops with their derived outcome and
  every recorded delivery attempt). Routing is relative, so the same bundle
  works under the console's `/last-mile-delivery/*` splat route and
  standalone on `:4014`.
- **Packaging mirrors the fleet.** A two-stage Dockerfile builds
  `@warehouse/ui-kit` from a NAMED BuildKit context (`--build-context
  uikit=../../warehouse-ui-kit`, a sibling checkout, not a registry package)
  and then this remote with `npm ci` against the committed lockfile; the
  runtime is `nginx-unprivileged` with an unhashed, no-cache `remoteEntry.js`,
  immutable hashed assets and a `/healthz` the probes use. The Helm chart
  gains a `frontend` Deployment + ClusterIP Service (default off); routing
  stays with the Nginx web gateway in warehouse-infra, never in this chart.
- **CI.** A `web` job checks out this repo and the ui-kit side by side, builds
  the ui-kit, then runs `npm ci`, oxlint, `tsc -b`, vitest and
  `vite build`. Tests are `@testing-library` component tests over a fetch
  mock that rejects every unmocked call, asserting this remote's own UI
  strings and that only this context's API base is ever fetched.

## Consequences

- The console can show last-mile-delivery's waves, stops and routes beside the
  other contexts, with zero new host surface: the remote is served from its
  own pod at the gateway's `/mfes/last-mile-delivery/` path.
- Writes stay where the contract puts them (`Idempotency-Key` POSTs on REST,
  owned by operator tooling); this remote deliberately has no write path.
- The exposed module (`./App`), the container name (`last_mile_mfe`), the
  port (4014) and the base path are a PINNED host/remote contract with the
  console: changing any of them is a coordinated change, not a patch release.
- Registering the remote in the warehouse-console host (its remoteEntry entry
  and nav item) is a change in that repository, not here.
