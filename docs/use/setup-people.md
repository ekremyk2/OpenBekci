# Setup and people

## Sites, blocks and units

A **site** has one or more **blocks**, and blocks have **units** (flats, shops). A single building is a site with one block that the screens hide. Costs can be split across the whole site or among one block's units.

Parking spots and storage rooms are, by default, **appurtenances of a flat**: listed under it, with no dues or statement of their own. A site can mark specific spots as independent units with their own owner, share and charges, for sites where parking is sold or rented separately.

## Who has a login

| Who | What they can do |
|---|---|
| **Owners** (every co-owner) | See and pay for their units; open tickets; announcements, bookings, visitors, documents; answer polls. |
| **Tenants** | The tenant side of their unit's account, tickets, announcements, bookings, visitors. |
| **Household members** | Tickets, announcements, bookings, visitors. No money access unless the owner or tenant grants it. |
| **Site staff** (caretaker, guard) | A restricted login: the gate log and assigned tickets. |

## How people get in

1. **Import and invitation.** The manager imports units and people from a spreadsheet; each person gets an invitation by SMS or e-mail and is linked to their unit. The manager sees who has joined.
2. **Site QR code.** The site publishes a QR code (notice board, WhatsApp group). A resident scans it, signs up and claims a unit; the manager approves the claim.
3. **Invite from the same unit.** An owner or tenant invites their own household members without the manager. This can never grant money access by itself.
4. **The manager adds a person by hand** at any time.

## Who runs a site

All four shapes work: a **management company** with many sites (one login, a cross-site dashboard, staff on several sites, a clean hand-over when a site leaves); a **single resident yönetici**; an **elected board** with an auditor (shared management, the auditor sees everything read-only and can leave notes, memberships have terms); and an **external accountant** (money screens only). Built-in roles: `org_admin`, `site_manager`, `board_member`, `auditor`, `accountant`, `staff`. Memberships have start and end dates.

## National ids

The national id (TCKN) is optional, stored encrypted, shown masked, visible to managers only, and never required to use the app. The checksum is validated on entry (by the country pack).

## When people move

The manager sets a move-out or sale date. The system produces a **closing statement** and a **debt or no-debt certificate** (borç durum belgesi). Remaining debt stays with the person who left; the new owner or tenant starts at zero, and the manager can override this with an audited reason. There is no pro-rating: the whole month's dues go to whoever is there on the due date. Former residents keep read access to their own period only.

## What residents see about each other

Nothing by default: residents see only their own unit's people. A site can switch on an opt-in directory where each person chooses whether name and phone appear. Managers and board members see the full list; guards see only what their task needs (name, unit, plate, expected visitors, never contacts or balances).
