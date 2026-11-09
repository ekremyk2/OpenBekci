# Permissions

Generated with the draft contract (`api/src/openapi-draft.yaml`). Every org-scoped operation names the permissions it needs in `x-openbekci-roles`, as the README defines it: a map from role to the permissions that role holds *for that operation*. Role keys are memberships (`org_admin`, `site_manager`, `board_member`, `auditor`, `accountant`, `staff`, `guard`), occupancy kinds (`owner`, `tenant`, `household`, `former_occupant`) and two pseudo roles: `member` (any person with an active membership or occupancy in the organization) and `platform_admin` (instance operators). The matrix below is the draft seed for the A4 registry; the authz module owns the final one.

Where an operation lists several permissions, holding any one of them suffices. Scope rules (not repeated per permission): memberships apply within their scope (org, site or block); occupancy rights apply to the occupant's own unit and the side they occupy (S3); `household` never sees money unless `may_see_money` is set; `former_occupant` holds only `statements.read_own`, the receipts of that period and `documents.read_addressed`, bounded by the occupancy dates (S7); `staff` see counts and assigned work, never money (S3/S8); `guard` sees no contacts, balances or QR tokens.

## tenancy

| Permission | Meaning | Roles |
|---|---|---|
| `org.read` | Read the organization and its sites list. | `member` |
| `org.manage` | Update the organization. | `org_admin` |
| `sites.read` | Read a site, its blocks and basic settings. | `member` |
| `sites.manage` | Create and edit sites, settings, blocks, allocation keys and unit tags. | `org_admin`, `site_manager`, `board_member` |
| `sites.bank_details` | Read the site bank details shown to payers. | `org_admin`, `site_manager`, `board_member`, `auditor`, `accountant`, `owner`, `tenant` |
| `sites.export` | Export a site archive and import one (re-authentication required). | `org_admin` |
| `sites.offboard` | Offboard a site from the organization (re-authentication required). | `org_admin` |
| `units.read` | Read units, appurtenances, allocation keys and tags (occupants: their own units; never former occupants). | `org_admin`, `site_manager`, `board_member`, `auditor`, `accountant`, `staff`, `owner`, `tenant`, `household` |
| `units.manage` | Create and edit units. | `org_admin`, `site_manager`, `board_member` |
| `units.import` | Import units and people from CSV or XLSX. | `org_admin`, `site_manager` |
| `vehicles.read` | Read the plate registry and export it. | `org_admin`, `site_manager`, `board_member`, `auditor`, `owner`, `tenant`, `household` |
| `vehicles.manage` | Register and remove vehicles (occupants: their own unit). | `org_admin`, `site_manager`, `board_member`, `owner`, `tenant`, `household` |
| `jobs.read` | Read background jobs started by the caller (managers: all jobs of the organization). | `member` |
| `exports.create` | Start an asynchronous export. | `org_admin`, `site_manager`, `board_member`, `auditor`, `accountant` |

## people

| Permission | Meaning | Roles |
|---|---|---|
| `people.read` | Read persons and their contact data. | `org_admin`, `site_manager`, `board_member`, `auditor` |
| `people.manage` | Create and edit persons. | `org_admin`, `site_manager`, `board_member` |
| `people.national_id` | Read the masked national id and set or clear it. | `org_admin`, `site_manager` |
| `people.erase` | Erase a person for a privacy request (re-authentication required; blocked while they have an open balance unless overridden). | `org_admin` |
| `people.move_out` | Record a move-out or sale and issue the closing statement and certificate. | `org_admin`, `site_manager` |
| `occupancies.read` | Read occupancies (occupants: the people of their own unit). | `org_admin`, `site_manager`, `board_member`, `auditor`, `owner`, `tenant`, `household` |
| `occupancies.manage` | Create, change and end occupancies. | `org_admin`, `site_manager`, `board_member` |
| `occupancies.household` | End a household member's occupancy on the caller's own unit (`kind = household` only). | `owner`, `tenant` |
| `occupancies.grant_money` | Grant or withdraw money visibility for a household member. | `org_admin`, `site_manager`, `owner`, `tenant` |
| `memberships.read` | Read role memberships. | `org_admin`, `site_manager`, `board_member`, `auditor` |
| `memberships.manage` | Create and end role memberships (site-owned ones need re-authentication or a decision). | `org_admin`, `site_manager` |
| `invitations.manage` | Send, list, resend and revoke invitations. | `org_admin`, `site_manager`, `board_member` |
| `invitations.household` | Invite household members to the caller's own unit, and list and revoke the household invitations the caller sent. | `owner`, `tenant` |
| `claims.decide` | Approve or reject unit claims from the site QR path; read the site join code. | `org_admin`, `site_manager` |
| `directory.read` | Read the resident directory (filtered by each person's consent; managers and board see everyone). | `org_admin`, `site_manager`, `board_member`, `auditor`, `owner`, `tenant`, `household` |
| `vendors.read` | Read vendors. | `org_admin`, `site_manager`, `board_member`, `auditor`, `accountant`, `staff` |
| `vendors.manage` | Create and edit vendors. | `org_admin`, `site_manager`, `board_member`, `accountant` |
| `roles.read` | Read the role to permission matrix. | `org_admin`, `site_manager`, `board_member`, `auditor` |
| `me.manage` | Read and edit the caller's own person, consent and unit list in the organization. | `member` |

## money

| Permission | Meaning | Roles |
|---|---|---|
| `money.read` | Read ledger, charges, payments, plans, imports and the money overview. | `org_admin`, `site_manager`, `board_member`, `auditor`, `accountant` |
| `money.manage` | Edit accounts, funds, cash boxes, plans, budgets, policies and importers. | `org_admin`, `site_manager`, `board_member`, `accountant` |
| `money.post` | Post money movements: charges, expenses, bills, transfers, refunds, waivers, opening balances, reversals. | `org_admin`, `site_manager`, `board_member`, `accountant` |
| `money.lock` | Move the lock date and close fiscal years (moving it back needs re-authentication). | `org_admin`, `site_manager`, `accountant` |
| `payments.record` | Record and confirm payments, allocate them and issue receipts. Cash collectors (a person bound to a cash box: `staff` such as the kapici, or a board member) hold it only for payments into their own cash box. | `org_admin`, `site_manager`, `board_member`, `accountant`, `staff` |
| `payments.declare` | Declare a bank transfer already made ("I have paid"). | `owner`, `tenant` |
| `payments.checkout` | Start a card checkout for a unit side, poll it and list one's own checkouts (a resident only for a side their occupancy reaches; a household member needs `may_see_money`). | `org_admin`, `site_manager`, `owner`, `tenant`, `household` |
| `statements.read` | Read unit-side and person statements. | `org_admin`, `site_manager`, `board_member`, `auditor`, `accountant` |
| `statements.read_own` | Read statements, balances, charges, payments and receipts of the caller's own side only, including the resident lists of charges and payments (household: only with may_see_money; former occupants: their period). | `owner`, `tenant`, `household`, `former_occupant` |
| `reports.read` | Read the financial reports. | `org_admin`, `site_manager`, `board_member`, `auditor`, `accountant` |
| `reports.debtors` | Read the debtor list (never public). | `org_admin`, `site_manager`, `board_member`, `auditor`, `accountant` |
| `reports.aggregate` | Read gelir-gider and budget vs actual when the site enables financial transparency to owners. | `owner` |
| `collections.manage` | Run reminders, manage dunning stages and collection cases. | `org_admin`, `site_manager`, `board_member` |

## operations

| Permission | Meaning | Roles |
|---|---|---|
| `tickets.read` | Read every ticket of the site, internal comments included. | `org_admin`, `site_manager`, `board_member`, `auditor` |
| `tickets.read_assigned` | Read the tickets assigned to the caller and the common tickets of the site; internal notes only with `tickets.work`. | `staff` |
| `tickets.manage` | Edit the workflow and categories; assign, merge, change visibility, internal notes, open on behalf. | `org_admin`, `site_manager`, `board_member` |
| `tickets.work` | Move assigned tickets through the workflow and add internal notes. | `staff` |
| `tickets.open` | Open tickets, comment, vote (+1); read own and common tickets. | `org_admin`, `site_manager`, `board_member`, `staff`, `owner`, `tenant`, `household` |
| `announcements.read` | Read announcements addressed to the caller and mark them read. | `member` |
| `announcements.publish` | Create, schedule and publish announcements; see read receipts. | `org_admin`, `site_manager`, `board_member` |
| `documents.read` | Read folders and documents the caller's visibility allows. | `org_admin`, `site_manager`, `board_member`, `auditor`, `owner`, `tenant`, `household` |
| `documents.read_addressed` | Read only the documents addressed to the caller (closing statement, debt certificate); no folders. | `former_occupant` |
| `documents.manage` | Create folders, upload, move and delete documents. | `org_admin`, `site_manager`, `board_member` |
| `documents.upload_own` | Upload documents into the private folder of the caller's own unit. | `owner`, `tenant` |
| `files.upload` | Upload files. | `member` |
| `files.read` | Download files (the owning resource decides visibility). | `member` |
| `notifications.read` | Read the caller's own notifications, mutes and preferences. | `member` |
| `notifications.manage` | Edit the site defaults for automatic messages; read the SMS counter. | `org_admin`, `site_manager` |

## audit

| Permission | Meaning | Roles |
|---|---|---|
| `audit.read` | Read the audit log (PII appears as hashes). | `org_admin`, `site_manager`, `board_member`, `auditor` |
| `audit.report` | Write audit notes (auditor). | `auditor` |

## dashboards

| Permission | Meaning | Roles |
|---|---|---|
| `dashboard.site` | Read the site dashboard (receivables, cash and bank; never for staff or guards). | `org_admin`, `site_manager`, `board_member`, `auditor`, `accountant` |
| `dashboard.staff` | Read the counts-only staff dashboard (assigned and common tickets, expected visitors, parcels; no money). | `staff` |
| `dashboard.portfolio` | Read the organization portfolio dashboard. | `org_admin`, `site_manager`, `auditor`, `accountant` |
| `me.dashboard` | Read the caller's home summary. | `member` |

## codes

| Permission | Meaning | Roles |
|---|---|---|
| `codes.read` | Read enum and status labels. | `member` |

## governance

| Permission | Meaning | Roles |
|---|---|---|
| `meetings.read` | Read meetings, agenda, decisions and minutes (tenants: only decisions marked as concerning residents). | `org_admin`, `site_manager`, `board_member`, `auditor`, `owner`, `tenant` |
| `meetings.manage` | Call, run and close assemblies; notices, agenda, counts, minutes, term reminders and governance settings. | `org_admin`, `site_manager`, `board_member` |
| `decisions.manage` | Record decisions, dissents and apply their effects. | `org_admin`, `site_manager`, `board_member` |
| `polls.answer` | Answer a non-binding poll for the caller's own unit. | `owner` |
| `attendance.manage` | Tick off attendance and check owners in by QR. | `org_admin`, `site_manager`, `board_member` |

## community

| Permission | Meaning | Roles |
|---|---|---|
| `facilities.manage` | Edit facilities, their rules and blocked dates. | `org_admin`, `site_manager`, `board_member` |
| `bookings.read` | Read bookings (residents: own bookings and the taken slots). | `org_admin`, `site_manager`, `board_member`, `owner`, `tenant`, `household` |
| `bookings.create` | Book a facility (managers: on behalf of a unit). | `org_admin`, `site_manager`, `board_member`, `owner`, `tenant`, `household` |
| `bookings.manage` | Cancel any booking. | `org_admin`, `site_manager`, `board_member` |
| `visitors.read` | Read the site visitor log. | `org_admin`, `site_manager`, `board_member`, `auditor` |
| `visitors.manage` | Pre-register visitors, manage recurring visitors and cancel entries (residents: own unit). | `org_admin`, `site_manager`, `board_member`, `owner`, `tenant`, `household` |
| `visitors.decide` | Answer a visitor approval request for the caller's unit. | `owner`, `tenant`, `household` |
| `deliveries.read` | Read parcels logged against the caller's unit. | `owner`, `tenant`, `household` |
| `gate.operate` | Use the guard console: expected visitors, scanning, walk-ins, arrivals, parcels, minimized search. | `org_admin`, `site_manager`, `guard` |
| `gate.manage` | Set guard PINs. | `org_admin`, `site_manager` |

## access

| Permission | Meaning | Roles |
|---|---|---|
| `access.read` | Read agents, access points, credentials and agent status. | `org_admin`, `site_manager`, `board_member`, `auditor` |
| `access.manage` | Create and edit agents and access points; revoke, re-enrol, rotate; rebuild the allow-list; create and revoke manual tag and PIN credentials. | `org_admin`, `site_manager` |
| `access.open` | Open an access point manually. | `org_admin`, `site_manager`, `guard` |
| `access.view_events` | Read the access event log. | `org_admin`, `site_manager`, `board_member`, `auditor` |

## integrations

| Permission | Meaning | Roles |
|---|---|---|
| `apikeys.read` | List the organization's API keys (prefix and metadata, never a secret). | `org_admin` |
| `apikeys.manage` | Create, rotate and revoke API keys (re-authentication required; a key can never manage keys). | `org_admin` |
| `webhooks.read` | Read webhook endpoints and their delivery log. | `org_admin` |
| `webhooks.manage` | Create, change, delete, test and rotate the secret of webhook endpoints (never through an API key). | `org_admin` |

## packs

| Permission | Meaning | Roles |
|---|---|---|
| `packs.read` | List the packs compiled into the server with their settings schema and the organization's enablement (secret values are never returned). | `org_admin` |
| `packs.manage` | Enable and disable packs for the organization and change their settings. | `org_admin` |

## platform

| Permission | Meaning | Roles |
|---|---|---|
| `platform.read` | Read platform status. | `platform_admin` |
| `platform.manage` | Create and change organizations and instance settings. | `platform_admin` |

