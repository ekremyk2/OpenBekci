# API versioning and deprecation

Decision record: [ADR-0003](../adr/0003-v1-freeze.md). Plan: `docs/plans/01-backend.md` section 8.

## What is frozen

From release 1.0 the served spec `api/openapi.yaml` is the contract of `/v1`. Every operation has an `x-stability`:

| Value | Meaning |
|---|---|
| `stable` (default) | Frozen. Clients may depend on it. Only additive changes. |
| `preview` | Served but not frozen: no web or mobile client uses it yet, or its design awaits a real consumer. The response carries `X-OpenBekci-Stability: preview`. It can change in any minor release, and is promoted to `stable` in an additive spec change once a client uses it. |

The draft root (`api/openapi-draft.yaml`) is not a contract; operations in it that are not served are plans (`api/DRAFT-INDEX.md`),
and what was dropped is in [deferred.md](deferred.md).

## Additive changes only

Allowed on a stable operation within `/v1`:

- a new operation, a new optional query parameter, a new optional request field;
- a new response field (clients ignore unknown fields), a new value of an **open** response enum (`x-open-enum`);
- a new problem `code`, a new response header, a new `2xx`/`4xx` status the client already treats generically;
- a looser limit (a longer maximum, a smaller minimum), a description change.

Not allowed (the contract test fails): removing an operation, a response status, a request or response field, a parameter or a media
type; changing a type or format; removing an enum value; renaming an `operationId`. Also not allowed, though the test cannot see
it: making an optional request field required, narrowing an accepted range, changing the meaning of a field, a new required
parameter, a new error on a request that used to succeed. Review these by hand.

An enum in a request that clients send must not grow in a way that changes old meaning; an enum in a response is open
and may grow.

## The contract test

`api/snapshots/v1.0.yaml` is a copy of the bundled served spec at the freeze. `TestV1FreezeAgainstSnapshots` in
`server/internal/httpapi/contract_freeze_test.go` compares the current `api/openapi.yaml` with every `api/snapshots/v1.*.yaml`
and fails on a removed operation, response, field or parameter, a changed type or format, or a removed enum value. Only operations
that were `stable` in the snapshot are held; `preview` operations are exempt.

Adding fields or operations needs no change to the snapshot. A deliberate breaking change has no escape hatch inside `/v1`:
it waits for `/v2` (below). Do **not** edit an old snapshot to make the test pass. At each minor release that adds to the
spec, a maintainer may add `api/snapshots/v1.<minor>.yaml` (a copy of `api/openapi.yaml`), which tightens the guard to the new surface.

## Deprecation

Deprecating means telling clients that something will not exist in `/v2`. It never removes anything from `/v1`.

1. Mark the operation `deprecated: true` in the path file and set `x-deprecated-since: "YYYY-MM-DD"` (the date of the release that
   announces it) and, if a date is known, `x-sunset: "YYYY-MM-DD"` (the earliest day it may stop working, and not earlier than the
   `/v1` sunset below). Describe the replacement in `description`.
2. The server then answers every call with `Deprecation: @<unix time of x-deprecated-since>` (RFC 9745) and, with `x-sunset`,
   `Sunset: <HTTP date>` (RFC 8594). The pipeline reads them from the spec (`server/internal/httpapi/pipeline.go`); nothing is coded per operation.
3. Announce it in the release notes of the minor release that introduces it.
4. **Notice: at least two minor releases** with the deprecation shown before a deprecated item can be dropped, and
   it can be dropped only by `/v2`. A deprecated field of a response stays in the response, filled, until then.
5. Deprecated is not preview: a deprecated stable operation keeps all its guarantees.

## Starting /v2

`/v2` begins only when a breaking change cannot be done additively, and needs a new ADR that states why.

1. A new root `api/src/openapi-v2.yaml` with paths under `/v2/...`, sharing components with `/v1` where unchanged.
   One server serves both; a client picks by path.
2. The `/v1` snapshots stay in `api/snapshots/` and the freeze test stays on: `/v1` is maintained, with security and data
   fixes, until its sunset.
3. `/v1` gets a sunset date at least 12 months after `/v2` is generally available; every `/v1` operation then carries
   `Deprecation` and `Sunset`.
4. `GET /.well-known/openbekci.json` lists the supported API versions; `min_app_version` gates old mobile builds.
5. `sdk/` versions independently ([sdk/CHANGELOG.md](../../sdk/CHANGELOG.md)): a `/v2` does not by itself make an `sdk` v2.

## Checklist for a pull request that touches `api/src`

- `tools/task.sh gen` (or `.ps1`) and commit the generated files.
- `go test ./internal/httpapi -run 'TestV1Freeze'` from `server/` passes.
- A new operation has an `x-stability`; promote `preview` to `stable` only with a real client and tests.
