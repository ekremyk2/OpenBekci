// Package rates holds effective-dated amounts and billing-period enumeration
// for charge plans (backend plan §4.5 charge_plan_rate, §9.7; decision M3).
//
// # Schedules
//
// A Schedule is a list of Rate{From, To?, Amount, DecisionID?}. From and To are
// inclusive calendar dates read in the site time zone passed to At and Validate
// (never in the zone a value carries); a nil To is open-ended. Schedule.At(date, tz) returns
// the rate in force on a date; ForPeriod picks the rate of a billing period by
// its due date (M3). Schedule.Validate(tz, contiguous) rejects overlaps, reversed
// ranges, negative amounts, mixed currencies, an open-ended rate that is not
// last and (when contiguity is required) gaps.
//
// # Periods and catch-up
//
// Periods enumerates a plan's billing periods by due date. A monthly plan has
// keys "2026-03", a quarterly one "2026-Q1" (periods start in Jan/Apr/Jul/Oct)
// and a yearly one "2026". The due date is DueDay of the first month of the
// period, clamped to the month length (31 -> Feb 28/29). The period containing
// the plan start is the first one; periods are not prorated, and a due date is
// never before the plan start (the first due date is the first DueDay on or
// after Start; see Periods). All date logic is
// done on calendar dates in the given time zone, so DST changes never produce
// a missing or duplicated period, and output is deterministic for the same
// input. The hourly generation job calls Periods(plan, lastDue+1, today) and
// creates the missing (plan, unit, period) charges, which makes it a catch-up
// job after downtime.
package rates
