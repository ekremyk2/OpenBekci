// Package statement builds account statements (hesap ekstresi) from ledger
// lines: running balance, opening and closing balance for a date range,
// principal/fee split, per debtor side (owner/tenant) and per person
// aggregation (money.md M1 and M8, backend plan §9.5 and §9.12).
//
// It is pure and in-memory; the PDF and CSV renderers consume Statement.
//
// # Sign convention
//
// Amounts are signed from the debtor's side: positive increases the balance
// owed (charge, fee, opening debt), negative decreases it (payment, waiver).
// A negative closing balance is a credit.
//
// # Range
//
// Range.From/To are inclusive calendar dates in the given zone. Lines before
// From fold into the opening balance; lines after To are ignored, so
// Closing = Opening + sum(rows). Each line moves either principal or fee (see
// Line.Part), so Closing.Principal + Closing.Fee = Closing.Total.
//
// Line.Part is required (there is no default): KindFee lines must be PartFee,
// KindCharge lines PartPrincipal, everything else states which component it
// moves. AccruedFeeLine builds the line for the fee accrued so far but not yet
// booked, which a statement always shows (money.md M4). Lines of one day keep
// input order (opening first), so a payment listed before a same-day charge
// shows a transient negative running balance; renderers should list charges
// first if that matters.
//
// Statement.Check verifies these invariants and Build runs it before returning.
package statement
