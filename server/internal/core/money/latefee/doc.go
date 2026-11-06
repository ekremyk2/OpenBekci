// Package latefee computes late fees (gecikme tazminatı) as decided in
// money.md M4 and backend plan §9.8: accrued continuously on demand from the
// open principal, crystallized once a month by a reconciling job, waivable and
// freezable.
//
// # Rules
//
//   - Fees accrue on the unpaid principal only; fees never compound.
//   - The rate is basis points per 30-day month (500 = 5 %, the statutory
//     maximum; a site may set less, Policy.Validate rejects more).
//   - Day-based pro-rating: fee = round(sum over days of unpaid * bps / 10000 / 30),
//     rounded half-up once per charge (see Accrue for the exact day semantics).
//   - Grace days delay the start of accrual; the grace days are never charged.
//   - A payment counts from its effective date, so a backdated payment lowers
//     the fee from that date; the monthly reconciliation then books a reversal.
//   - A Freeze (unit side in legal collection) skips the frozen days.
//   - A Waiver subtracts a fixed amount from the expected fee (floored at 0).
//
// # Reconciliation
//
// Expected = accrued total - waivers. Reconcile(expected, booked) returns
// Delta = expected - sum(booked), with Action book (>0), reverse (<0) or none.
// Posting Delta as a new booking (negative for a reversal) makes booked equal
// expected, so the job is idempotent. A waiver is posted as a booking of
// minus its amount at the moment it is granted (it is also what the waiver
// ledger entry reverses), so it appears in booked and in the waivers passed to
// Expected, and is never counted twice.
//
// # (verify) Legal points
//
// To confirm with counsel before release: Law 634 art. 20 (5 % per month, "aylık
// yüzde beş") applied pro rata by day on a 30-day month rather than per started
// month; fees on principal only (not on earlier fees or on costs); whether a
// grace period may be configured by the site; whether a freeze during
// enforcement (icra) is the right treatment of accrual.
package latefee
