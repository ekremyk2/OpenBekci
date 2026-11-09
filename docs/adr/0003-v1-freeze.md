# ADR-0003: Freezing /v1 and the pack SDK at 1.0

| | |
|---|---|
| Status | **Accepted** |
| Date | 2026-11-09 |
| Deciders | project owner (plan default: `docs/plans/01-backend.md` section 8 and `04-packs.md` section 3.10), implemented by an agent |
| Supersedes | the "/v1 is not frozen until 1.0" notes in `api/src/openapi.yaml` and `docs/packs/README.md` |

## Context

The plans promise a `/v1` freeze and an `sdk/` v1.0 freeze with project release 1.0. Third parties build on both: mobile apps in
older store versions, scripts with API keys, and packs compiled into forks. After the freeze a mistake is permanent, so the
decision is what is frozen, how a change is checked, and how the freeze ends.

## Decision

1. **Scope.** `/v1` is the served spec (`api/openapi.yaml`), per operation by `x-stability`: `stable` operations are frozen (additive
   changes only), `preview` operations are not. The draft root is a plan, not a contract. At the freeze, 123 preview operations
   that the web client uses and that have server tests became `stable`, 24 stay `preview` (no client yet, among them the agent protocol
   and gate console parts, the mobile device and web-login operations), and eight draft operations nobody needed were dropped
   (`docs/api/deferred.md`).
2. **Rule and guard.** Additive-only, defined in `docs/api/versioning.md`. A committed snapshot `api/snapshots/v1.0.yaml` and a Go test
   (`TestV1FreezeAgainstSnapshots`) fail the build on a removed operation, response, field or parameter, a changed type or format, or a
   removed enum value. The check is deliberately structural and small; semantic breaks are caught in review.
3. **Deprecation.** `deprecated: true` with `x-deprecated-since` and `x-sunset` makes the server send `Deprecation` and `Sunset`
   headers. Two minor releases of notice minimum; removal only through `/v2`, which gets 12 months of overlap.
4. **SDK.** `sdk/` is 1.0.0 and follows the same rule: no removal, rename or signature change; interfaces never widen; additions are
   new optional interfaces. Before the freeze two paper packs (Germany, Netherlands, `docs/packs/paper-packs.md`) were mapped onto
   the extension points; the two gaps that would need new interfaces (multi-account statement files, compliance checks) were added
   as optional interfaces now, the rest are additive later.

## Options considered

| | Freeze everything served | Freeze `stable` only (chosen) | Freeze nothing, rely on semver notes |
|---|---|---|---|
| Honest about unproven parts | No: preview operations with no client are locked in | Yes | Yes |
| Cost of a mistake in an unused operation | Permanent | A preview change | None |
| Trust of third parties | Highest | High for what is promised | Low |
| Test | One snapshot | One snapshot, preview exempt | None |

A breaking-change tool such as `oasdiff` would check more (parameter requiredness, status codes), but it is a new dependency and
a pnpm or binary download in CI for a check a 150-line test does for the cases that matter; it can replace the test later without
changing the rule.

## Consequences

- Authors of path files must read `docs/api/versioning.md` before touching a stable operation; the PR checklist is there.
- An operation promoted to `stable` is a promise: promote on use by a client and a server test, not on hope.
- The unserved draft operations enter `/v1` additively, so the freeze does not delay them.
- A security fix that needs a breaking change goes through the deprecation path or `/v2`; there is no snapshot override.
- `sdk` v1 keeps `WithOrg` exported and the registry's trust model (packs are compiled-in, trusted code); tightening is a host change.
