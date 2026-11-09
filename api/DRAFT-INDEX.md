# Draft API index

The builders' checklist (the Status column says whether `api/src/openapi.yaml`, the served root, already has the operation; `tenancy.importPeople`, `tenancy.importAllocationKeyValues`, the unit-tag resources, `people.erasePerson`, `people.previewMoveOut` and `people.updateMembership` are the tenancy and people operations not yet served): every operation of the draft contract (`api/src/openapi-draft.yaml`, bundled to `api/openapi-draft.yaml`). Generated; regenerate by editing the path files, not this table.

Total: 457 new operations, plus the 15 already served (instance, auth, me) which stay in `api/src/openapi.yaml`. Release 0.1 operations are `x-stability: stable`; 0.2, 0.3, 0.4 (and the 1.0 platform items) are `preview`. Where several permissions are listed, any one suffices.

| Module | Operations |
|---|---|
| tenancy | 48 |
| people | 53 |
| me | 9 |
| money | 138 |
| operations | 56 |
| audit | 4 |
| governance | 45 |
| community | 43 |
| access | 21 |
| integrations | 12 |
| agent | 4 |
| dashboards | 4 |
| codes | 3 |
| platform | 9 |
| packs | 4 |
| auth | 3 |

| Module | operationId | Method and path | Release | Permission | Idempotency | Status |
|---|---|---|---|---|---|---|
| tenancy | `tenancy.getOrg` | `GET /v1/orgs/{org_id}` | 0.1 | `org.read` | - | served |
| tenancy | `tenancy.updateOrg` | `PATCH /v1/orgs/{org_id}` | 0.1 | `org.manage` | optional | served |
| tenancy | `tenancy.listSites` | `GET /v1/orgs/{org_id}/sites` | 0.1 | `sites.read` | - | served |
| tenancy | `tenancy.createSite` | `POST /v1/orgs/{org_id}/sites` | 0.1 | `sites.manage` | optional | served |
| tenancy | `tenancy.getSite` | `GET /v1/orgs/{org_id}/sites/{site_id}` | 0.1 | `sites.read` | - | served |
| tenancy | `tenancy.updateSite` | `PATCH /v1/orgs/{org_id}/sites/{site_id}` | 0.1 | `sites.manage` | optional | served |
| tenancy | `tenancy.getSiteSettings` | `GET /v1/orgs/{org_id}/sites/{site_id}/settings` | 0.1 | `sites.read` | - | served |
| tenancy | `tenancy.updateSiteSettings` | `PATCH /v1/orgs/{org_id}/sites/{site_id}/settings` | 0.1 | `sites.manage` | optional | served |
| tenancy | `tenancy.getBankDetails` | `GET /v1/orgs/{org_id}/sites/{site_id}/bank-details` | 0.1 | `sites.bank_details` | - | served |
| tenancy | `tenancy.setBankDetails` | `PUT /v1/orgs/{org_id}/sites/{site_id}/bank-details` | 0.1 | `sites.manage` | optional | served |
| tenancy | `tenancy.listBlocks` | `GET /v1/orgs/{org_id}/blocks` | 0.1 | `sites.read` | - | served |
| tenancy | `tenancy.createBlock` | `POST /v1/orgs/{org_id}/blocks` | 0.1 | `sites.manage` | optional | served |
| tenancy | `tenancy.getBlock` | `GET /v1/orgs/{org_id}/blocks/{block_id}` | 0.1 | `sites.read` | - | served |
| tenancy | `tenancy.updateBlock` | `PATCH /v1/orgs/{org_id}/blocks/{block_id}` | 0.1 | `sites.manage` | optional | served |
| tenancy | `tenancy.deleteBlock` | `DELETE /v1/orgs/{org_id}/blocks/{block_id}` | 0.1 | `sites.manage` | optional | served |
| tenancy | `tenancy.listUnits` | `GET /v1/orgs/{org_id}/units` | 0.1 | `units.read` | - | served |
| tenancy | `tenancy.createUnit` | `POST /v1/orgs/{org_id}/units` | 0.1 | `units.manage` | optional | served |
| tenancy | `tenancy.getUnit` | `GET /v1/orgs/{org_id}/units/{unit_id}` | 0.1 | `units.read` | - | served |
| tenancy | `tenancy.updateUnit` | `PATCH /v1/orgs/{org_id}/units/{unit_id}` | 0.1 | `units.manage` | optional | served |
| tenancy | `tenancy.deleteUnit` | `DELETE /v1/orgs/{org_id}/units/{unit_id}` | 0.1 | `units.manage` | optional | served |
| tenancy | `tenancy.importUnits` | `POST /v1/orgs/{org_id}/units:import` | 0.1 | `units.import` | optional | served |
| tenancy | `tenancy.importPeople` | `POST /v1/orgs/{org_id}/persons:import` | 0.1 | `units.import` | optional | not yet served |
| tenancy | `tenancy.listAllocationKeys` | `GET /v1/orgs/{org_id}/allocation-keys` | 0.1 | `units.read` | - | served |
| tenancy | `tenancy.createAllocationKey` | `POST /v1/orgs/{org_id}/allocation-keys` | 0.1 | `sites.manage` | optional | served |
| tenancy | `tenancy.getAllocationKey` | `GET /v1/orgs/{org_id}/allocation-keys/{key_id}` | 0.1 | `units.read` | - | served |
| tenancy | `tenancy.updateAllocationKey` | `PATCH /v1/orgs/{org_id}/allocation-keys/{key_id}` | 0.1 | `sites.manage` | optional | served |
| tenancy | `tenancy.deleteAllocationKey` | `DELETE /v1/orgs/{org_id}/allocation-keys/{key_id}` | 0.1 | `sites.manage` | optional | served |
| tenancy | `tenancy.listAllocationKeyValues` | `GET /v1/orgs/{org_id}/allocation-keys/{key_id}/values` | 0.1 | `units.read` | - | served |
| tenancy | `tenancy.putAllocationKeyValues` | `PUT /v1/orgs/{org_id}/allocation-keys/{key_id}/values` | 0.1 | `sites.manage` | optional | served |
| tenancy | `tenancy.importAllocationKeyValues` | `POST /v1/orgs/{org_id}/allocation-keys/{key_id}/values:import` | 0.1 | `sites.manage` | optional | not yet served |
| tenancy | `tenancy.listUnitTags` | `GET /v1/orgs/{org_id}/unit-tags` | 0.1 | `units.read` | - | not yet served |
| tenancy | `tenancy.createUnitTag` | `POST /v1/orgs/{org_id}/unit-tags` | 0.1 | `sites.manage` | optional | not yet served |
| tenancy | `tenancy.getUnitTag` | `GET /v1/orgs/{org_id}/unit-tags/{tag_id}` | 0.1 | `units.read` | - | not yet served |
| tenancy | `tenancy.updateUnitTag` | `PATCH /v1/orgs/{org_id}/unit-tags/{tag_id}` | 0.1 | `sites.manage` | optional | not yet served |
| tenancy | `tenancy.deleteUnitTag` | `DELETE /v1/orgs/{org_id}/unit-tags/{tag_id}` | 0.1 | `sites.manage` | optional | not yet served |
| tenancy | `tenancy.getUnitTagMembers` | `GET /v1/orgs/{org_id}/unit-tags/{tag_id}/units` | 0.1 | `units.read` | - | not yet served |
| tenancy | `tenancy.setUnitTagMembers` | `PUT /v1/orgs/{org_id}/unit-tags/{tag_id}/units` | 0.1 | `sites.manage` | optional | not yet served |
| tenancy | `tenancy.exportSite` | `POST /v1/orgs/{org_id}/sites/{site_id}:export` | 0.1 | `sites.export` | optional | served |
| tenancy | `tenancy.importSite` | `POST /v1/orgs/{org_id}/sites:import` | 0.1 | `sites.export` | optional | served |
| tenancy | `tenancy.offboardSite` | `POST /v1/orgs/{org_id}/sites/{site_id}:offboard` | 0.1 | `sites.offboard` | optional | served |
| tenancy | `tenancy.listSiteTransfers` | `GET /v1/orgs/{org_id}/site-transfers` | 0.1 | `sites.export` | - | served |
| tenancy | `tenancy.getSiteTransfer` | `GET /v1/orgs/{org_id}/site-transfers/{transfer_id}` | 0.1 | `sites.export` | - | served |
| tenancy | `tenancy.listVehicles` | `GET /v1/orgs/{org_id}/vehicles` | 0.1 | `vehicles.read` | - | served |
| tenancy | `tenancy.createVehicle` | `POST /v1/orgs/{org_id}/vehicles` | 0.1 | `vehicles.manage` | optional | served |
| tenancy | `tenancy.getVehicle` | `GET /v1/orgs/{org_id}/vehicles/{vehicle_id}` | 0.1 | `vehicles.read` | - | served |
| tenancy | `tenancy.updateVehicle` | `PATCH /v1/orgs/{org_id}/vehicles/{vehicle_id}` | 0.1 | `vehicles.manage` | optional | served |
| tenancy | `tenancy.deleteVehicle` | `DELETE /v1/orgs/{org_id}/vehicles/{vehicle_id}` | 0.1 | `vehicles.manage` | optional | served |
| tenancy | `tenancy.exportVehicles` | `GET /v1/orgs/{org_id}/vehicles:export` | 0.1 | `vehicles.read` | - | served |
| people | `people.listPersons` | `GET /v1/orgs/{org_id}/persons` | 0.1 | `people.read` | - | served |
| people | `people.createPerson` | `POST /v1/orgs/{org_id}/persons` | 0.1 | `people.manage` | optional | served |
| people | `people.getPerson` | `GET /v1/orgs/{org_id}/persons/{person_id}` | 0.1 | `people.read` | - | served |
| people | `people.updatePerson` | `PATCH /v1/orgs/{org_id}/persons/{person_id}` | 0.1 | `people.manage` | optional | served |
| people | `people.erasePerson` | `POST /v1/orgs/{org_id}/persons/{person_id}:erase` | 0.1 | `people.erase` | required | not yet served |
| people | `people.getNationalId` | `GET /v1/orgs/{org_id}/persons/{person_id}/national-id` | 0.1 | `people.national_id` | - | served |
| people | `people.setNationalId` | `PUT /v1/orgs/{org_id}/persons/{person_id}/national-id` | 0.1 | `people.national_id` | optional | served |
| people | `people.clearNationalId` | `DELETE /v1/orgs/{org_id}/persons/{person_id}/national-id` | 0.1 | `people.national_id` | optional | served |
| people | `people.listOccupancies` | `GET /v1/orgs/{org_id}/occupancies` | 0.1 | `occupancies.read` | - | served |
| people | `people.createOccupancy` | `POST /v1/orgs/{org_id}/occupancies` | 0.1 | `occupancies.manage` | optional | served |
| people | `people.getOccupancy` | `GET /v1/orgs/{org_id}/occupancies/{occupancy_id}` | 0.1 | `occupancies.read` | - | served |
| people | `people.updateOccupancy` | `PATCH /v1/orgs/{org_id}/occupancies/{occupancy_id}` | 0.1 | `occupancies.manage` | optional | served |
| people | `people.deleteOccupancy` | `DELETE /v1/orgs/{org_id}/occupancies/{occupancy_id}` | 0.1 | `occupancies.manage` | optional | served |
| people | `people.setMoneyAccess` | `PUT /v1/orgs/{org_id}/occupancies/{occupancy_id}/money-access` | 0.1 | `occupancies.grant_money` | optional | served |
| people | `people.previewMoveOut` | `POST /v1/orgs/{org_id}/occupancies/{occupancy_id}/move-out:preview` | 0.1 | `people.move_out` | optional | not yet served |
| people | `people.moveOut` | `POST /v1/orgs/{org_id}/occupancies/{occupancy_id}/move-out` | 0.1 | `people.move_out` | required | served |
| people | `people.listMoveOuts` | `GET /v1/orgs/{org_id}/move-outs` | 0.1 | `people.move_out` | - | served |
| people | `people.getMoveOut` | `GET /v1/orgs/{org_id}/move-outs/{move_out_id}` | 0.1 | `people.move_out` | - | served |
| people | `people.listMemberships` | `GET /v1/orgs/{org_id}/memberships` | 0.1 | `memberships.read` | - | served |
| people | `people.createMembership` | `POST /v1/orgs/{org_id}/memberships` | 0.1 | `memberships.manage` | optional | served |
| people | `people.getMembership` | `GET /v1/orgs/{org_id}/memberships/{membership_id}` | 0.1 | `memberships.read` | - | served |
| people | `people.updateMembership` | `PATCH /v1/orgs/{org_id}/memberships/{membership_id}` | 0.1 | `memberships.manage` | optional | not yet served |
| people | `people.deleteMembership` | `DELETE /v1/orgs/{org_id}/memberships/{membership_id}` | 0.1 | `memberships.manage` | optional | served |
| people | `people.listInvitations` | `GET /v1/orgs/{org_id}/invitations` | 0.1 | `invitations.manage` | - | served |
| people | `people.createInvitation` | `POST /v1/orgs/{org_id}/invitations` | 0.1 | `invitations.manage` | optional | served |
| people | `people.getInvitation` | `GET /v1/orgs/{org_id}/invitations/{invitation_id}` | 0.1 | `invitations.manage` | - | served |
| people | `people.revokeInvitation` | `DELETE /v1/orgs/{org_id}/invitations/{invitation_id}` | 0.1 | `invitations.manage` | optional | served |
| people | `people.resendInvitation` | `POST /v1/orgs/{org_id}/invitations/{invitation_id}:resend` | 0.1 | `invitations.manage` | optional | served |
| people | `people.createHouseholdInvitation` | `POST /v1/orgs/{org_id}/units/{unit_id}/household-invitations` | 0.1 | `invitations.household` | optional | served |
| people | `people.previewInvitation` | `GET /v1/invitations/{token}` | 0.1 | public / self | - | served |
| people | `people.acceptInvitation` | `POST /v1/invitations/{token}:accept` | 0.1 | public / self | optional | served |
| people | `people.getJoinCode` | `GET /v1/orgs/{org_id}/sites/{site_id}/join-code` | 0.1 | `claims.decide` | - | served |
| people | `people.setJoinCodeEnabled` | `PUT /v1/orgs/{org_id}/sites/{site_id}/join-code` | 0.1 | `claims.decide` | optional | served |
| people | `people.rotateJoinCode` | `POST /v1/orgs/{org_id}/sites/{site_id}/join-code:rotate` | 0.1 | `claims.decide` | optional | served |
| people | `people.getJoinInfo` | `GET /v1/join/{code}` | 0.1 | public / self | - | served |
| people | `people.listJoinUnits` | `GET /v1/join/{code}/units` | 0.1 | public / self | - | served |
| people | `people.joinSignUp` | `POST /v1/join/{code}:sign-up` | 0.1 | public / self | optional | served |
| people | `people.createUnitClaim` | `POST /v1/join/{code}/claims` | 0.1 | public / self | optional | served |
| people | `people.listUnitClaims` | `GET /v1/orgs/{org_id}/unit-claims` | 0.1 | `claims.decide` | - | served |
| people | `people.getUnitClaim` | `GET /v1/orgs/{org_id}/unit-claims/{claim_id}` | 0.1 | `claims.decide` | - | served |
| people | `people.approveUnitClaim` | `POST /v1/orgs/{org_id}/unit-claims/{claim_id}:approve` | 0.1 | `claims.decide` | optional | served |
| people | `people.rejectUnitClaim` | `POST /v1/orgs/{org_id}/unit-claims/{claim_id}:reject` | 0.1 | `claims.decide` | optional | served |
| people | `people.listMyUnitClaims` | `GET /v1/me/unit-claims` | 0.1 | `me.read` | - | served |
| people | `people.listDirectory` | `GET /v1/orgs/{org_id}/directory` | 0.1 | `directory.read` | - | served |
| people | `people.listVendors` | `GET /v1/orgs/{org_id}/vendors` | 0.1 | `vendors.read` | - | served |
| people | `people.createVendor` | `POST /v1/orgs/{org_id}/vendors` | 0.1 | `vendors.manage` | optional | served |
| people | `people.getVendor` | `GET /v1/orgs/{org_id}/vendors/{vendor_id}` | 0.1 | `vendors.read` | - | served |
| people | `people.updateVendor` | `PATCH /v1/orgs/{org_id}/vendors/{vendor_id}` | 0.1 | `vendors.manage` | optional | served |
| people | `people.listRoles` | `GET /v1/orgs/{org_id}/roles` | 0.1 | `roles.read` | - | served |
| people | `people.getMyPerson` | `GET /v1/orgs/{org_id}/me/person` | 0.1 | `me.manage` | - | served |
| people | `people.updateMyPerson` | `PATCH /v1/orgs/{org_id}/me/person` | 0.1 | `me.manage` | optional | served |
| people | `people.setMyDirectoryConsent` | `PUT /v1/orgs/{org_id}/me/directory-consent` | 0.1 | `me.manage` | optional | served |
| people | `people.listMyOccupancies` | `GET /v1/orgs/{org_id}/me/occupancies` | 0.1 | `me.manage` | - | served |
| me | `me.listHouseholdInvitations` | `GET /v1/orgs/{org_id}/me/household-invitations` | 0.1 | `invitations.household` | - | served |
| me | `me.revokeHouseholdInvitation` | `DELETE /v1/orgs/{org_id}/me/household-invitations/{invitation_id}` | 0.1 | `invitations.household` | optional | served |
| me | `me.endHouseholdOccupancy` | `DELETE /v1/orgs/{org_id}/me/household-occupancies/{occupancy_id}` | 0.1 | `occupancies.household` | optional | served |
| money | `money.listAccounts` | `GET /v1/orgs/{org_id}/accounts` | 0.1 | `money.read` | - | served |
| money | `money.createAccount` | `POST /v1/orgs/{org_id}/accounts` | 0.1 | `money.manage` | optional | served |
| money | `money.getAccount` | `GET /v1/orgs/{org_id}/accounts/{account_id}` | 0.1 | `money.read` | - | served |
| money | `money.updateAccount` | `PATCH /v1/orgs/{org_id}/accounts/{account_id}` | 0.1 | `money.manage` | optional | served |
| money | `money.listFunds` | `GET /v1/orgs/{org_id}/funds` | 0.1 | `money.read` | - | served |
| money | `money.createFund` | `POST /v1/orgs/{org_id}/funds` | 0.1 | `money.manage` | optional | served |
| money | `money.getFund` | `GET /v1/orgs/{org_id}/funds/{fund_id}` | 0.1 | `money.read` | - | served |
| money | `money.updateFund` | `PATCH /v1/orgs/{org_id}/funds/{fund_id}` | 0.1 | `money.manage` | optional | served |
| money | `money.listCashBoxes` | `GET /v1/orgs/{org_id}/cash-boxes` | 0.1 | `money.read` | - | served |
| money | `money.createCashBox` | `POST /v1/orgs/{org_id}/cash-boxes` | 0.1 | `money.manage` | optional | served |
| money | `money.getCashBox` | `GET /v1/orgs/{org_id}/cash-boxes/{cash_box_id}` | 0.1 | `money.read` | - | served |
| money | `money.updateCashBox` | `PATCH /v1/orgs/{org_id}/cash-boxes/{cash_box_id}` | 0.1 | `money.manage` | optional | served |
| money | `money.listFiscalYears` | `GET /v1/orgs/{org_id}/fiscal-years` | 0.1 | `money.read` | - | served |
| money | `money.createFiscalYear` | `POST /v1/orgs/{org_id}/fiscal-years` | 0.1 | `money.manage` | optional | served |
| money | `money.getFiscalYear` | `GET /v1/orgs/{org_id}/fiscal-years/{fiscal_year_id}` | 0.1 | `money.read` | - | served |
| money | `money.closeFiscalYear` | `POST /v1/orgs/{org_id}/fiscal-years/{fiscal_year_id}:close` | 0.1 | `money.lock` | required | served |
| money | `money.approveFiscalYear` | `POST /v1/orgs/{org_id}/fiscal-years/{fiscal_year_id}:approve` | 0.1 | `money.lock` | required | served |
| money | `money.getLockDate` | `GET /v1/orgs/{org_id}/sites/{site_id}/lock-date` | 0.1 | `money.read` | - | served |
| money | `money.setLockDate` | `PUT /v1/orgs/{org_id}/sites/{site_id}/lock-date` | 0.1 | `money.lock` | optional | served |
| money | `money.listJournalEntries` | `GET /v1/orgs/{org_id}/journal-entries` | 0.1 | `money.read` | - | served |
| money | `money.getJournalEntry` | `GET /v1/orgs/{org_id}/journal-entries/{entry_id}` | 0.1 | `money.read` | - | served |
| money | `money.reverseJournalEntry` | `POST /v1/orgs/{org_id}/journal-entries/{entry_id}:reverse` | 0.1 | `money.post` | required | served |
| money | `money.listOpeningBalances` | `GET /v1/orgs/{org_id}/opening-balances` | 0.1 | `money.read` | - | served |
| money | `money.createOpeningBalance` | `POST /v1/orgs/{org_id}/opening-balances` | 0.1 | `money.post` | required | served |
| money | `money.getOpeningBalance` | `GET /v1/orgs/{org_id}/opening-balances/{opening_balance_id}` | 0.1 | `money.read` | - | served |
| money | `money.voidOpeningBalance` | `POST /v1/orgs/{org_id}/opening-balances/{opening_balance_id}:void` | 0.1 | `money.post` | required | served |
| money | `money.importOpeningBalances` | `POST /v1/orgs/{org_id}/opening-balances:import` | 0.1 | `money.post` | required | served |
| money | `money.listBudgets` | `GET /v1/orgs/{org_id}/budgets` | 0.1 | `money.read` | - | served |
| money | `money.createBudget` | `POST /v1/orgs/{org_id}/budgets` | 0.1 | `money.manage` | optional | served |
| money | `money.getBudget` | `GET /v1/orgs/{org_id}/budgets/{budget_id}` | 0.1 | `money.read` | - | served |
| money | `money.updateBudget` | `PATCH /v1/orgs/{org_id}/budgets/{budget_id}` | 0.1 | `money.manage` | optional | served |
| money | `money.deleteBudget` | `DELETE /v1/orgs/{org_id}/budgets/{budget_id}` | 0.1 | `money.manage` | optional | served |
| money | `money.getBudgetLines` | `GET /v1/orgs/{org_id}/budgets/{budget_id}/lines` | 0.1 | `money.read` | - | served |
| money | `money.putBudgetLines` | `PUT /v1/orgs/{org_id}/budgets/{budget_id}/lines` | 0.1 | `money.manage` | optional | served |
| money | `money.finalizeBudget` | `POST /v1/orgs/{org_id}/budgets/{budget_id}:finalize` | 0.1 | `money.manage` | optional | served |
| money | `money.listChargePlans` | `GET /v1/orgs/{org_id}/charge-plans` | 0.1 | `money.read` | - | served |
| money | `money.createChargePlan` | `POST /v1/orgs/{org_id}/charge-plans` | 0.1 | `money.manage` | optional | served |
| money | `money.getChargePlan` | `GET /v1/orgs/{org_id}/charge-plans/{plan_id}` | 0.1 | `money.read` | - | served |
| money | `money.updateChargePlan` | `PATCH /v1/orgs/{org_id}/charge-plans/{plan_id}` | 0.1 | `money.manage` | optional | served |
| money | `money.listChargePlanRates` | `GET /v1/orgs/{org_id}/charge-plans/{plan_id}/rates` | 0.1 | `money.read` | - | served |
| money | `money.createChargePlanRate` | `POST /v1/orgs/{org_id}/charge-plans/{plan_id}/rates` | 0.1 | `money.manage` | required | served |
| money | `money.deleteChargePlanRate` | `DELETE /v1/orgs/{org_id}/charge-plans/{plan_id}/rates/{rate_id}` | 0.1 | `money.manage` | required | served |
| money | `money.previewChargePlan` | `POST /v1/orgs/{org_id}/charge-plans:preview` | 0.1 | `money.manage` | optional | served |
| money | `money.getChargeGenerationStatus` | `GET /v1/orgs/{org_id}/charge-generation` | 0.1 | `money.read` | - | served |
| money | `money.previewExpenseAllocation` | `POST /v1/orgs/{org_id}/expense-allocations:preview` | 0.1 | `money.manage` | optional | not yet served |
| money | `money.createExpenseAllocation` | `POST /v1/orgs/{org_id}/expense-allocations` | 0.1 | `money.post` | required | not yet served |
| money | `money.listCharges` | `GET /v1/orgs/{org_id}/charges` | 0.1 | `money.read`, `statements.read_own` | - | served |
| money | `money.createCharge` | `POST /v1/orgs/{org_id}/charges` | 0.1 | `money.post` | required | served |
| money | `money.getCharge` | `GET /v1/orgs/{org_id}/charges/{charge_id}` | 0.1 | `money.read`, `statements.read_own` | - | served |
| money | `money.cancelCharge` | `POST /v1/orgs/{org_id}/charges/{charge_id}:cancel` | 0.1 | `money.post` | required | served |
| money | `money.adjustCharge` | `POST /v1/orgs/{org_id}/charges/{charge_id}:adjust` | 0.1 | `money.post` | required | not yet served |
| money | `money.getLateFeePolicy` | `GET /v1/orgs/{org_id}/late-fee-policy` | 0.1 | `money.read` | - | served |
| money | `money.setLateFeePolicy` | `PUT /v1/orgs/{org_id}/late-fee-policy` | 0.1 | `money.manage` | optional | served |
| money | `money.listLateFeeAccruals` | `GET /v1/orgs/{org_id}/late-fee-accruals` | 0.1 | `money.read` | - | served |
| money | `money.listLateFeeBookings` | `GET /v1/orgs/{org_id}/late-fee-bookings` | 0.1 | `money.read` | - | served |
| money | `money.listLateFeeWaivers` | `GET /v1/orgs/{org_id}/late-fee-waivers` | 0.1 | `money.read` | - | served |
| money | `money.createLateFeeWaiver` | `POST /v1/orgs/{org_id}/late-fee-waivers` | 0.1 | `money.post` | required | served |
| money | `money.listLateFeeFreezes` | `GET /v1/orgs/{org_id}/late-fee-freezes` | 0.1 | `money.read` | - | served |
| money | `money.createLateFeeFreeze` | `POST /v1/orgs/{org_id}/late-fee-freezes` | 0.1 | `money.manage` | optional | served |
| money | `money.getLateFeeFreeze` | `GET /v1/orgs/{org_id}/late-fee-freezes/{freeze_id}` | 0.1 | `money.read` | - | served |
| money | `money.updateLateFeeFreeze` | `PATCH /v1/orgs/{org_id}/late-fee-freezes/{freeze_id}` | 0.1 | `money.manage` | optional | served |
| money | `money.listPayments` | `GET /v1/orgs/{org_id}/payments` | 0.1 | `money.read`, `statements.read_own` | - | served |
| money | `money.createPayment` | `POST /v1/orgs/{org_id}/payments` | 0.1 | `payments.record` | required | served |
| money | `money.getPayment` | `GET /v1/orgs/{org_id}/payments/{payment_id}` | 0.1 | `money.read`, `statements.read_own` | - | served |
| money | `money.voidPayment` | `POST /v1/orgs/{org_id}/payments/{payment_id}:void` | 0.1 | `payments.record` | required | served |
| money | `money.getPaymentReceipt` | `GET /v1/orgs/{org_id}/payments/{payment_id}/receipt` | 0.1 | `payments.record`, `statements.read_own` | - | served |
| money | `money.sendPaymentReceipt` | `POST /v1/orgs/{org_id}/payments/{payment_id}:send-receipt` | 0.1 | `payments.record` | optional | served |
| money | `money.setPaymentAllocations` | `PUT /v1/orgs/{org_id}/payments/{payment_id}/allocations` | 0.1 | `payments.record` | required | not yet served |
| money | `money.previewPaymentAllocation` | `POST /v1/orgs/{org_id}/payments:preview-allocation` | 0.1 | `payments.record` | optional | not yet served |
| money | `money.declarePayment` | `POST /v1/orgs/{org_id}/payments:declare` | 0.1 | `payments.declare` | required | served |
| money | `money.confirmPayment` | `POST /v1/orgs/{org_id}/payments/{payment_id}:confirm` | 0.1 | `payments.record` | required | served |
| money | `money.rejectPayment` | `POST /v1/orgs/{org_id}/payments/{payment_id}:reject` | 0.1 | `payments.record` | optional | served |
| money | `money.refundPayment` | `POST /v1/orgs/{org_id}/payments/{payment_id}:refund` | 0.4 | `money.post` | required | served |
| money | `money.createCheckout` | `POST /v1/orgs/{org_id}/checkouts` | 0.4 | `payments.checkout` | required | served |
| money | `money.listCheckouts` | `GET /v1/orgs/{org_id}/checkouts` | 0.4 | `money.read` | - | served |
| money | `money.getCheckout` | `GET /v1/orgs/{org_id}/checkouts/{checkout_id}` | 0.4 | `payments.checkout` | - | served |
| money | `money.listMyCheckouts` | `GET /v1/orgs/{org_id}/me/checkouts` | 0.4 | `payments.checkout` | - | served |
| money | `money.listPaymentSettlements` | `GET /v1/orgs/{org_id}/payment-settlements` | 0.4 | `money.read` | - | served |
| money | `money.getPaymentClearing` | `GET /v1/orgs/{org_id}/payment-clearing` | 0.4 | `money.read` | - | served |
| money | `money.payoutClientMoney` | `POST /v1/orgs/{org_id}/payment-clearing:payout` | 0.4 | `money.post` | required | served |
| money | `money.listCredits` | `GET /v1/orgs/{org_id}/credits` | 0.1 | `money.read`, `statements.read_own` | - | not yet served |
| money | `money.applyCredit` | `POST /v1/orgs/{org_id}/credits/{credit_id}:apply` | 0.1 | `payments.record` | required | not yet served |
| money | `money.listTransfers` | `GET /v1/orgs/{org_id}/transfers` | 0.1 | `money.read` | - | served |
| money | `money.createTransfer` | `POST /v1/orgs/{org_id}/transfers` | 0.1 | `money.post` | required | served |
| money | `money.getTransfer` | `GET /v1/orgs/{org_id}/transfers/{transfer_id}` | 0.1 | `money.read` | - | served |
| money | `money.listRefunds` | `GET /v1/orgs/{org_id}/refunds` | 0.1 | `money.read` | - | served |
| money | `money.createRefund` | `POST /v1/orgs/{org_id}/refunds` | 0.1 | `money.post` | required | served |
| money | `money.getRefund` | `GET /v1/orgs/{org_id}/refunds/{refund_id}` | 0.1 | `money.read` | - | served |
| money | `money.listBankImporters` | `GET /v1/orgs/{org_id}/bank-importers` | 0.1 | `money.read` | - | served |
| money | `money.listBankStatementImports` | `GET /v1/orgs/{org_id}/bank-statement-imports` | 0.1 | `money.read` | - | served |
| money | `money.createBankStatementImport` | `POST /v1/orgs/{org_id}/bank-statement-imports` | 0.1 | `money.post` | optional | served |
| money | `money.getBankStatementImport` | `GET /v1/orgs/{org_id}/bank-statement-imports/{import_id}` | 0.1 | `money.read` | - | served |
| money | `money.deleteBankStatementImport` | `DELETE /v1/orgs/{org_id}/bank-statement-imports/{import_id}` | 0.1 | `money.post` | optional | served |
| money | `money.getImportReconciliation` | `GET /v1/orgs/{org_id}/bank-statement-imports/{import_id}/reconciliation` | 0.1 | `money.read` | - | served |
| money | `money.listBankTransactions` | `GET /v1/orgs/{org_id}/bank-transactions` | 0.1 | `money.read` | - | served |
| money | `money.getBankTransaction` | `GET /v1/orgs/{org_id}/bank-transactions/{transaction_id}` | 0.1 | `money.read` | - | served |
| money | `money.matchBankTransaction` | `POST /v1/orgs/{org_id}/bank-transactions/{transaction_id}:match` | 0.1 | `payments.record` | required | served |
| money | `money.unmatchBankTransaction` | `POST /v1/orgs/{org_id}/bank-transactions/{transaction_id}:unmatch` | 0.1 | `payments.record` | required | served |
| money | `money.ignoreBankTransaction` | `POST /v1/orgs/{org_id}/bank-transactions/{transaction_id}:ignore` | 0.1 | `payments.record` | optional | served |
| money | `money.listExpenses` | `GET /v1/orgs/{org_id}/expenses` | 0.1 | `money.read` | - | served |
| money | `money.createExpense` | `POST /v1/orgs/{org_id}/expenses` | 0.1 | `money.post` | required | served |
| money | `money.getExpense` | `GET /v1/orgs/{org_id}/expenses/{expense_id}` | 0.1 | `money.read` | - | served |
| money | `money.voidExpense` | `POST /v1/orgs/{org_id}/expenses/{expense_id}:void` | 0.1 | `money.post` | required | served |
| money | `money.listRecurringExpenses` | `GET /v1/orgs/{org_id}/recurring-expenses` | 0.1 | `money.read` | - | not yet served |
| money | `money.createRecurringExpense` | `POST /v1/orgs/{org_id}/recurring-expenses` | 0.1 | `money.post` | required | not yet served |
| money | `money.getRecurringExpense` | `GET /v1/orgs/{org_id}/recurring-expenses/{recurring_expense_id}` | 0.1 | `money.read` | - | not yet served |
| money | `money.updateRecurringExpense` | `PATCH /v1/orgs/{org_id}/recurring-expenses/{recurring_expense_id}` | 0.1 | `money.post` | optional | not yet served |
| money | `money.deleteRecurringExpense` | `DELETE /v1/orgs/{org_id}/recurring-expenses/{recurring_expense_id}` | 0.1 | `money.post` | optional | not yet served |
| money | `money.listBills` | `GET /v1/orgs/{org_id}/bills` | 0.1 | `money.read` | - | served |
| money | `money.createBill` | `POST /v1/orgs/{org_id}/bills` | 0.1 | `money.post` | required | served |
| money | `money.getBill` | `GET /v1/orgs/{org_id}/bills/{bill_id}` | 0.1 | `money.read` | - | served |
| money | `money.createBillPayment` | `POST /v1/orgs/{org_id}/bills/{bill_id}/payments` | 0.1 | `money.post` | required | served |
| money | `money.listDunningStages` | `GET /v1/orgs/{org_id}/dunning-stages` | 0.1 | `money.read` | - | served |
| money | `money.createDunningStage` | `POST /v1/orgs/{org_id}/dunning-stages` | 0.1 | `collections.manage` | optional | served |
| money | `money.getDunningStage` | `GET /v1/orgs/{org_id}/dunning-stages/{stage_id}` | 0.1 | `money.read` | - | served |
| money | `money.updateDunningStage` | `PATCH /v1/orgs/{org_id}/dunning-stages/{stage_id}` | 0.1 | `collections.manage` | optional | served |
| money | `money.deleteDunningStage` | `DELETE /v1/orgs/{org_id}/dunning-stages/{stage_id}` | 0.1 | `collections.manage` | optional | served |
| money | `money.previewDunningRun` | `POST /v1/orgs/{org_id}/dunning-runs:preview` | 0.1 | `collections.manage` | optional | served |
| money | `money.createDunningRun` | `POST /v1/orgs/{org_id}/dunning-runs` | 0.1 | `collections.manage` | required | served |
| money | `money.listDunningEvents` | `GET /v1/orgs/{org_id}/dunning-events` | 0.1 | `money.read` | - | served |
| money | `money.getDunningLetter` | `GET /v1/orgs/{org_id}/dunning-letter` | 0.1 | `collections.manage` | - | served |
| money | `money.listCollectionCases` | `GET /v1/orgs/{org_id}/collection-cases` | 0.1 | `money.read` | - | served |
| money | `money.createCollectionCase` | `POST /v1/orgs/{org_id}/collection-cases` | 0.1 | `collections.manage` | optional | served |
| money | `money.getCollectionCase` | `GET /v1/orgs/{org_id}/collection-cases/{case_id}` | 0.1 | `money.read` | - | served |
| money | `money.updateCollectionCase` | `PATCH /v1/orgs/{org_id}/collection-cases/{case_id}` | 0.1 | `collections.manage` | optional | served |
| money | `money.closeCollectionCase` | `POST /v1/orgs/{org_id}/collection-cases/{case_id}:close` | 0.1 | `collections.manage` | optional | served |
| money | `money.getDebtBreakdown` | `GET /v1/orgs/{org_id}/debt-breakdown` | 0.1 | `collections.manage` | - | served |
| money | `money.getUnitStatement` | `GET /v1/orgs/{org_id}/units/{unit_id}/statements/{debtor_kind}` | 0.1 | `statements.read`, `statements.read_own` | - | served |
| money | `money.getPersonStatement` | `GET /v1/orgs/{org_id}/persons/{person_id}/statement` | 0.1 | `statements.read`, `statements.read_own` | - | served |
| money | `money.sendStatement` | `POST /v1/orgs/{org_id}/statements:send` | 0.1 | `statements.read` | required | served |
| money | `money.getMyBalances` | `GET /v1/orgs/{org_id}/me/balances` | 0.1 | `statements.read_own` | - | not yet served |
| money | `money.getIncomeExpenseReport` | `GET /v1/orgs/{org_id}/reports/income-expense` | 0.1 | `reports.read`, `reports.aggregate` | - | served |
| money | `money.getDebtorReport` | `GET /v1/orgs/{org_id}/reports/debtors` | 0.1 | `reports.debtors` | - | served |
| money | `money.getBudgetVsActualReport` | `GET /v1/orgs/{org_id}/reports/budget-vs-actual` | 0.1 | `reports.read`, `reports.aggregate` | - | served |
| money | `money.getTrialBalanceReport` | `GET /v1/orgs/{org_id}/reports/trial-balance` | 0.1 | `reports.read` | - | served |
| money | `money.getReceivablesAgingReport` | `GET /v1/orgs/{org_id}/reports/receivables-aging` | 0.1 | `reports.read` | - | served |
| money | `money.getCollectorReconciliationReport` | `GET /v1/orgs/{org_id}/reports/collector-reconciliation` | 0.1 | `reports.read` | - | not yet served |
| money | `money.getBankReconciliationReport` | `GET /v1/orgs/{org_id}/reports/bank-reconciliation` | 0.1 | `reports.read` | - | not yet served |
| money | `money.getFundBalancesReport` | `GET /v1/orgs/{org_id}/reports/fund-balances` | 0.1 | `reports.read` | - | served |
| money | `money.getMoneyOverview` | `GET /v1/orgs/{org_id}/money/overview` | 0.1 | `money.read` | - | served |
| operations | `operations.getTicketWorkflow` | `GET /v1/orgs/{org_id}/ticket-workflow` | 0.1 | `tickets.read`, `tickets.open`, `tickets.read_assigned` | - | served |
| operations | `operations.updateTicketWorkflow` | `PUT /v1/orgs/{org_id}/ticket-workflow` | 0.1 | `tickets.manage` | optional | served |
| operations | `operations.listTicketCategories` | `GET /v1/orgs/{org_id}/ticket-categories` | 0.1 | `tickets.read`, `tickets.open`, `tickets.read_assigned` | - | served |
| operations | `operations.createTicketCategory` | `POST /v1/orgs/{org_id}/ticket-categories` | 0.1 | `tickets.manage` | optional | served |
| operations | `operations.getTicketCategory` | `GET /v1/orgs/{org_id}/ticket-categories/{category_id}` | 0.1 | `tickets.read`, `tickets.open`, `tickets.read_assigned` | - | served |
| operations | `operations.updateTicketCategory` | `PATCH /v1/orgs/{org_id}/ticket-categories/{category_id}` | 0.1 | `tickets.manage` | optional | served |
| operations | `operations.listTickets` | `GET /v1/orgs/{org_id}/tickets` | 0.1 | `tickets.read`, `tickets.open`, `tickets.read_assigned` | - | served |
| operations | `operations.createTicket` | `POST /v1/orgs/{org_id}/tickets` | 0.1 | `tickets.open` | optional | served |
| operations | `operations.getTicket` | `GET /v1/orgs/{org_id}/tickets/{ticket_id}` | 0.1 | `tickets.read`, `tickets.open`, `tickets.read_assigned` | - | served |
| operations | `operations.updateTicket` | `PATCH /v1/orgs/{org_id}/tickets/{ticket_id}` | 0.1 | `tickets.manage` | optional | served |
| operations | `operations.transitionTicket` | `POST /v1/orgs/{org_id}/tickets/{ticket_id}:transition` | 0.1 | `tickets.manage`, `tickets.work` | optional | served |
| operations | `operations.mergeTicket` | `POST /v1/orgs/{org_id}/tickets/{ticket_id}:merge` | 0.1 | `tickets.manage` | optional | served |
| operations | `operations.voteTicket` | `PUT /v1/orgs/{org_id}/tickets/{ticket_id}/vote` | 0.1 | `tickets.open` | optional | served |
| operations | `operations.unvoteTicket` | `DELETE /v1/orgs/{org_id}/tickets/{ticket_id}/vote` | 0.1 | `tickets.open` | optional | served |
| operations | `operations.listTicketComments` | `GET /v1/orgs/{org_id}/tickets/{ticket_id}/comments` | 0.1 | `tickets.read`, `tickets.open`, `tickets.read_assigned` | - | served |
| operations | `operations.createTicketComment` | `POST /v1/orgs/{org_id}/tickets/{ticket_id}/comments` | 0.1 | `tickets.read`, `tickets.open`, `tickets.read_assigned`, `tickets.work` | optional | served |
| operations | `operations.createTicketAttachment` | `POST /v1/orgs/{org_id}/tickets/{ticket_id}/attachments` | 0.1 | `tickets.open` | optional | served |
| operations | `operations.deleteTicketAttachment` | `DELETE /v1/orgs/{org_id}/tickets/{ticket_id}/attachments/{attachment_id}` | 0.1 | `tickets.open` | optional | served |
| operations | `operations.listTicketHistory` | `GET /v1/orgs/{org_id}/tickets/{ticket_id}/history` | 0.1 | `tickets.read`, `tickets.open`, `tickets.read_assigned` | - | served |
| operations | `operations.listAnnouncements` | `GET /v1/orgs/{org_id}/announcements` | 0.1 | `announcements.publish`, `announcements.read` | - | served |
| operations | `operations.createAnnouncement` | `POST /v1/orgs/{org_id}/announcements` | 0.1 | `announcements.publish` | optional | served |
| operations | `operations.getAnnouncement` | `GET /v1/orgs/{org_id}/announcements/{announcement_id}` | 0.1 | `announcements.publish`, `announcements.read` | - | served |
| operations | `operations.updateAnnouncement` | `PATCH /v1/orgs/{org_id}/announcements/{announcement_id}` | 0.1 | `announcements.publish` | optional | served |
| operations | `operations.deleteAnnouncement` | `DELETE /v1/orgs/{org_id}/announcements/{announcement_id}` | 0.1 | `announcements.publish` | optional | served |
| operations | `operations.previewAnnouncement` | `POST /v1/orgs/{org_id}/announcements:preview` | 0.1 | `announcements.publish` | optional | served |
| operations | `operations.publishAnnouncement` | `POST /v1/orgs/{org_id}/announcements/{announcement_id}:publish` | 0.1 | `announcements.publish` | required | served |
| operations | `operations.archiveAnnouncement` | `POST /v1/orgs/{org_id}/announcements/{announcement_id}:archive` | 0.1 | `announcements.publish` | optional | served |
| operations | `operations.listAnnouncementReads` | `GET /v1/orgs/{org_id}/announcements/{announcement_id}/reads` | 0.1 | `announcements.publish` | - | served |
| operations | `operations.markAnnouncementRead` | `PUT /v1/orgs/{org_id}/announcements/{announcement_id}/read` | 0.1 | `announcements.read` | optional | served |
| operations | `operations.listFolders` | `GET /v1/orgs/{org_id}/folders` | 0.1 | `documents.read` | - | served |
| operations | `operations.createFolder` | `POST /v1/orgs/{org_id}/folders` | 0.1 | `documents.manage` | optional | served |
| operations | `operations.getFolder` | `GET /v1/orgs/{org_id}/folders/{folder_id}` | 0.1 | `documents.read` | - | served |
| operations | `operations.updateFolder` | `PATCH /v1/orgs/{org_id}/folders/{folder_id}` | 0.1 | `documents.manage` | optional | served |
| operations | `operations.deleteFolder` | `DELETE /v1/orgs/{org_id}/folders/{folder_id}` | 0.1 | `documents.manage` | optional | served |
| operations | `operations.listDocuments` | `GET /v1/orgs/{org_id}/documents` | 0.1 | `documents.read`, `documents.read_addressed` | - | served |
| operations | `operations.createDocument` | `POST /v1/orgs/{org_id}/documents` | 0.1 | `documents.manage`, `documents.upload_own` | optional | served |
| operations | `operations.getDocument` | `GET /v1/orgs/{org_id}/documents/{document_id}` | 0.1 | `documents.read`, `documents.read_addressed` | - | served |
| operations | `operations.updateDocument` | `PATCH /v1/orgs/{org_id}/documents/{document_id}` | 0.1 | `documents.manage`, `documents.upload_own` | optional | served |
| operations | `operations.deleteDocument` | `DELETE /v1/orgs/{org_id}/documents/{document_id}` | 0.1 | `documents.manage`, `documents.upload_own` | optional | served |
| operations | `operations.listNotifications` | `GET /v1/orgs/{org_id}/me/notifications` | 0.1 | `notifications.read` | - | served |
| operations | `operations.getUnreadCount` | `GET /v1/orgs/{org_id}/me/notifications/unread-count` | 0.1 | `notifications.read` | - | served |
| operations | `operations.markNotificationRead` | `POST /v1/orgs/{org_id}/me/notifications/{notification_id}:read` | 0.1 | `notifications.read` | optional | served |
| operations | `operations.markAllNotificationsRead` | `POST /v1/orgs/{org_id}/me/notifications:read-all` | 0.1 | `notifications.read` | optional | served |
| operations | `operations.getNotificationDefaults` | `GET /v1/orgs/{org_id}/notification-defaults` | 0.1 | `notifications.manage` | - | served |
| operations | `operations.setNotificationDefaults` | `PUT /v1/orgs/{org_id}/notification-defaults` | 0.1 | `notifications.manage` | optional | served |
| operations | `operations.getNotificationMutes` | `GET /v1/orgs/{org_id}/me/notification-mutes` | 0.1 | `notifications.read` | - | served |
| operations | `operations.setNotificationMutes` | `PUT /v1/orgs/{org_id}/me/notification-mutes` | 0.1 | `notifications.read` | optional | served |
| operations | `operations.getSmsUsage` | `GET /v1/orgs/{org_id}/sms-usage` | 0.1 | `notifications.manage` | - | served |
| operations | `operations.uploadFile` | `POST /v1/orgs/{org_id}/files` | 0.1 | `files.upload` | optional | served |
| operations | `operations.getFileMeta` | `GET /v1/orgs/{org_id}/files/{file_id}/meta` | 0.1 | `files.read` | - | served |
| operations | `operations.downloadFile` | `GET /v1/orgs/{org_id}/files/{file_id}` | 0.1 | `files.read` | - | served |
| operations | `operations.deleteFile` | `DELETE /v1/orgs/{org_id}/files/{file_id}` | 0.1 | `files.upload` | optional | served |
| operations | `operations.listJobs` | `GET /v1/orgs/{org_id}/jobs` | 0.1 | `jobs.read` | - | not yet served |
| operations | `operations.getJob` | `GET /v1/orgs/{org_id}/jobs/{job_id}` | 0.1 | `jobs.read` | - | not yet served |
| operations | `operations.createExport` | `POST /v1/orgs/{org_id}/exports` | 0.1 | `exports.create` | optional | not yet served |
| audit | `audit.listEvents` | `GET /v1/orgs/{org_id}/audit-events` | 0.1 | `audit.read` | - | not yet served |
| audit | `audit.getEvent` | `GET /v1/orgs/{org_id}/audit-events/{event_id}` | 0.1 | `audit.read` | - | not yet served |
| audit | `audit.listNotes` | `GET /v1/orgs/{org_id}/audit-notes` | 0.1 | `audit.read` | - | not yet served |
| audit | `audit.createNote` | `POST /v1/orgs/{org_id}/audit-notes` | 0.1 | `audit.report` | optional | not yet served |
| governance | `governance.getGovernanceSettings` | `GET /v1/orgs/{org_id}/sites/{site_id}/governance-settings` | 0.2 | `meetings.read` | - | served |
| governance | `governance.setGovernanceSettings` | `PUT /v1/orgs/{org_id}/sites/{site_id}/governance-settings` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.listGovernancePresets` | `GET /v1/orgs/{org_id}/governance-presets` | 0.2 | `meetings.read` | - | served |
| governance | `governance.listMeetings` | `GET /v1/orgs/{org_id}/meetings` | 0.2 | `meetings.read` | - | served |
| governance | `governance.createMeeting` | `POST /v1/orgs/{org_id}/meetings` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.getMeeting` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}` | 0.2 | `meetings.read` | - | served |
| governance | `governance.updateMeeting` | `PATCH /v1/orgs/{org_id}/meetings/{meeting_id}` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.deleteMeeting` | `DELETE /v1/orgs/{org_id}/meetings/{meeting_id}` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.callMeeting` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}:call` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.startMeeting` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}:start` | 0.2 | `meetings.manage` | optional | not yet served |
| governance | `governance.closeMeeting` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}:close` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.cancelMeeting` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}:cancel` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.getMeetingQuorum` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/quorum` | 0.2 | `meetings.manage`, `attendance.manage` | - | served |
| governance | `governance.getCallLetter` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/call-letter` | 0.2 | `meetings.read` | - | served |
| governance | `governance.getMeetingDocument` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/documents/{kind}` | 0.4 | `meetings.manage` | - | served (not in the original draft) |
| governance | `governance.getAttendanceSheet` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/attendance-sheet` | 0.2 | `attendance.manage` | - | served |
| governance | `governance.getMyMeeting` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/me` | 0.2 | `meetings.read` | - | served |
| governance | `governance.generateMinutes` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}/minutes:generate` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.getMinutes` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/minutes` | 0.2 | `meetings.read` | - | served |
| governance | `governance.listMeetingNotices` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/notices` | 0.2 | `meetings.manage` | - | served |
| governance | `governance.recordMeetingNotice` | `PUT /v1/orgs/{org_id}/meetings/{meeting_id}/notices/{person_id}` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.recordMeetingNoticesBulk` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}/notices:bulk` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.notifyMeetingDecisions` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}:notify-decisions` | 0.2 | `meetings.manage` | required | served |
| governance | `governance.listAgendaItems` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/agenda-items` | 0.2 | `meetings.read` | - | served |
| governance | `governance.createAgendaItem` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}/agenda-items` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.updateAgendaItem` | `PATCH /v1/orgs/{org_id}/agenda-items/{agenda_item_id}` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.deleteAgendaItem` | `DELETE /v1/orgs/{org_id}/agenda-items/{agenda_item_id}` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.reorderAgendaItems` | `PUT /v1/orgs/{org_id}/meetings/{meeting_id}/agenda-items:order` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.getPoll` | `GET /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll` | 0.2 | `meetings.read` | - | served |
| governance | `governance.setPoll` | `PUT /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll` | 0.2 | `meetings.manage` | optional | served |
| governance | `governance.answerPoll` | `PUT /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll/answer` | 0.2 | `polls.answer` | optional | served |
| governance | `governance.getPollResults` | `GET /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll/results` | 0.2 | `meetings.manage`, `polls.answer` | - | served |
| governance | `governance.listPollAnswers` | `GET /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll/answers` | 0.2 | `meetings.manage` | - | served |
| governance | `governance.getVoteCounts` | `GET /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/vote-counts` | 0.2 | `meetings.read` | - | served |
| governance | `governance.recordVoteCounts` | `PUT /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/vote-counts` | 0.2 | `decisions.manage` | optional | served |
| governance | `governance.listAttendance` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/attendance` | 0.2 | `attendance.manage` | - | served |
| governance | `governance.recordAttendance` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}/attendance` | 0.2 | `attendance.manage` | optional | served |
| governance | `governance.removeAttendance` | `DELETE /v1/orgs/{org_id}/meetings/{meeting_id}/attendance/{attendance_id}` | 0.2 | `attendance.manage` | optional | served |
| governance | `governance.checkInAttendance` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}/attendance:checkin` | 0.2 | `attendance.manage` | optional | served |
| governance | `governance.listDecisions` | `GET /v1/orgs/{org_id}/decisions` | 0.2 | `meetings.read` | - | served |
| governance | `governance.createDecision` | `POST /v1/orgs/{org_id}/decisions` | 0.2 | `decisions.manage` | optional | served |
| governance | `governance.getDecision` | `GET /v1/orgs/{org_id}/decisions/{decision_id}` | 0.2 | `meetings.read` | - | served |
| governance | `governance.updateDecision` | `PATCH /v1/orgs/{org_id}/decisions/{decision_id}` | 0.2 | `decisions.manage` | optional | served |
| governance | `governance.applyDecision` | `POST /v1/orgs/{org_id}/decisions/{decision_id}:apply` | 0.2 | `decisions.manage` | required | served |
| governance | `governance.listTermReminders` | `GET /v1/orgs/{org_id}/term-reminders` | 0.2 | `meetings.manage` | - | served |
| community | `community.listFacilities` | `GET /v1/orgs/{org_id}/facilities` | 0.3 | `bookings.read`, `facilities.manage` | - | served |
| community | `community.createFacility` | `POST /v1/orgs/{org_id}/facilities` | 0.3 | `facilities.manage` | optional | served |
| community | `community.getFacility` | `GET /v1/orgs/{org_id}/facilities/{facility_id}` | 0.3 | `bookings.read`, `facilities.manage` | - | served |
| community | `community.updateFacility` | `PATCH /v1/orgs/{org_id}/facilities/{facility_id}` | 0.3 | `facilities.manage` | optional | served |
| community | `community.deleteFacility` | `DELETE /v1/orgs/{org_id}/facilities/{facility_id}` | 0.3 | `facilities.manage` | optional | served |
| community | `community.listFacilityBlocks` | `GET /v1/orgs/{org_id}/facilities/{facility_id}/blocks` | 0.3 | `bookings.read`, `facilities.manage` | - | served |
| community | `community.createFacilityBlock` | `POST /v1/orgs/{org_id}/facilities/{facility_id}/blocks` | 0.3 | `facilities.manage` | optional | served |
| community | `community.deleteFacilityBlock` | `DELETE /v1/orgs/{org_id}/facilities/{facility_id}/blocks/{block_id}` | 0.3 | `facilities.manage` | optional | served |
| community | `community.getFacilityAvailability` | `GET /v1/orgs/{org_id}/facilities/{facility_id}/availability` | 0.3 | `bookings.create` | - | served |
| community | `community.listBookings` | `GET /v1/orgs/{org_id}/bookings` | 0.3 | `bookings.read` | - | served |
| community | `community.createBooking` | `POST /v1/orgs/{org_id}/bookings` | 0.3 | `bookings.create` | required | served |
| community | `community.getBooking` | `GET /v1/orgs/{org_id}/bookings/{booking_id}` | 0.3 | `bookings.read` | - | served |
| community | `community.cancelBooking` | `POST /v1/orgs/{org_id}/bookings/{booking_id}:cancel` | 0.3 | `bookings.manage`, `bookings.create` | required | served |
| community | `community.listVisitorEntries` | `GET /v1/orgs/{org_id}/visitor-entries` | 0.3 | `visitors.read`, `visitors.manage` | - | served |
| community | `community.createVisitorEntry` | `POST /v1/orgs/{org_id}/visitor-entries` | 0.3 | `visitors.manage` | optional | served |
| community | `community.getVisitorEntry` | `GET /v1/orgs/{org_id}/visitor-entries/{entry_id}` | 0.3 | `visitors.read`, `visitors.manage` | - | served |
| community | `community.updateVisitorEntry` | `PATCH /v1/orgs/{org_id}/visitor-entries/{entry_id}` | 0.3 | `visitors.manage` | optional | served |
| community | `community.cancelVisitorEntry` | `POST /v1/orgs/{org_id}/visitor-entries/{entry_id}:cancel` | 0.3 | `visitors.manage` | optional | served |
| community | `community.getVisitorEntryQr` | `GET /v1/orgs/{org_id}/visitor-entries/{entry_id}/qr` | 0.3 | `visitors.manage` | - | not yet served |
| community | `community.listRecurringVisitors` | `GET /v1/orgs/{org_id}/recurring-visitors` | 0.3 | `visitors.read`, `visitors.manage` | - | served |
| community | `community.createRecurringVisitor` | `POST /v1/orgs/{org_id}/recurring-visitors` | 0.3 | `visitors.manage` | optional | served |
| community | `community.getRecurringVisitor` | `GET /v1/orgs/{org_id}/recurring-visitors/{recurring_visitor_id}` | 0.3 | `visitors.read`, `visitors.manage` | - | served |
| community | `community.updateRecurringVisitor` | `PATCH /v1/orgs/{org_id}/recurring-visitors/{recurring_visitor_id}` | 0.3 | `visitors.manage` | optional | served |
| community | `community.revokeRecurringVisitor` | `POST /v1/orgs/{org_id}/recurring-visitors/{recurring_visitor_id}:revoke` | 0.3 | `visitors.manage` | optional | served |
| community | `community.getRecurringVisitorQr` | `GET /v1/orgs/{org_id}/recurring-visitors/{recurring_visitor_id}/qr` | 0.3 | `visitors.manage` | - | not yet served |
| community | `community.listVisitorApprovalRequests` | `GET /v1/orgs/{org_id}/visitor-approval-requests` | 0.3 | `visitors.decide` | - | served |
| community | `community.decideVisitorApprovalRequest` | `POST /v1/orgs/{org_id}/visitor-approval-requests/{request_id}:decide` | 0.3 | `visitors.decide` | optional | served |
| community | `community.listDeliveries` | `GET /v1/orgs/{org_id}/deliveries` | 0.3 | `visitors.read`, `deliveries.read` | - | served |
| community | `community.getGateConsole` | `GET /v1/orgs/{org_id}/gate/console` | 0.3 | `gate.operate` | - | served |
| community | `community.listGateVisitors` | `GET /v1/orgs/{org_id}/gate/visitors` | 0.3 | `gate.operate` | - | served |
| community | `community.getGateVisitor` | `GET /v1/orgs/{org_id}/gate/visitors/{entry_id}` | 0.3 | `gate.operate` | - | served |
| community | `community.scanGateVisitor` | `POST /v1/orgs/{org_id}/gate/visitors:scan` | 0.3 | `gate.operate` | optional | served |
| community | `community.createWalkInVisitor` | `POST /v1/orgs/{org_id}/gate/visitors:walk-in` | 0.3 | `gate.operate` | optional | served |
| community | `community.arriveGateVisitor` | `POST /v1/orgs/{org_id}/gate/visitors/{entry_id}:arrive` | 0.3 | `gate.operate` | optional | served |
| community | `community.leaveGateVisitor` | `POST /v1/orgs/{org_id}/gate/visitors/{entry_id}:leave` | 0.3 | `gate.operate` | optional | served |
| community | `community.getGateVisitorCallContact` | `GET /v1/orgs/{org_id}/gate/visitors/{entry_id}/call-contact` | 0.3 | `gate.operate` | - | served |
| community | `community.searchGatePlates` | `GET /v1/orgs/{org_id}/gate/plates` | 0.3 | `gate.operate` | - | served |
| community | `community.listGateDeliveries` | `GET /v1/orgs/{org_id}/gate/deliveries` | 0.3 | `gate.operate` | - | served |
| community | `community.createGateDelivery` | `POST /v1/orgs/{org_id}/gate/deliveries` | 0.3 | `gate.operate` | optional | served |
| community | `community.collectGateDelivery` | `POST /v1/orgs/{org_id}/gate/deliveries/{delivery_id}:collect` | 0.3 | `gate.operate` | optional | served |
| community | `community.createGuardSession` | `POST /v1/orgs/{org_id}/gate/sessions` | 0.3 | `gate.operate` | optional | served |
| community | `gate.listGuards` | `GET /v1/orgs/{org_id}/gate/guards` | 0.3 | `gate.operate` | - | served |
| community | `community.setGuardPin` | `PUT /v1/orgs/{org_id}/persons/{person_id}/gate-pin` | 0.3 | `gate.manage` | optional | served |
| access | `access.listAccessPoints` | `GET /v1/orgs/{org_id}/access-points` | 0.3 | `access.read` | - | served |
| access | `access.createAccessPoint` | `POST /v1/orgs/{org_id}/access-points` | 0.3 | `access.manage` | optional | served |
| access | `access.getAccessPoint` | `GET /v1/orgs/{org_id}/access-points/{point_id}` | 0.3 | `access.read` | - | served |
| access | `access.updateAccessPoint` | `PATCH /v1/orgs/{org_id}/access-points/{point_id}` | 0.3 | `access.manage` | optional | served |
| access | `access.deleteAccessPoint` | `DELETE /v1/orgs/{org_id}/access-points/{point_id}` | 0.3 | `access.manage` | optional | served |
| access | `access.listAgents` | `GET /v1/orgs/{org_id}/agents` | 0.3 | `access.read` | - | served |
| access | `access.createAgent` | `POST /v1/orgs/{org_id}/agents` | 0.3 | `access.manage` | optional | served |
| access | `access.getAgent` | `GET /v1/orgs/{org_id}/agents/{agent_id}` | 0.3 | `access.read` | - | served |
| access | `access.updateAgent` | `PATCH /v1/orgs/{org_id}/agents/{agent_id}` | 0.3 | `access.manage` | optional | served |
| access | `access.reenrolAgent` | `POST /v1/orgs/{org_id}/agents/{agent_id}:reenrol` | 0.3 | `access.manage` | optional | served |
| access | `access.revokeAgent` | `POST /v1/orgs/{org_id}/agents/{agent_id}:revoke` | 0.3 | `access.manage` | optional | served |
| access | `access.rotateAgentToken` | `POST /v1/orgs/{org_id}/agents/{agent_id}:rotate-token` | 0.3 | `access.manage` | optional | served |
| access | `access.rebuildAllowlist` | `POST /v1/orgs/{org_id}/access/allowlist:rebuild` | 0.3 | `access.manage` | optional | served |
| access | `access.listCredentials` | `GET /v1/orgs/{org_id}/credentials` | 0.3 | `access.read` | - | served |
| access | `access.createCredential` | `POST /v1/orgs/{org_id}/credentials` | 0.3 | `access.manage` | required | served |
| access | `access.getCredential` | `GET /v1/orgs/{org_id}/credentials/{credential_id}` | 0.3 | `access.read` | - | served |
| access | `access.revokeCredential` | `DELETE /v1/orgs/{org_id}/credentials/{credential_id}` | 0.3 | `access.manage` | optional | served |
| access | `access.listAccessEvents` | `GET /v1/orgs/{org_id}/access/events` | 0.3 | `access.view_events` | - | served |
| access | `access.listGuardEvents` | `GET /v1/orgs/{org_id}/gate/events` | 0.3 | `gate.operate` | - | served |
| access | `access.openAccessPoint` | `POST /v1/orgs/{org_id}/access-points/{point_id}:open` | 0.3 | `access.open` | required | served |
| access | `access.getAgentCommand` | `GET /v1/orgs/{org_id}/access/commands/{command_id}` | 0.3 | `access.open`, `access.manage` | - | served |
| agent | `agent.enrol` | `POST /v1/agent/enrol` | 0.3 | agent token | optional | served |
| agent | `agent.poll` | `POST /v1/agent/poll` | 0.3 | agent token | optional | served |
| agent | `agent.rotateToken` | `POST /v1/agent/token:rotate` | 0.3 | agent token | optional | served |
| agent | `agent.connect` | `GET /v1/agent/ws` | 0.3 | agent token | - | served |
| dashboards | `dashboards.getSiteDashboard` | `GET /v1/orgs/{org_id}/sites/{site_id}/dashboard` | 0.1 | `dashboard.site` | - | served |
| dashboards | `dashboards.getStaffDashboard` | `GET /v1/orgs/{org_id}/sites/{site_id}/staff-dashboard` | 0.1 | `dashboard.staff` | - | served |
| dashboards | `dashboards.getPortfolioDashboard` | `GET /v1/orgs/{org_id}/dashboard/portfolio` | 0.1 | `dashboard.portfolio` | - | served |
| dashboards | `dashboards.getMyDashboard` | `GET /v1/orgs/{org_id}/me/dashboard` | 0.1 | `me.dashboard` | - | served |
| codes | `codes.listCodes` | `GET /v1/orgs/{org_id}/codes` | 0.1 | `codes.read` | - | served |
| codes | `codes.listReference` | `GET /v1/orgs/{org_id}/reference/{kind}` | 0.4 | `codes.read` | - | served (not in the original draft) |
| codes | `codes.listConsents` | `GET /v1/orgs/{org_id}/privacy/consents` | 0.4 | `codes.read` | - | served (not in the original draft) |
| integrations | `integrations.listApiKeys` | `GET /v1/orgs/{org_id}/api-keys` | 0.4 | `apikeys.read` | - | served |
| integrations | `integrations.createApiKey` | `POST /v1/orgs/{org_id}/api-keys` | 0.4 | `apikeys.manage` | none | served |
| integrations | `integrations.rotateApiKey` | `POST /v1/orgs/{org_id}/api-keys/{key_id}:rotate` | 0.4 | `apikeys.manage` | none | served |
| integrations | `integrations.revokeApiKey` | `POST /v1/orgs/{org_id}/api-keys/{key_id}:revoke` | 0.4 | `apikeys.manage` | optional | served |
| integrations | `integrations.listWebhookEndpoints` | `GET /v1/orgs/{org_id}/webhook-endpoints` | 0.4 | `webhooks.read` | - | served |
| integrations | `integrations.createWebhookEndpoint` | `POST /v1/orgs/{org_id}/webhook-endpoints` | 0.4 | `webhooks.manage` | none | served |
| integrations | `integrations.getWebhookEndpoint` | `GET /v1/orgs/{org_id}/webhook-endpoints/{endpoint_id}` | 0.4 | `webhooks.read` | - | served |
| integrations | `integrations.updateWebhookEndpoint` | `PATCH /v1/orgs/{org_id}/webhook-endpoints/{endpoint_id}` | 0.4 | `webhooks.manage` | optional | served |
| integrations | `integrations.deleteWebhookEndpoint` | `DELETE /v1/orgs/{org_id}/webhook-endpoints/{endpoint_id}` | 0.4 | `webhooks.manage` | optional | served |
| integrations | `integrations.testWebhookEndpoint` | `POST /v1/orgs/{org_id}/webhook-endpoints/{endpoint_id}:test` | 0.4 | `webhooks.manage` | none | served |
| integrations | `integrations.rotateWebhookEndpointSecret` | `POST /v1/orgs/{org_id}/webhook-endpoints/{endpoint_id}:rotate-secret` | 0.4 | `webhooks.manage` | none | served |
| integrations | `integrations.listWebhookDeliveries` | `GET /v1/orgs/{org_id}/webhook-endpoints/{endpoint_id}/deliveries` | 0.4 | `webhooks.read` | - | served |
| platform | `platform.listOrgs` | `GET /v1/platform/orgs` | 0.1 | `platform.read` | - | not yet served |
| platform | `platform.createOrg` | `POST /v1/platform/orgs` | 0.1 | `platform.manage` | optional | not yet served |
| platform | `platform.getOrg` | `GET /v1/platform/orgs/{org_id}` | 0.1 | `platform.read` | - | not yet served |
| platform | `platform.updateOrg` | `PATCH /v1/platform/orgs/{org_id}` | 0.1 | `platform.manage` | optional | not yet served |
| platform | `platform.getSettings` | `GET /v1/platform/settings` | 1.0 | `platform.read` | - | not yet served |
| platform | `platform.updateSettings` | `PATCH /v1/platform/settings` | 1.0 | `platform.manage` | optional | not yet served |
| platform | `platform.getHealth` | `GET /v1/platform/health` | 0.1 | `platform.read` | - | not yet served |
| platform | `platform.getBackupStatus` | `GET /v1/platform/backup-status` | 0.1 | `platform.read` | - | not yet served |
| platform | `platform.listJobs` | `GET /v1/platform/jobs` | 1.0 | `platform.read` | - | not yet served |
| me | `me.updateProfile` | `PATCH /v1/me/profile` | 0.1 | `sessions.manage_own` | optional | served |
| me | `me.startContactChange` | `POST /v1/me/contact-changes` | 0.1 | public / self | optional | not yet served |
| me | `me.confirmContactChange` | `POST /v1/me/contact-changes/{change_id}:confirm` | 0.1 | public / self | optional | not yet served |
| auth | `auth.requestPasswordReset` | `POST /v1/auth/password-reset` | 0.1 | public / self | optional | not yet served |
| auth | `auth.resetPassword` | `POST /v1/auth/password-reset/consume` | 0.1 | public / self | optional | not yet served |
| tenancy | `tenancy.setUnitTags` | `PUT /v1/orgs/{org_id}/units/{unit_id}/tags` | 0.1 | `units.manage` | optional | served (not in the original draft) |
| tenancy | `tenancy.downloadSiteTransferArchive` | `GET /v1/orgs/{org_id}/site-transfers/{transfer_id}/archive` | 0.1 | `sites.export` | - | served (not in the original draft) |
| people | `people.deleteVendor` | `DELETE /v1/orgs/{org_id}/vendors/{vendor_id}` | 0.1 | `vendors.manage` | optional | served (not in the original draft) |
| me | `me.listHouseholdMembers` | `GET /v1/orgs/{org_id}/me/household-members` | 0.1 | `invitations.household` | - | served (not in the original draft) |
| me | `me.registerDevice` | `POST /v1/me/devices` | 0.4 | `sessions.manage_own` | optional | served (not in the original draft) |
| me | `me.deleteDevice` | `DELETE /v1/me/devices/{id}` | 0.4 | `sessions.manage_own` | optional | served (not in the original draft) |
| me | `me.createWebLoginLink` | `POST /v1/me/web-login-link` | 0.4 | `sessions.manage_own` | none | served (not in the original draft) |
| auth | `auth.exchangeWebLoginLink` | `POST /v1/auth/web-login-link:exchange` | 0.4 | public / self | none | served (not in the original draft) |
| operations | `operations.getNotification` | `GET /v1/orgs/{org_id}/me/notifications/{notification_id}` | 0.1 | `notifications.read` | - | served (not in the original draft) |
| packs | `packs.list` | `GET /v1/orgs/{org_id}/packs` | 0.4 | `packs.read` | - | served (not in the original draft) |
| packs | `packs.enable` | `POST /v1/orgs/{org_id}/packs/{pack_id}/enable` | 0.4 | `packs.manage` | optional | served (not in the original draft) |
| packs | `packs.updateSettings` | `PUT /v1/orgs/{org_id}/packs/{pack_id}/settings` | 0.4 | `packs.manage` | optional | served (not in the original draft) |
| packs | `packs.disable` | `POST /v1/orgs/{org_id}/packs/{pack_id}/disable` | 0.4 | `packs.manage` | optional | served (not in the original draft) |
