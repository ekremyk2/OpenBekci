/**
 * Open enums. The API's enums are open: a newer server may send a value this
 * client was not generated with. openapi-typescript emits closed unions, so
 * wrap every enum you read from a response in Open<T> and always keep a
 * default branch:
 *
 *   type Code = Open<components["schemas"]["Problem"]["code"]>;
 *   switch (problem.code as Code) { case "not_found": ...; default: ... }
 *
 * Autocomplete still offers the known values; unknown strings stay assignable.
 */
export type Open<T extends string> = T | (string & {});
