// Package allocate splits a money total across units by an allocation key
// (backend plan §4.1/§4.5/§9.6, decision M2).
//
// # Largest remainder
//
// For weights w_i with sum W and an integer total T (minor units), unit i's
// exact quota is q_i = T*w_i/W (computed exactly with big.Rat, no floats).
// Each unit gets floor(q_i); the L = T - sum(floor) units left over go one each
// to the units with the largest fractional remainders. Ties are broken by the
// caller-supplied OrderFn (door-number sort key, then id), and the returned
// shares are in that same order, so the result is fully deterministic and
// always sums exactly to T. Zero-weight and exempt units never receive a
// leftover unit. Zero-decimal currencies (JPY) need no special case because
// the unit of rounding is the minor unit. A negative total (credit note) is
// allocated as |T| and negated.
//
// # Rotation
//
// By default ties always go to the first units in order, so a fixed 100 kuruş
// over 7 units costs the same two units one kuruş more every month. Passing
// WithRotation(periodKey) offsets the start of the tie order by
// fnv1a64(periodKey) mod n over the ordered units: still fully deterministic
// and still summing exactly, but the leftover kuruş move from period to
// period. Callers pass the charge's period key (plus a charge-type code when
// several charges share a period). A composite rotates each part by
// key + "/" + part index.
//
// # Keys
//
//   - equal: every non-exempt unit weighs 1 (Law 634 default for staff costs).
//   - share: arsa payı (land share) as a rational; Law 634 default for all
//     other costs. (verify: Law 634 art. 20 default of arsa payı and the
//     equal split for "yönetici/kapıcı" costs is the reading used here.)
//   - area: m² per unit.
//   - consumption: a metered quantity per unit, e.g. heat-cost-allocator units.
//   - table: explicit per-unit amounts (the heating company's monthly file, or
//     shops at a different rate). The non-exempt amounts must sum to the total
//     or ErrTableMismatch is returned; they are used as-is.
//   - composite: fractions of the total, each split by its own key (for
//     example 70 % consumption and 30 % area for central heating). The part
//     totals are rounded by largest remainder, so parts sum to the total.
//     A table part inside a composite must itself sum to its part total.
//     Table keys (and composites with a table part) reject a negative total:
//     a reversal must negate the original shares, not re-allocate.
//
// Scope (site, one block, a unit tag) is the caller's choice of which units
// it passes in; it is recorded in the snapshot only as a label.
//
// # Basis snapshot
//
// BasisSnapshot is the JSON stored on each charge (allocation_basis): key,
// scope, total, each unit's effective weight as an exact rational string and
// the weight total (composites nest their parts). It is stable: the same
// input always produces byte-identical JSON.
package allocate
