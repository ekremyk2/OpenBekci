# Contributing, licensing, legal

## Contributing

Contributions are accepted under the [Developer Certificate of Origin](https://developercertificate.org/): sign off every commit (`git commit -s`, which adds `Signed-off-by: Your Name <you@example.org>`). There is no contributor licence agreement. Because of that, the pack-linking exception below cannot be widened later without every contributor's consent.

The repository is a Go workspace: `server/` (Go), `web/` (React), `sdk/`, `packs/`, `api/` (the OpenAPI source), `deploy/` and `docs/`. Each README says how to build and test its part: `server/README.md`, `web/README.md`, `api/README.md`. `tools/task.sh` and `tools/task.ps1` run the pinned task runner (`tools/task.sh --list`). The API is contract-first: edit `api/src/**`, then run the `gen` task and commit the generated files with the source.

Documentation lives in `docs/` and is published by MkDocs Material from `docs-site/`. To preview: `pip install -r docs-site/requirements.txt`, then `mkdocs serve -f docs-site/mkdocs.yml`. The site is English only for now.

## Licensing

Licensing is per directory; a file is governed by the nearest `LICENSE` above it.

| Directory | Licence |
|---|---|
| `server/`, `web/`, `deploy/`, `docs/` and anything not listed below | AGPL-3.0-or-later, with the pack-linking exception below |
| `mobile` (own repository) | MPL-2.0 |
| `sdk/`, `api/`, `clients/` | Apache-2.0: interfaces anyone may build against, including closed-source integrations |
| `packs/*` (official packs), the push relay and the on-site agent | AGPL-3.0-or-later |

The full text and the reasoning are in `LICENSING.md` in the repository.

### Pack-linking exception (draft)

The server's AGPL carries an additional permission so that an organization may combine it with **packs** that interact with it solely through the public SDK without having to publish the pack's source. The wording in `LICENSING.md` is a **draft that has not been reviewed by a lawyer**. It states the project's intent; do not rely on it until the review has happened (planned before the first outside contribution and no later than 1.0). Modifying the server's own source, or reaching its internal packages, is a modification of the server, and the exception does not cover it.

## Legal

- **Not legal advice.** Packs that encode law (the [Turkish pack](packs/tr.md)) implement the maintainers' reading of the law at a given date. Organizations remain responsible for their own compliance. Values marked **(verify)** are unconfirmed.
- **Personal data.** The software is built for KVKK and GDPR-style obligations: national ids are encrypted and masked, the audit trail records hashes instead of personal values, and the push relay never sees notification content. A self-hosting organization is the data controller and carries the obligations that follow, including the choice of where to host.
- **No telemetry.** Self-hosted instances send nothing to the project.
