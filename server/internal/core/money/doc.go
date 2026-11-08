// Package money holds the pure, database-free money domain of OpenBekci:
// the Money value type and its currency table, plus (in sub-packages) the
// calculators phase C builds on:
//
//   - allocate:     splitting a total across units by allocation key
//     (equal, area, share, consumption, table, composite) with largest-remainder rounding
//   - rates:        effective-dated rate schedules and catch-up period enumeration
//   - latefee:      continuous late-fee accrual on principal and monthly reconciliation
//   - paymentalloc: applying a payment to open items (oldest first, principal before fee)
//   - statement:    running-balance statements per unit side and per person
//
// Nothing in this tree touches the database, HTTP or other core modules, so
// every rule is unit- and property-testable in isolation (backend plan §18).
//
// # Representation
//
// Money is an int64 count of minor units plus an ISO 4217 code; the exponent
// comes from the currency table (TRY 2, JPY 0, KWD 3, ...). Mixing currencies
// is an error, never a conversion, and arithmetic detects int64 overflow.
// There is deliberately no floating point or decimal library in the domain;
// the single float use is locale display formatting (Format), never a stored
// or computed value.
//
// # JSON
//
// Money marshals as {"amount": 123450, "currency": "TRY"}, matching the
// Money schema in api/openapi.yaml (integer minor units).
package money
