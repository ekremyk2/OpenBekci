# Decisions — Setup & people

| | |
|---|---|
| Date | 2026-11-06 |
| Decided by | project owner, in a question-and-answer walkthrough |
| Status | Decided. The backend and web plans are to be revised to match; until then this file wins over conflicting text in `docs/plans/`. Builds on decision D2 (the site owns its data). |

## S1. Site shape

**Decided:** the first release serves **both single buildings and multi-block sites**. Blocks exist from day one: a cost can be split among one block's units or across the whole site (see Money M2). A single building is a site with one block the UI doesn't show. Block-level boards, meetings and representatives come with the governance area later.

**Plan changes:** keep Site → Block → Unit; `building` is auto-created and hidden for single buildings; allocation scope (site / block) is part of the money model; nothing else changes.

## S2. Parking, storage and similar

**Decided:** **chosen per site.** By default a parking spot or storage room (depo) is an **appurtenance of a flat** — listed under it, no dues or statement of its own. A site can mark specific spots as **independent units** with their own owner, share and charges (for sites where parking is sold or rented separately).

**Plan changes:** `unit.parent_unit_id` for appurtenances; a per-site default plus a per-unit override; independent parking/storage units behave like any other unit type.

## S3. Who gets a login

**Decided:** four kinds of people get their own account:

| Who | What they can do |
|---|---|
| **Owners** (every co-owner) | See and pay for their units, open tickets, announcements, bookings, visitors, documents, vote (later) |
| **Tenants** (the person on the contract) | The tenant side of their unit (Money M1), tickets, announcements, bookings, visitors |
| **Other household members** (spouse, adult children, flatmates) | Tickets, announcements, bookings, visitors — **no money** unless the unit's owner or tenant grants it |
| **Site staff** (kapıcı, guard, cleaner) | A restricted login for their tasks: gate log, assigned tickets. Mostly used on phones, so the full value arrives with the mobile app |

**Plan changes:** occupancy kinds `owner`, `tenant`, `resident` (household member) with a "may see money" flag grantable by the owner/tenant; a `staff` role scoped to a site; permission rules derive from occupancy for the first three and from role for staff.

## S4. How people get in

**Decided:** four paths, all available from the first release:

1. **Import + invitation** — the manager imports units and people from a spreadsheet (same step as the money opening balances, M7); each person receives an invitation by **SMS or email** with a link, sets a password, and is linked to their unit. The manager sees who has joined and who hasn't.
2. **Site QR / code** — the site publishes a QR code (on the notice board, in the WhatsApp group); a resident scans it, signs up, and **claims a unit**; the manager approves the claim before it takes effect.
3. **Invite from the same unit** — an owner or tenant can invite their own household members to their unit directly, without the manager. The invite creates a household-member occupancy on that unit only.
4. **Manager adds a person by hand** at any time, with an invitation sent the same way as (1).

**Plan changes:** `invitation` gains a kind (manager / site-code / household); a `unit_claim` with an approval queue; site-level QR generation; household invitations are scoped to the inviter's unit and cannot grant money access by themselves (S3).

## S5. Who runs a site

**Decided:** all four organizational shapes are supported in the first release:

| Shape | What the system does |
|---|---|
| **Professional management company with many sites** | One company login sees all its sites; a cross-site dashboard (overdue, cash, open tickets per site); staff assigned to several sites; a clean hand-over when a site leaves the company (D2: site export/import, site-owned board memberships) |
| **Single yönetici who is a resident** | A self-managed building: one person with the manager role; the company layer is invisible; minimal setup |
| **Elected board (yönetim kurulu) of 2–3 people plus a denetçi** | Board members share management rights; the auditor sees everything read-only and can write audit notes; memberships have terms (start/end) and, once governance exists, link to the assembly decision that elected them |
| **External accountant** | Money screens only, across one or more sites; no access to tickets, residents' contacts or documents |

**Plan changes:** built-in roles for the first release: `org_admin`, `site_manager`, `board_member`, `auditor`, `accountant`, `staff`. Memberships are time-bounded. Board and auditor memberships are **site-owned** (D2). The org-level portfolio dashboard is a first-release web feature, not a later one as the web plan currently has it.

## S6. National ID numbers (TCKN)

**Decided:** **optional, encrypted, for owners and tenants.** Captured when known — needed for legal follow-up (icra) and formal notices — stored encrypted, shown masked, visible to managers only, never required to use the app. The checksum is validated on entry.

**Plan changes:** this is core, not Turkish-pack: a generic `person.national_id` (encrypted, with country code) with the TCKN checksum as the Turkish validator. The pack's separate `pack_tr_person_identity` table is dropped. Audit diffs never contain the number (review §5, erasure design).

## S7. When people move

**Decided:**

- The manager sets a **move-out / sale date**. The system produces a **closing statement** for the leaving person and a **debt / no-debt certificate (borç durum belgesi)** for the sale or deposit return.
- **Remaining debt stays with the leaving person** on their personal account for follow-up; the new owner or tenant starts at zero. The manager can override when the parties agreed otherwise (audited).
- **No pro-rating**: the whole month's dues go to whoever is in place on the due date.
- **Former residents keep read access** to their own period — statements and receipts from their time there — and see nothing about the unit after they left.

**Plan changes:** a `move_out` / `transfer` use case on occupancy; person-level receivable for carried debt (Money M1 already gives persons a balance); certificate and closing-statement templates in core; occupancy-derived visibility bounded by the occupancy's dates.

## S8. What residents see about each other

**Decided:** **nothing by default.** Residents see only their own unit's people. A site can enable an **opt-in directory** where each person chooses whether their name and phone appear. Managers and board members always see the full list; staff see only what their task needs (guards: name, unit, plate, expected visitors — never contacts or balances).

**Plan changes:** `person.directory_consent` per site; directory endpoint filtered by it; data-minimized staff endpoints (review §4).

## Defaults set without asking (change them if wrong)

| Topic | Default |
|---|---|
| Co-ownership | Several owners per unit with percentage shares; the unit still has one owner-side balance; any co-owner may pay; one of them is marked the correspondent for notices |
| Unit identifiers | A door number (what people say: "A-12") and an optional legal reference (tapu bağımsız bölüm no) kept separately |
| Unit types | Flat, shop, office, parking, storage, common (kapıcı dairesi, management room), other |
| Vacancy | Derived from occupancies (no owner-side resident and no tenant); not a status the manager sets |
| Phone numbers | International format; Turkish numbers entered as 05xx are normalized automatically |
| Household money access | Off by default; the owner or tenant turns it on per household member |
| Site hand-over (D2) | Export of a whole site (people, occupancies, money history, documents, decisions) to a file; import into another organization preserving identifiers; residents' logins survive because accounts are instance-wide |

## Still open in Setup & people

- Whether the manager, the board, or both approve a unit claim from the site QR path (default: the manager; the board can be given the right).
- Whether staff logins ship in the web-only 0.1 or wait for the mobile app (default: the role exists in 0.1; the gate-log screen is a web page sized for a tablet; the phone experience comes with mobile).
