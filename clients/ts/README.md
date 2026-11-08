# OpenBekci TypeScript API types

Types for the OpenBekci HTTP API, generated from [`api/openapi.yaml`](../../api/openapi.yaml) with
[openapi-typescript](https://openapi-ts.dev) 7. Licensed Apache-2.0 so web, mobile and third-party
clients can use them freely.

- `index.d.ts` is generated; do not edit it. Regenerate with `tools/task.ps1 gen` (or `tools/task.sh gen`).
- `open-enum.d.ts` exports `Open<T> = T | (string & {})`. The API's enums are open (a newer server may add
  values), but the generated unions are closed. Wrap enum values read from responses in `Open<...>` and
  always handle an unknown value.

```ts
import type { components } from "@openbekci/api-types"; // path alias to clients/ts/index.d.ts
import type { Open } from "@openbekci/api-types/open-enum";

type ProblemCode = Open<components["schemas"]["Problem"]["code"]>;
```

Errors are RFC 9457 `application/problem+json`; translate them by `code`, never by `detail`.
Conventions: [api/README.md](../../api/README.md).
