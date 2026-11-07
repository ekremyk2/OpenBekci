// Package latefee computes late fees (gecikme tazminatı) as decided in
// money.md M4 and backend plan §9.8: accrued continuously on demand from the
// open principal, crystallized once a month by a reconciling job, waivable and
// freezable.
//
// # Rules
//
//   - Fees accrue on the unpaid principal only; fees never compound. Only
//     OpenCharge values of principal kind are accepted.
//   - The rate is basis points per calendar month (500 = 5 %, the statutory
//     maximum; Policy.Validate rejects more). A day accrues bps/daysInMonth, so
//     a full month costs exactly bps, and each calendar month's accrual is
//     additionally clamped at principal x bps, so the statutory ceiling holds on
//     the amount, not only on the parameter.
//   - The policy is effective-dated (PolicySchedule): each accrual day uses the
//     policy in force that day, so changing a rate for the future never changes
//     what already accrued. The grace of a charge is the one in force on its
//     due date.
//   - Grace delays the start of accrual: the free window is exactly GraceDays
//     days after the due date.
//   - A payment counts from its effective date and stops accrual for that very
//     day, so a payoff quote on day D equals the fee after paying on D. A
//     backdated payment lowers the fee from its date; the monthly
//     reconciliation then books a reversal.
//   - A Freeze (unit side in legal collection) skips the frozen days of every
//     charge of that side, including charges created after the freeze began
//     (intended: the side is in collection).
//
// # Reconciliation and waivers
//
// Bookings are single-entry rows with a Kind: fee (>= 0), reversal (<= 0) and
// waiver (>= 0, the amount waived). Expected(accrual, bookings) derives the
// fee to charge: accrued total minus the waiver bookings, never below zero;
// a waiver larger than the accrued fee is capped and the excess reported as
// Expectation.Surplus. Reconcile(expected.Fee, bookings) compares with the fee
// and reversal bookings only and returns Delta, a fee booking (>0), a
// reversal booking (<0) or nothing, so running the monthly job twice posts
// nothing the second time. A waiver is entered once, as a waiver booking, and
// cannot be forgotten or double counted.
//
// # (verify) Legal points
//
// To confirm with counsel before release: Law 634 art. 20 (5 % per month, "aylık
// yüzde beş") applied pro rata by calendar day within the month rather than
// per started month (the code never exceeds 5 % in a month); fees on
// principal only (not on earlier fees or on costs); whether a grace period may
// be configured by the site; whether a freeze during enforcement (icra) is
// the right treatment of accrual.
package latefee
