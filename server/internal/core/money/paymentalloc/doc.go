// Package paymentalloc applies a received payment to open items (backend plan
// §9.9, money.md defaults: "oldest debt first, principal before fee; the
// manager can always pick a specific charge to pay").
//
// # Orders
//
//   - OldestFirstPrincipalBeforeFee (default): every open principal item in due
//     date order, then every fee item in due date order; ties by charge id.
//     (Paying principal first stops fee accrual soonest and is the documented
//     reading of the decision; verify with counsel that this ordering is
//     acceptable under TBK art. 100, which by default clears costs and interest
//     before principal. Sites in doubt can use FeesFirst.)
//   - FeesFirst: fees first, then principal, each oldest first. Reserved for packs.
//   - Manual(ids): only the listed charges, in the listed order.
//
// # Guarantees
//
// Allocate never allocates more than an item's remaining amount or more than
// the payment; the sum of allocations plus Credit equals the payment exactly;
// the result is deterministic. The remainder becomes Credit, which phase C
// books as a prepayment liability on the unit side or the person.
package paymentalloc
