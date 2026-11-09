# SDK changelog

`sdk/` follows semantic versioning independently of the server. From 1.0.0 the module is frozen: within `sdk` v1 nothing
exported is removed, renamed or changed in signature, and an existing interface never gains a method. New behaviour arrives as a
new optional interface (found by type assertion) or a new exported name, in a minor release. A breaking change waits for `sdk/v2`.
A pack built for 1.x (`MinSDK` 1.x.z) runs on every server whose SDK is 1.y.w that is not older than that.

## 1.1.0

- New optional extension point `ChatProvider` (messengers such as BiP for the notification channel `chat`), registered through the
  optional `ChatRegistrar` that the host's registry implements (an existing interface never gains a method). See `docs/packs/authoring.md`.

## 1.0.0

Freeze, with project release 1.0.

- `Version` is `1.0.0`; before 1.0 the server refused packs of another minor line, from now on only another major.
- New optional interfaces from the paper packs for Germany and the Netherlands (`docs/packs/paper-packs.md`):
  `MultiStatementImporter` (one bank file, several accounts) and `ComplianceChecker` (statutory checks reported to the dashboard).
  Their host call sites arrive in a 1.x minor release; implementing them earlier is harmless.
- Everything else is the 0.1.0 surface unchanged.

## 0.1.0

First release (milestones P0 to P2): the extension points listed in `docs/packs/README.md`.
