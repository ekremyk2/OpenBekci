# Licensing

OpenBekci is licensed per directory. The split and its reasons were decided on 2026-11-06 (see [docs/plans/00-overview.md §2.2](docs/plans/00-overview.md) and [docs/plans/06-review.md §2](docs/plans/06-review.md)).

| Directory | License | Why |
|---|---|---|
| `server/`, `web/`, `relay/`, `deploy/`, `docs/`, everything not listed below | **AGPL-3.0-or-later** ([LICENSE](LICENSE)) **with the pack-linking exception below** | Copyleft keeps improvements to the server open, including when it is only run as a hosted service. The exception lets organizations compile private country or vendor packs into the binary without the AGPL extending to the pack. |
| `mobile/` | **MPL-2.0** (file to be added with the directory) | A file-level copyleft that is compatible with App Store and Play distribution terms, which AGPL is widely considered not to be. |
| `sdk/`, `api/`, `clients/` | **Apache-2.0** (file to be added with each directory) | The pack SDK, the OpenAPI specification and the generated client libraries are interfaces; anyone may build against them, including closed-source integrations. |
| `packs/*` (official packs) | **AGPL-3.0-or-later**, same as the server | Official packs are part of the product. |

Each of those directories carries its own `LICENSE` file; a file is governed by the nearest `LICENSE` above it in the tree.

Contributions are accepted under the [Developer Certificate of Origin](https://developercertificate.org/) (`Signed-off-by`), not a CLA. Because of that, the exception below cannot be widened later without every contributor's consent — which is why it exists from the first commit.

## Pack-linking exception to the AGPL-3.0 (DRAFT — pending legal review)

> **Status: draft.** This wording has not been reviewed by a lawyer. It must be before the project accepts its first outside contribution. Until then it states the project's *intent*; do not rely on it.

Additional permission under GNU AGPL version 3 section 7:

If you modify this Program, or any covered work, by linking or combining it with one or more **Packs** — Go modules that interact with the Program solely through the public pack SDK published under `sdk/` (the "SDK"), register themselves through the SDK's registry, and do not modify the Program's source — the licensors of this Program grant you additional permission to convey the resulting work and to make it available for interaction over a network **without** being required to license the Pack itself under the AGPL or to provide the Pack's Corresponding Source, provided that:

1. the Pack communicates with the Program only through the SDK's exported interfaces and registered extension points;
2. you comply with the AGPL for the Program itself and for any modifications you make to it, including section 13 (offering the Corresponding Source of the *Program as modified* to users interacting with it remotely — the Pack is not part of that modified Program's Corresponding Source for this purpose);
3. the SDK remains licensed under Apache-2.0 or a license no more restrictive.

This exception applies only to Packs as defined above. Modifying the Program's own source, or bypassing the SDK to reach the Program's internal packages, is a modification of the Program and the exception does not apply to it.

If you modify this Program and distribute or convey it, you may extend this exception to your version of the Program, but you are not obligated to do so. If you do not wish to do so, delete this exception statement from your version.

## Why not Apache-2.0 for everything?

It would maximize adoption, at the cost of allowing anyone to run a closed hosted fork of the server. The project may one day fund itself through a hosted service; AGPL keeps that option open while leaving the SDK, spec and clients free of any copyleft concern.

## Why not AGPL for the mobile app?

The App Store's terms impose restrictions on users that the GPL family does not allow, which is why GPL apps have been removed from the store in the past (the VLC case). MPL-2.0 keeps changes to the app's files open without that conflict.
