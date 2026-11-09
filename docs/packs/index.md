# Pack index

Packs add a country's rules, papers and providers to the server. This page lists the packs the project knows about.

## Official

| Pack | Id | Countries | Licence | What |
|---|---|---|---|---|
| [Turkish pack](tr.md) | `tr` | TR | AGPL-3.0 | Law 634 presets, documents, KVKK texts, iyzico, two bank importers, İleti Merkezi SMS. In the default build. |
| Example pack (`packs/example`) | `example` | none | AGPL-3.0 | A deliberately tiny pack implementing every extension point, the starting point for new packs. Compiled in only with `-tags examplepack`. |

## Community packs

None are listed yet.

## List yours

1. Build a server with it: `tools/obkbuild --with <your module>@<version>` must succeed and list the pack (see [Building a server with your pack](authoring.md#12-building-a-server-with-your-pack)).
2. Run the conformance suites that apply to what you implement (payment providers, statement importers); see [Testing](authoring.md#11-testing).
3. If the pack encodes law, ship a `LEGAL.md` (articles, dates, ambiguities, disclaimer).
4. Open a pull request that adds a row to the table above: name, id, countries, licence, repository URL, latest version, one line of description, and the maintainer. Sign off your commit (DCO, see [Contributing](../contributing.md)).

A listing is not an endorsement or a legal review. Packs are trusted code compiled into the server: read a pack before you build it in.
