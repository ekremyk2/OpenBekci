# Deferred API operations

Operations that were in the draft contract (`api/src/openapi-draft.yaml`) and were removed at the `/v1` freeze (ADR-0003):
nothing serves them, no web client calls them, and no plan row up to release 1.0 needs them. Each can be added to `/v1`
later without breaking anything (additive), so removing them from the draft costs nothing and keeps the draft an honest list of plans.

| operationId | Method and path | Why deferred | If it returns |
|---|---|---|---|
| `me.startContactChange` | `POST /v1/me/contact-changes` | Changing the e-mail address or phone of an account has no plan row; sign-in identifiers are set at invitation | With a verified-change flow (code to the new address) and re-authentication |
| `me.confirmContactChange` | `POST /v1/me/contact-changes/{change_id}:confirm` | Same | Same |
| `auth.requestPasswordReset` | `POST /v1/auth/password-reset` | Phone-first sign-in (SMS code) and magic links already recover an account; no plan row for password reset | If password sign-in becomes common; needs the same enumeration protection as `auth.requestMagicLink` |
| `auth.resetPassword` | `POST /v1/auth/password-reset/consume` | Same | Same |
| `operations.createExport` | `POST /v1/orgs/{org_id}/exports` | Report exports are `format=` on the report operations; the export job is a CLI in 0.1 and the plan has no privacy or export UI before 1.0 | With the privacy UI; the schema `ExportRequest` stays in the spec |
| `governance.startMeeting` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}:start` | The meeting opens implicitly with the first call and the attendance desk; no web screen uses a separate start | If a manager wants an explicit "open the meeting" step |
| `community.getVisitorEntryQr` | `GET /v1/orgs/{org_id}/visitor-entries/{entry_id}/qr` | The mobile app and the web app render the server-issued `qr_token` themselves (`qr_flutter`, a QR component); no server renderer is wired | If a server-rendered image is needed for e-mail or SMS links |
| `community.getRecurringVisitorQr` | `GET /v1/orgs/{org_id}/recurring-visitors/{recurring_visitor_id}/qr` | Same | Same |

## Planned, not served, and kept in the draft

42 further draft operations are not served yet (imports of people and key values, unit tags, erasing a person, move-out preview,
credits, recurring expenses, payment allocation overrides, reconciliation reports, the audit trail and notes, jobs, the platform
operations, and others). Each belongs to a plan row up to release 1.0 or has a web screen written against it, so they stay in
the draft and in `api/DRAFT-INDEX.md` (Status "not yet served"). They enter `/v1` additively as they are built; a client
must not rely on an operation until it is in `api/openapi.yaml`.
