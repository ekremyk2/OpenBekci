# Decisions — Governance (assemblies, decisions, elections)

| | |
|---|---|
| Date | 2026-11-06 |
| Decided by | project owner, in a question-and-answer walkthrough |
| Status | Decided. Ships in **0.2** (after the web-only 0.1, before gate control and mobile). The backend and web plans are to be revised to match; until then this file wins over conflicting text in `docs/plans/`. |

Covers the kat malikleri kurulu (owners' assembly), decisions, minutes, elections and terms. Legal claims marked **(verify)** must be checked by a person with Turkish practice before being encoded (see review §10.F).

## G1. The system runs the whole assembly

**Decided:** **full paperwork.** The manager runs an assembly from the system end to end:

1. **Call (çağrı)** with the agenda and the second-meeting date, produced as a letter.
2. **Agenda items**, each marked whether it will be voted on.
3. **Attendance desk** on the day, with proxies.
4. **Quorum check** live, under the site's rules (Law 634 defaults: on the first call a majority by *both* number of owners and arsa payı; on the second call no quorum, decisions by majority of those present **(verify)**).
5. **Decisions** per agenda item with vote counts, outcome, and dissent notes (muhalefet şerhi).
6. **Minutes (tutanak)** as a PDF, filed into the site's documents automatically (Daily operations O8).
7. **Searchable decision history** with sequence numbers for transcription into the physical, notarized karar defteri.

Board (yönetim kurulu) meetings are *not* in the first governance release; the same machinery can be reused for them later with simpler rules.

**Plan changes:** backend §4.10 gains `meeting_notice` (see G3), `decision.dissents[]`, `meeting.eligibility_snapshot` (who could vote, with what weight, as of the meeting date), `decision.sequence_no` per site per year; quorum and thresholds are **site settings with Law 634 defaults**, not pack-only code.

## G2. Voting from the app is non-binding

**Decided:** owners can answer **non-binding polls** on agenda items before the meeting, so the manager sees the temperature. The binding vote happens in the room and the manager records the counts. Binding online voting is not built, because it has no clear basis under Law 634 **(verify)**.

**Plan changes:** `poll` (per agenda item, one answer per unit, open/close dates) separate from `vote`; the mobile plan's "online ballot" becomes "pre-meeting poll"; web §9.4 "live vote runner" becomes "record the in-room count".

## G3. Call letter generated; delivery tracked per owner

**Decided:** the system **generates the call letter** and keeps a **delivery record per owner**: registered-mail tracking number, signed by hand, or — if the site's yönetim planı allows it — email/SMS **(verify)**. The manager records which method was used and when. This is what a site needs if a decision is later challenged in court.

**Plan changes:** `meeting_notice` (meeting, person, method, sent_at, proof reference/file); the call letter and the per-owner delivery checklist are generated documents; the Turkish pack supplies the letter template and the legal timing checks (≥ 15 days before; second meeting 7–15 days after the first **(verify)**).

## G4. Attendance: tick-off screen plus QR check-in

**Decided:** on the day, the manager **ticks owners off on a screen** (laptop or tablet at the door) — present, or represented by whom. In addition, **owners can check in with a QR code** from the app, or printed in their call letter for people without the app. Both mark the same attendance record.

**Plan changes:** `attendance` (meeting, person, units represented, mode: in person / proxy, proxy_for, checked_in_at, checked_in_by); a per-person QR token in the call letter and the app; the printable hazirun cetveli (legal attendance list) is a generated document from the same data.

## G5. Decisions can trigger the change they describe

**Decided:** a recorded decision can **create the change it describes**, with the manager confirming each one, and the link kept for audit:

| Decision says | System offers to create |
|---|---|
| "Aidat becomes ₺2,000 from March" | The new effective-dated dues rate (Money M3), linked to the decision |
| "X elected manager / board / denetçi for one year" | The role membership with its term (G6) |
| "Budget approved" | The budget marked final (Money M3's light budget) |
| "Special assessment of ₺Y per unit for the roof" | A one-off owner-paid charge plan (Money M1) |

Anything else stays a text decision with an outcome.

**Plan changes:** `decision.effect` (kind + reference to the created record); the dues rate, membership and charge-plan records carry `decision_id`; the web "record decision" flow has an optional "apply" step.

## G6. Elections and terms

**Decided:** yönetici, board and denetçi memberships carry a **start and end date**, are **created from the electing decision** (G5), and the system **reminds the site before terms expire** so the assembly is scheduled in time (Law 634: the ordinary assembly is yearly, in January unless the yönetim planı says otherwise **(verify)**). A new election ends the old terms.

**Plan changes:** `membership.decision_id`, `valid_from/to` already exist; a `governance.term_expiry` reminder job; board and auditor memberships are site-owned (D2) — a management company cannot remove them without a decision.

## G7. Multi-block sites: site-level assembly only at first

**Decided:** one kat malikleri kurulu for the whole site in the first governance release. **Block-level assemblies** and the **representatives' council** (temsilciler kurulu, delegates voting with the weight of the units they represent) come later.

**Plan changes:** `meeting.scope` reserved (site now; block and council later); no delegate weighting in 0.2.

## G8. Roadmap

**Decided:** governance ships in **0.2**, directly after the web-only 0.1. **Gate and door control moves to 0.3** (decision D6 updated). Mobile follows both.

## Defaults set without asking (change them if wrong)

| Topic | Default |
|---|---|
| Vote weight | One vote per unit regardless of arsa payı; co-owners vote through one representative; a person may hold at most one-third of all votes **(verify, Law 634 art. 31)** |
| Proxies | Registered at the desk or in advance; legal caps enforced as site settings with Law 634 defaults (a person may represent at most 5 % of total votes; in buildings of 40 units or fewer, at most two others **(verify)**) |
| Decision thresholds | A per-topic table (simple majority of those present; majority by number *and* share; four-fifths; unanimity) with Law 634 defaults supplied by the Turkish pack and editable per site where the yönetim planı allows **(verify article mapping)** |
| Who can see decisions and minutes | All owners; tenants see only decisions the manager marks as "concerns residents" |
| Decision book | The system never claims to *be* the karar defteri; it produces numbered drafts and a yearly reminder for the notary closing **(verify)** |
| Absent owners | After the meeting the decisions are sent to absent owners by the sender-chosen channels (O5) and the delivery is recorded like the call |

## Still open in Governance

- Whether the site or only the Turkish pack may edit the threshold table (default: site-editable with pack defaults, since the yönetim planı can deviate where the law allows).
- Whether board meetings get the same machinery in 0.2 or later (default: later).
