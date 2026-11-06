# Decisions — Community (bookings, visitors, guard console, parcels)

| | |
|---|---|
| Date | 2026-11-06 |
| Decided by | project owner, in a question-and-answer walkthrough |
| Status | Decided. Ships in **0.3** together with gate control (D6). The backend and web plans are to be revised to match; until then this file wins over conflicting text in `docs/plans/`. |

## C1. Facility bookings: rules per facility, instant confirmation, optional fee

**Decided:** each bookable facility (hall, barbecue area, court, guest flat …) has its own rules — opening hours, maximum duration, how far ahead, how many bookings per unit per month, optional fee. A booking within the rules **confirms instantly**; a fee becomes a charge on the unit's account (owner or tenant side per Money M1, defaulting to whoever booked). The manager can cancel bookings and block dates.

**Plan changes:** as planned in backend §4.11 with `facility.rules` made concrete (hours, max duration, lead time, quota per unit per period, fee, who may book: owners / tenants / household members); no approval queue.

## C2. Visitors: QR admission, pre-registration, guard console, recurring visitors

**Decided:**

1. **Pre-registration** — a resident enters a guest's name, plate and time window. The system generates a **QR code** the resident sends to the guest (WhatsApp, SMS, email).
2. **QR admission at the gate** — the guard scans the guest's QR; if it is valid for now and this gate, the guard **admits directly**; the visit is logged against the unit. From 0.3 the gate itself can act on it where hardware exists (D6).
3. **No QR → ask the resident** — for an unexpected visitor the guard enters the name (and plate) and the unit, and the system sends the resident an **approve / deny request** (in-app and SMS, O5 defaults); the resident answers from the portal or app; if there is no answer within a short time the guard **calls the resident** (the console shows the number the resident chose to share for this purpose). The decision is logged either way.
4. **Guard console** — the booth page: today's expected visitors, search by name or plate, mark arrived / left, walk-in entry, the approval-request button, parcels (C3). Large targets, auto-refresh, sized for a tablet or the booth PC.
5. **Recurring visitors** — a standing permission for a named person (cleaner, nanny, carer) on chosen days and hours, with its own long-lived QR, revocable by the resident.

**Plan changes:** `visitor_entry` gains `qr_token`, `recurring_rule`, `approval_status` (pre-approved / requested / approved / denied / expired) and `decided_by`; a `visitor_approval_request` sent through notifications with an expiry; the guard console in the web plan becomes a first-class screen in 0.3 (not 1.1 of mobile); a per-person "gate contact number" setting (S8 directory rules apply — guards see only that number).

## C3. Parcels: simple log

**Decided:** the guard logs a delivery against a unit; the resident is notified; the guard marks it collected (by whom). No photos or signatures required; optional parcel description.

**Plan changes:** `delivery` (unit, received_at, received_by, description, collected_at, collected_by_name) — promoted from "optional" to a 0.3 feature.

## C4. Roadmap: ships with gate control in 0.3

**Decided:** the whole Community area ships in **0.3** with gate control (D6/D7). Everything is web: the guard booth uses a tablet or PC; residents use the web portal to book and pre-register until the mobile app exists. The mobile app later adds the phone-native versions (scan-to-share QR, approve-from-notification).

## Defaults set without asking (change them if wrong)

| Topic | Default |
|---|---|
| Who may book | Owners, tenants and household members of a unit; staff cannot book; the manager can book on behalf of a unit |
| Booking fee | Charged when the booking is confirmed; refunded automatically if cancelled before a facility-defined cutoff; otherwise kept |
| Visitor QR validity | The registered time window plus 30 minutes either side; single entry unless marked recurring; a QR is bound to the site, not to a specific gate, until gate control exists |
| Visitor data retention | Visitor logs are kept for a site-configurable period (default 90 days) then deleted; KVKK-sensitive, so shorter is allowed, not longer without a stated reason |
| What the guard sees | Visitor name, plate, unit, time window, and the resident's chosen gate contact number — never balances, other contacts or national IDs (S8) |
| Deliveries notification | In-app plus the resident's preferred channel; "collected" is logged, not notified |
| Guard login | A staff role scoped to the site (S3); the console works on a shared booth device with a quick PIN switch between guards so each action is attributed to a person |

## Still open in Community

- Whether a facility can require a **deposit** (returned after inspection) in addition to a fee (default: not in 0.3; a deposit kind exists in the money model for later).
- Whether residents can see **who booked** a facility slot or only that it is taken (default: only "taken" — KVKK; the manager sees names).
