# Decisions — Daily operations

| | |
|---|---|
| Date | 2026-11-06 |
| Decided by | project owner, in a question-and-answer walkthrough |
| Status | Decided. The backend and web plans are to be revised to match; until then this file wins over conflicting text in `docs/plans/`. |

Covers tickets, announcements, documents and the notification plumbing. Dues reminders and the collections trail are in [money.md](money.md) (M4, M5).

## O1. Tickets: simple by default, configurable per site

**Decided:** a ticket starts with a simple flow — **open → in progress → done** — with a category, photos, assignment to a site staff member, and comments between the resident and management. But the flow is **configurable per site, Jira-style**: a site can add its own statuses (e.g. "waiting for parts", "waiting for board approval", "cancelled"), name them, and order them.

**Means:** the default works out of the box for a small building; a management company can define a richer process for its sites. Priorities, due dates and SLAs are not part of the first release but fit naturally as fields on the same configurable workflow later.

**Plan changes:** backend §4.7 — the fixed status enum becomes a per-site `ticket_workflow` (ordered statuses with "open-like / closed-like" semantics so reports and counts still work); a site inherits the default workflow until it edits it; internal notes (hidden from the resident) stay in. Reopen is just a transition back to an open-like status.

## O2. Outside technicians and contractors

**Decided:** **not in the first release.** A ticket can be assigned to a named vendor as a label; the manager relays information. Vendor logins (or one-time links) are reconsidered with the mobile app.

**Plan changes:** `ticket.assignee` is a person *or* a vendor reference; no vendor accounts.

## O3. Who sees which tickets

**Decided:** tickets are either **private** (about one flat: visible to that unit's people and management) or **common** (about shared spaces: elevator, garden, entrance, parking — visible to the whole site or block). Residents can **"+1" a common ticket** ("me too") instead of opening a duplicate, and the count is visible to management.

**Plan changes:** `ticket.visibility` (private / common) chosen by the category's default and overridable by the manager; `ticket_vote` (person, ticket) — one per person; the common-ticket list shows vote counts; duplicates are merged by the manager (votes carry over).

## O4. Announcements are one-way

**Decided:** management publishes, residents read, the manager sees **who has read**. Questions go through a ticket. No comments, reactions or polls under announcements in the first release.

**Plan changes:** as planned (backend §4.8); no comment entity.

## O5. Channels are chosen by the sender

**Decided:** for anything a person sends — announcements first of all — **the sender chooses the channels** for that message: in-app, email, SMS, (push later). The site pays the SMS bill, so the person pressing "publish" sees the recipient count and the SMS count before sending.

**Automatic messages** (nobody "sends" them) use these defaults, set without asking — change if wrong:

| Automatic message | Default channel |
|---|---|
| Sign-in codes, invitations | SMS when the person has a phone, otherwise email — always |
| Dues reminders (before and after due date) | In-app + SMS; a site setting can turn SMS off |
| Payment receipts | In-app + SMS; a site setting can turn SMS off |
| Ticket updates to the reporter | In-app + email; SMS off |
| Everything else | In-app only |

Residents can still mute non-essential types in their own preferences; sign-in codes and invitations cannot be muted.

**Plan changes:** backend §12 — the "notification type → default channels" table is replaced by: sender-chosen channels for authored messages (announcements, manual reminders) with a recipient/SMS count preview; site-level defaults for automatic messages; per-person mute for non-essential types. The "org can force channels" rule stays for sign-in and invitations only.

## O6. No chat

**Decided:** **no messaging inside the app** in the first release — not resident-to-manager, not group chat. Every request is a ticket, every notice is an announcement, so everything is tracked. Direct messaging is reconsidered after the mobile app ships.

## O7. Scheduled maintenance

**Decided:** **later, after 0.1.** Recurring expenses already cover the money side (Money M6). An equipment registry, service calendar and inspection reminders come as a follow-up to tickets.

## O8. Documents

**Decided:** the documents area holds, from the first release:

1. **Site documents with visibility rules** — yönetim planı, insurance, contracts, assembly minutes, financial reports, in folders; each folder or file visible to board only / owners / everyone / chosen units.
2. **Per-unit private documents** — a private folder per unit (lease contract, deposit receipt, ID copies) visible to that unit's owner/tenant and the manager only.
3. **Generated documents are filed automatically** — statements, receipts, borç durum belgesi, closing statements, later meeting minutes, land in the right folder with the right visibility without anyone uploading them.

Not in the first release: versions of a document (replacing a file overwrites it).

**Plan changes:** backend §4.9 — a per-unit folder created with the unit; `document.source` (uploaded / generated, with a link to the record that generated it); drop `document_version` from the first release.

## Defaults set without asking (change them if wrong)

| Topic | Default |
|---|---|
| Ticket categories | A starter list per site (plumbing, electrical, elevator, cleaning, security, noise/neighbor, other) with a default visibility (private/common) and a default assignee role each; editable per site |
| Who can open tickets | Any logged-in person of the site, including household members and staff; management can open one on a resident's behalf (phone call at the desk) |
| Photos | Up to 10 per ticket or comment, downscaled on upload, location data stripped |
| Announcement audience | Whole site by default; one block, owners only, tenants only, or chosen units as options; scheduling and pinning as planned |
| Read receipts | Counted when the announcement is opened in the web or app; an SMS/email delivery is *not* a read |
| Retention | Tickets and announcements are kept indefinitely; a site can archive closed tickets after N months (hidden, not deleted) |

## Still open in Daily operations

- Whether a site can let **board members** publish announcements, or only the manager (default: both, with a site switch).
- Whether common-ticket "+1" counts should be visible to residents or only to management (default: visible — it discourages duplicates).
