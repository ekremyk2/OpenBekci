# Draft API index

The builders' checklist: every operation of the draft contract (`api/src/openapi-draft.yaml`, bundled to `api/openapi-draft.yaml`). Generated; regenerate by editing the path files, not this table.

Total: 419 new operations, plus the 15 already served (instance, auth, me) which stay in `api/src/openapi.yaml`. Release 0.1 operations are `x-stability: stable`; 0.2, 0.3 (and the 1.0 platform items) are `preview`. Where several permissions are listed, any one suffices.

| Module | Operations |
|---|---|
| tenancy | 48 |
| people | 51 |
| money | 134 |
| operations | 55 |
| audit | 4 |
| governance | 44 |
| community | 42 |
| access | 19 |
| agent | 4 |
| dashboards | 3 |
| codes | 1 |
| platform | 9 |
| me | 3 |
| auth | 2 |

| Module | operationId | Method and path | Release | Permission | Idempotency |
|---|---|---|---|---|---|
| tenancy | `tenancy.getOrg` | `GET /v1/orgs/{org_id}` | 0.1 | `org.read` | - |
| tenancy | `tenancy.updateOrg` | `PATCH /v1/orgs/{org_id}` | 0.1 | `org.manage` | optional |
| tenancy | `tenancy.listSites` | `GET /v1/orgs/{org_id}/sites` | 0.1 | `sites.read` | - |
| tenancy | `tenancy.createSite` | `POST /v1/orgs/{org_id}/sites` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.getSite` | `GET /v1/orgs/{org_id}/sites/{site_id}` | 0.1 | `sites.read` | - |
| tenancy | `tenancy.updateSite` | `PATCH /v1/orgs/{org_id}/sites/{site_id}` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.getSiteSettings` | `GET /v1/orgs/{org_id}/sites/{site_id}/settings` | 0.1 | `sites.read` | - |
| tenancy | `tenancy.updateSiteSettings` | `PATCH /v1/orgs/{org_id}/sites/{site_id}/settings` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.getBankDetails` | `GET /v1/orgs/{org_id}/sites/{site_id}/bank-details` | 0.1 | `sites.bank_details` | - |
| tenancy | `tenancy.setBankDetails` | `PUT /v1/orgs/{org_id}/sites/{site_id}/bank-details` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.listBlocks` | `GET /v1/orgs/{org_id}/blocks` | 0.1 | `sites.read` | - |
| tenancy | `tenancy.createBlock` | `POST /v1/orgs/{org_id}/blocks` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.getBlock` | `GET /v1/orgs/{org_id}/blocks/{block_id}` | 0.1 | `sites.read` | - |
| tenancy | `tenancy.updateBlock` | `PATCH /v1/orgs/{org_id}/blocks/{block_id}` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.deleteBlock` | `DELETE /v1/orgs/{org_id}/blocks/{block_id}` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.listUnits` | `GET /v1/orgs/{org_id}/units` | 0.1 | `units.read` | - |
| tenancy | `tenancy.createUnit` | `POST /v1/orgs/{org_id}/units` | 0.1 | `units.manage` | optional |
| tenancy | `tenancy.getUnit` | `GET /v1/orgs/{org_id}/units/{unit_id}` | 0.1 | `units.read` | - |
| tenancy | `tenancy.updateUnit` | `PATCH /v1/orgs/{org_id}/units/{unit_id}` | 0.1 | `units.manage` | optional |
| tenancy | `tenancy.deleteUnit` | `DELETE /v1/orgs/{org_id}/units/{unit_id}` | 0.1 | `units.manage` | optional |
| tenancy | `tenancy.importUnits` | `POST /v1/orgs/{org_id}/units:import` | 0.1 | `units.import` | optional |
| tenancy | `tenancy.importPeople` | `POST /v1/orgs/{org_id}/persons:import` | 0.1 | `units.import` | optional |
| tenancy | `tenancy.listAllocationKeys` | `GET /v1/orgs/{org_id}/allocation-keys` | 0.1 | `units.read` | - |
| tenancy | `tenancy.createAllocationKey` | `POST /v1/orgs/{org_id}/allocation-keys` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.getAllocationKey` | `GET /v1/orgs/{org_id}/allocation-keys/{key_id}` | 0.1 | `units.read` | - |
| tenancy | `tenancy.updateAllocationKey` | `PATCH /v1/orgs/{org_id}/allocation-keys/{key_id}` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.deleteAllocationKey` | `DELETE /v1/orgs/{org_id}/allocation-keys/{key_id}` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.listAllocationKeyValues` | `GET /v1/orgs/{org_id}/allocation-keys/{key_id}/values` | 0.1 | `units.read` | - |
| tenancy | `tenancy.putAllocationKeyValues` | `PUT /v1/orgs/{org_id}/allocation-keys/{key_id}/values` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.importAllocationKeyValues` | `POST /v1/orgs/{org_id}/allocation-keys/{key_id}/values:import` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.listUnitTags` | `GET /v1/orgs/{org_id}/unit-tags` | 0.1 | `units.read` | - |
| tenancy | `tenancy.createUnitTag` | `POST /v1/orgs/{org_id}/unit-tags` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.getUnitTag` | `GET /v1/orgs/{org_id}/unit-tags/{tag_id}` | 0.1 | `units.read` | - |
| tenancy | `tenancy.updateUnitTag` | `PATCH /v1/orgs/{org_id}/unit-tags/{tag_id}` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.deleteUnitTag` | `DELETE /v1/orgs/{org_id}/unit-tags/{tag_id}` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.getUnitTagMembers` | `GET /v1/orgs/{org_id}/unit-tags/{tag_id}/units` | 0.1 | `units.read` | - |
| tenancy | `tenancy.setUnitTagMembers` | `PUT /v1/orgs/{org_id}/unit-tags/{tag_id}/units` | 0.1 | `sites.manage` | optional |
| tenancy | `tenancy.exportSite` | `POST /v1/orgs/{org_id}/sites/{site_id}:export` | 0.1 | `sites.export` | optional |
| tenancy | `tenancy.importSite` | `POST /v1/orgs/{org_id}/sites:import` | 0.1 | `sites.export` | optional |
| tenancy | `tenancy.offboardSite` | `POST /v1/orgs/{org_id}/sites/{site_id}:offboard` | 0.1 | `sites.offboard` | optional |
| tenancy | `tenancy.listSiteTransfers` | `GET /v1/orgs/{org_id}/site-transfers` | 0.1 | `sites.export` | - |
| tenancy | `tenancy.getSiteTransfer` | `GET /v1/orgs/{org_id}/site-transfers/{transfer_id}` | 0.1 | `sites.export` | - |
| tenancy | `tenancy.listVehicles` | `GET /v1/orgs/{org_id}/vehicles` | 0.1 | `vehicles.read` | - |
| tenancy | `tenancy.createVehicle` | `POST /v1/orgs/{org_id}/vehicles` | 0.1 | `vehicles.manage` | optional |
| tenancy | `tenancy.getVehicle` | `GET /v1/orgs/{org_id}/vehicles/{vehicle_id}` | 0.1 | `vehicles.read` | - |
| tenancy | `tenancy.updateVehicle` | `PATCH /v1/orgs/{org_id}/vehicles/{vehicle_id}` | 0.1 | `vehicles.manage` | optional |
| tenancy | `tenancy.deleteVehicle` | `DELETE /v1/orgs/{org_id}/vehicles/{vehicle_id}` | 0.1 | `vehicles.manage` | optional |
| tenancy | `tenancy.exportVehicles` | `GET /v1/orgs/{org_id}/vehicles:export` | 0.1 | `vehicles.read` | - |
| people | `people.listPersons` | `GET /v1/orgs/{org_id}/persons` | 0.1 | `people.read` | - |
| people | `people.createPerson` | `POST /v1/orgs/{org_id}/persons` | 0.1 | `people.manage` | optional |
| people | `people.getPerson` | `GET /v1/orgs/{org_id}/persons/{person_id}` | 0.1 | `people.read` | - |
| people | `people.updatePerson` | `PATCH /v1/orgs/{org_id}/persons/{person_id}` | 0.1 | `people.manage` | optional |
| people | `people.getNationalId` | `GET /v1/orgs/{org_id}/persons/{person_id}/national-id` | 0.1 | `people.national_id` | - |
| people | `people.setNationalId` | `PUT /v1/orgs/{org_id}/persons/{person_id}/national-id` | 0.1 | `people.national_id` | optional |
| people | `people.clearNationalId` | `DELETE /v1/orgs/{org_id}/persons/{person_id}/national-id` | 0.1 | `people.national_id` | optional |
| people | `people.listOccupancies` | `GET /v1/orgs/{org_id}/occupancies` | 0.1 | `occupancies.read` | - |
| people | `people.createOccupancy` | `POST /v1/orgs/{org_id}/occupancies` | 0.1 | `occupancies.manage` | optional |
| people | `people.getOccupancy` | `GET /v1/orgs/{org_id}/occupancies/{occupancy_id}` | 0.1 | `occupancies.read` | - |
| people | `people.updateOccupancy` | `PATCH /v1/orgs/{org_id}/occupancies/{occupancy_id}` | 0.1 | `occupancies.manage` | optional |
| people | `people.deleteOccupancy` | `DELETE /v1/orgs/{org_id}/occupancies/{occupancy_id}` | 0.1 | `occupancies.manage` | optional |
| people | `people.setMoneyAccess` | `PUT /v1/orgs/{org_id}/occupancies/{occupancy_id}/money-access` | 0.1 | `occupancies.grant_money` | optional |
| people | `people.previewMoveOut` | `POST /v1/orgs/{org_id}/occupancies/{occupancy_id}/move-out:preview` | 0.1 | `people.move_out` | optional |
| people | `people.moveOut` | `POST /v1/orgs/{org_id}/occupancies/{occupancy_id}/move-out` | 0.1 | `people.move_out` | required |
| people | `people.listMoveOuts` | `GET /v1/orgs/{org_id}/move-outs` | 0.1 | `people.move_out` | - |
| people | `people.getMoveOut` | `GET /v1/orgs/{org_id}/move-outs/{move_out_id}` | 0.1 | `people.move_out` | - |
| people | `people.listMemberships` | `GET /v1/orgs/{org_id}/memberships` | 0.1 | `memberships.read` | - |
| people | `people.createMembership` | `POST /v1/orgs/{org_id}/memberships` | 0.1 | `memberships.manage` | optional |
| people | `people.getMembership` | `GET /v1/orgs/{org_id}/memberships/{membership_id}` | 0.1 | `memberships.read` | - |
| people | `people.updateMembership` | `PATCH /v1/orgs/{org_id}/memberships/{membership_id}` | 0.1 | `memberships.manage` | optional |
| people | `people.deleteMembership` | `DELETE /v1/orgs/{org_id}/memberships/{membership_id}` | 0.1 | `memberships.manage` | optional |
| people | `people.listInvitations` | `GET /v1/orgs/{org_id}/invitations` | 0.1 | `invitations.manage` | - |
| people | `people.createInvitation` | `POST /v1/orgs/{org_id}/invitations` | 0.1 | `invitations.manage` | optional |
| people | `people.getInvitation` | `GET /v1/orgs/{org_id}/invitations/{invitation_id}` | 0.1 | `invitations.manage` | - |
| people | `people.resendInvitation` | `POST /v1/orgs/{org_id}/invitations/{invitation_id}:resend` | 0.1 | `invitations.manage` | optional |
| people | `people.revokeInvitation` | `DELETE /v1/orgs/{org_id}/invitations/{invitation_id}` | 0.1 | `invitations.manage` | optional |
| people | `people.createHouseholdInvitation` | `POST /v1/orgs/{org_id}/units/{unit_id}/household-invitations` | 0.1 | `invitations.household` | optional |
| people | `people.previewInvitation` | `GET /v1/invitations/{token}` | 0.1 | public / self | - |
| people | `people.acceptInvitation` | `POST /v1/invitations/{token}:accept` | 0.1 | public / self | optional |
| people | `people.getJoinCode` | `GET /v1/orgs/{org_id}/sites/{site_id}/join-code` | 0.1 | `claims.decide` | - |
| people | `people.setJoinCodeEnabled` | `PUT /v1/orgs/{org_id}/sites/{site_id}/join-code` | 0.1 | `claims.decide` | optional |
| people | `people.rotateJoinCode` | `POST /v1/orgs/{org_id}/sites/{site_id}/join-code:rotate` | 0.1 | `claims.decide` | optional |
| people | `people.getJoinInfo` | `GET /v1/join/{code}` | 0.1 | public / self | - |
| people | `people.listJoinUnits` | `GET /v1/join/{code}/units` | 0.1 | public / self | - |
| people | `people.createUnitClaim` | `POST /v1/join/{code}/claims` | 0.1 | public / self | optional |
| people | `people.listUnitClaims` | `GET /v1/orgs/{org_id}/unit-claims` | 0.1 | `claims.decide` | - |
| people | `people.getUnitClaim` | `GET /v1/orgs/{org_id}/unit-claims/{claim_id}` | 0.1 | `claims.decide` | - |
| people | `people.approveUnitClaim` | `POST /v1/orgs/{org_id}/unit-claims/{claim_id}:approve` | 0.1 | `claims.decide` | optional |
| people | `people.rejectUnitClaim` | `POST /v1/orgs/{org_id}/unit-claims/{claim_id}:reject` | 0.1 | `claims.decide` | optional |
| people | `people.listMyUnitClaims` | `GET /v1/me/unit-claims` | 0.1 | public / self | - |
| people | `people.listDirectory` | `GET /v1/orgs/{org_id}/directory` | 0.1 | `directory.read` | - |
| people | `people.listVendors` | `GET /v1/orgs/{org_id}/vendors` | 0.1 | `vendors.read` | - |
| people | `people.createVendor` | `POST /v1/orgs/{org_id}/vendors` | 0.1 | `vendors.manage` | optional |
| people | `people.getVendor` | `GET /v1/orgs/{org_id}/vendors/{vendor_id}` | 0.1 | `vendors.read` | - |
| people | `people.updateVendor` | `PATCH /v1/orgs/{org_id}/vendors/{vendor_id}` | 0.1 | `vendors.manage` | optional |
| people | `people.listRoles` | `GET /v1/orgs/{org_id}/roles` | 0.1 | `roles.read` | - |
| people | `people.getMyPerson` | `GET /v1/orgs/{org_id}/me/person` | 0.1 | `me.manage` | - |
| people | `people.updateMyPerson` | `PATCH /v1/orgs/{org_id}/me/person` | 0.1 | `me.manage` | optional |
| people | `people.setMyDirectoryConsent` | `PUT /v1/orgs/{org_id}/me/directory-consent` | 0.1 | `me.manage` | optional |
| people | `people.listMyOccupancies` | `GET /v1/orgs/{org_id}/me/occupancies` | 0.1 | `me.manage` | - |
| money | `money.listAccounts` | `GET /v1/orgs/{org_id}/accounts` | 0.1 | `money.read` | - |
| money | `money.createAccount` | `POST /v1/orgs/{org_id}/accounts` | 0.1 | `money.manage` | optional |
| money | `money.getAccount` | `GET /v1/orgs/{org_id}/accounts/{account_id}` | 0.1 | `money.read` | - |
| money | `money.updateAccount` | `PATCH /v1/orgs/{org_id}/accounts/{account_id}` | 0.1 | `money.manage` | optional |
| money | `money.listFunds` | `GET /v1/orgs/{org_id}/funds` | 0.1 | `money.read` | - |
| money | `money.createFund` | `POST /v1/orgs/{org_id}/funds` | 0.1 | `money.manage` | optional |
| money | `money.getFund` | `GET /v1/orgs/{org_id}/funds/{fund_id}` | 0.1 | `money.read` | - |
| money | `money.updateFund` | `PATCH /v1/orgs/{org_id}/funds/{fund_id}` | 0.1 | `money.manage` | optional |
| money | `money.listCashBoxes` | `GET /v1/orgs/{org_id}/cash-boxes` | 0.1 | `money.read` | - |
| money | `money.createCashBox` | `POST /v1/orgs/{org_id}/cash-boxes` | 0.1 | `money.manage` | optional |
| money | `money.getCashBox` | `GET /v1/orgs/{org_id}/cash-boxes/{cash_box_id}` | 0.1 | `money.read` | - |
| money | `money.updateCashBox` | `PATCH /v1/orgs/{org_id}/cash-boxes/{cash_box_id}` | 0.1 | `money.manage` | optional |
| money | `money.listFiscalYears` | `GET /v1/orgs/{org_id}/fiscal-years` | 0.1 | `money.read` | - |
| money | `money.createFiscalYear` | `POST /v1/orgs/{org_id}/fiscal-years` | 0.1 | `money.manage` | optional |
| money | `money.getFiscalYear` | `GET /v1/orgs/{org_id}/fiscal-years/{fiscal_year_id}` | 0.1 | `money.read` | - |
| money | `money.closeFiscalYear` | `POST /v1/orgs/{org_id}/fiscal-years/{fiscal_year_id}:close` | 0.1 | `money.lock` | optional |
| money | `money.approveFiscalYear` | `POST /v1/orgs/{org_id}/fiscal-years/{fiscal_year_id}:approve` | 0.1 | `money.lock` | optional |
| money | `money.getLockDate` | `GET /v1/orgs/{org_id}/sites/{site_id}/lock-date` | 0.1 | `money.read` | - |
| money | `money.setLockDate` | `PUT /v1/orgs/{org_id}/sites/{site_id}/lock-date` | 0.1 | `money.lock` | optional |
| money | `money.listJournalEntries` | `GET /v1/orgs/{org_id}/journal-entries` | 0.1 | `money.read` | - |
| money | `money.getJournalEntry` | `GET /v1/orgs/{org_id}/journal-entries/{entry_id}` | 0.1 | `money.read` | - |
| money | `money.reverseJournalEntry` | `POST /v1/orgs/{org_id}/journal-entries/{entry_id}:reverse` | 0.1 | `money.post` | required |
| money | `money.listOpeningBalances` | `GET /v1/orgs/{org_id}/opening-balances` | 0.1 | `money.read` | - |
| money | `money.createOpeningBalance` | `POST /v1/orgs/{org_id}/opening-balances` | 0.1 | `money.post` | required |
| money | `money.getOpeningBalance` | `GET /v1/orgs/{org_id}/opening-balances/{opening_balance_id}` | 0.1 | `money.read` | - |
| money | `money.deleteOpeningBalance` | `DELETE /v1/orgs/{org_id}/opening-balances/{opening_balance_id}` | 0.1 | `money.post` | optional |
| money | `money.importOpeningBalances` | `POST /v1/orgs/{org_id}/opening-balances:import` | 0.1 | `money.post` | required |
| money | `money.listBudgets` | `GET /v1/orgs/{org_id}/budgets` | 0.1 | `money.read` | - |
| money | `money.createBudget` | `POST /v1/orgs/{org_id}/budgets` | 0.1 | `money.manage` | optional |
| money | `money.getBudget` | `GET /v1/orgs/{org_id}/budgets/{budget_id}` | 0.1 | `money.read` | - |
| money | `money.updateBudget` | `PATCH /v1/orgs/{org_id}/budgets/{budget_id}` | 0.1 | `money.manage` | optional |
| money | `money.deleteBudget` | `DELETE /v1/orgs/{org_id}/budgets/{budget_id}` | 0.1 | `money.manage` | optional |
| money | `money.getBudgetLines` | `GET /v1/orgs/{org_id}/budgets/{budget_id}/lines` | 0.1 | `money.read` | - |
| money | `money.putBudgetLines` | `PUT /v1/orgs/{org_id}/budgets/{budget_id}/lines` | 0.1 | `money.manage` | optional |
| money | `money.finalizeBudget` | `POST /v1/orgs/{org_id}/budgets/{budget_id}:finalize` | 0.1 | `money.manage` | optional |
| money | `money.listChargePlans` | `GET /v1/orgs/{org_id}/charge-plans` | 0.1 | `money.read` | - |
| money | `money.createChargePlan` | `POST /v1/orgs/{org_id}/charge-plans` | 0.1 | `money.manage` | optional |
| money | `money.getChargePlan` | `GET /v1/orgs/{org_id}/charge-plans/{plan_id}` | 0.1 | `money.read` | - |
| money | `money.updateChargePlan` | `PATCH /v1/orgs/{org_id}/charge-plans/{plan_id}` | 0.1 | `money.manage` | optional |
| money | `money.listChargePlanRates` | `GET /v1/orgs/{org_id}/charge-plans/{plan_id}/rates` | 0.1 | `money.read` | - |
| money | `money.createChargePlanRate` | `POST /v1/orgs/{org_id}/charge-plans/{plan_id}/rates` | 0.1 | `money.manage` | optional |
| money | `money.deleteChargePlanRate` | `DELETE /v1/orgs/{org_id}/charge-plans/{plan_id}/rates/{rate_id}` | 0.1 | `money.manage` | optional |
| money | `money.previewChargePlan` | `POST /v1/orgs/{org_id}/charge-plans:preview` | 0.1 | `money.manage` | optional |
| money | `money.getChargeGenerationStatus` | `GET /v1/orgs/{org_id}/charge-generation` | 0.1 | `money.read` | - |
| money | `money.previewExpenseAllocation` | `POST /v1/orgs/{org_id}/expense-allocations:preview` | 0.1 | `money.manage` | optional |
| money | `money.createExpenseAllocation` | `POST /v1/orgs/{org_id}/expense-allocations` | 0.1 | `money.post` | required |
| money | `money.listCharges` | `GET /v1/orgs/{org_id}/charges` | 0.1 | `money.read` | - |
| money | `money.createCharge` | `POST /v1/orgs/{org_id}/charges` | 0.1 | `money.post` | required |
| money | `money.getCharge` | `GET /v1/orgs/{org_id}/charges/{charge_id}` | 0.1 | `money.read`, `statements.read_own` | - |
| money | `money.cancelCharge` | `POST /v1/orgs/{org_id}/charges/{charge_id}:cancel` | 0.1 | `money.post` | required |
| money | `money.adjustCharge` | `POST /v1/orgs/{org_id}/charges/{charge_id}:adjust` | 0.1 | `money.post` | required |
| money | `money.getLateFeePolicy` | `GET /v1/orgs/{org_id}/late-fee-policy` | 0.1 | `money.read` | - |
| money | `money.setLateFeePolicy` | `PUT /v1/orgs/{org_id}/late-fee-policy` | 0.1 | `money.manage` | optional |
| money | `money.listLateFeeAccruals` | `GET /v1/orgs/{org_id}/late-fee-accruals` | 0.1 | `money.read` | - |
| money | `money.listLateFeeBookings` | `GET /v1/orgs/{org_id}/late-fee-bookings` | 0.1 | `money.read` | - |
| money | `money.listLateFeeWaivers` | `GET /v1/orgs/{org_id}/late-fee-waivers` | 0.1 | `money.read` | - |
| money | `money.createLateFeeWaiver` | `POST /v1/orgs/{org_id}/late-fee-waivers` | 0.1 | `money.post` | required |
| money | `money.listLateFeeFreezes` | `GET /v1/orgs/{org_id}/late-fee-freezes` | 0.1 | `money.read` | - |
| money | `money.createLateFeeFreeze` | `POST /v1/orgs/{org_id}/late-fee-freezes` | 0.1 | `money.manage` | optional |
| money | `money.getLateFeeFreeze` | `GET /v1/orgs/{org_id}/late-fee-freezes/{freeze_id}` | 0.1 | `money.read` | - |
| money | `money.updateLateFeeFreeze` | `PATCH /v1/orgs/{org_id}/late-fee-freezes/{freeze_id}` | 0.1 | `money.manage` | optional |
| money | `money.listPayments` | `GET /v1/orgs/{org_id}/payments` | 0.1 | `money.read` | - |
| money | `money.createPayment` | `POST /v1/orgs/{org_id}/payments` | 0.1 | `payments.record` | required |
| money | `money.getPayment` | `GET /v1/orgs/{org_id}/payments/{payment_id}` | 0.1 | `money.read`, `statements.read_own` | - |
| money | `money.voidPayment` | `POST /v1/orgs/{org_id}/payments/{payment_id}:void` | 0.1 | `payments.record` | required |
| money | `money.getPaymentReceipt` | `GET /v1/orgs/{org_id}/payments/{payment_id}/receipt` | 0.1 | `payments.record`, `statements.read_own` | - |
| money | `money.sendPaymentReceipt` | `POST /v1/orgs/{org_id}/payments/{payment_id}:send-receipt` | 0.1 | `payments.record` | optional |
| money | `money.setPaymentAllocations` | `PUT /v1/orgs/{org_id}/payments/{payment_id}/allocations` | 0.1 | `payments.record` | required |
| money | `money.previewPaymentAllocation` | `POST /v1/orgs/{org_id}/payments:preview-allocation` | 0.1 | `payments.record` | optional |
| money | `money.declarePayment` | `POST /v1/orgs/{org_id}/payments:declare` | 0.1 | `payments.declare` | required |
| money | `money.confirmPayment` | `POST /v1/orgs/{org_id}/payments/{payment_id}:confirm` | 0.1 | `payments.record` | required |
| money | `money.rejectPayment` | `POST /v1/orgs/{org_id}/payments/{payment_id}:reject` | 0.1 | `payments.record` | optional |
| money | `money.listCredits` | `GET /v1/orgs/{org_id}/credits` | 0.1 | `money.read`, `statements.read_own` | - |
| money | `money.applyCredit` | `POST /v1/orgs/{org_id}/credits/{credit_id}:apply` | 0.1 | `payments.record` | required |
| money | `money.listTransfers` | `GET /v1/orgs/{org_id}/transfers` | 0.1 | `money.read` | - |
| money | `money.createTransfer` | `POST /v1/orgs/{org_id}/transfers` | 0.1 | `money.post` | required |
| money | `money.getTransfer` | `GET /v1/orgs/{org_id}/transfers/{transfer_id}` | 0.1 | `money.read` | - |
| money | `money.listRefunds` | `GET /v1/orgs/{org_id}/refunds` | 0.1 | `money.read` | - |
| money | `money.createRefund` | `POST /v1/orgs/{org_id}/refunds` | 0.1 | `money.post` | required |
| money | `money.getRefund` | `GET /v1/orgs/{org_id}/refunds/{refund_id}` | 0.1 | `money.read` | - |
| money | `money.listBankImporters` | `GET /v1/orgs/{org_id}/bank-importers` | 0.1 | `money.read` | - |
| money | `money.listBankStatementImports` | `GET /v1/orgs/{org_id}/bank-statement-imports` | 0.1 | `money.read` | - |
| money | `money.createBankStatementImport` | `POST /v1/orgs/{org_id}/bank-statement-imports` | 0.1 | `money.post` | optional |
| money | `money.getBankStatementImport` | `GET /v1/orgs/{org_id}/bank-statement-imports/{import_id}` | 0.1 | `money.read` | - |
| money | `money.deleteBankStatementImport` | `DELETE /v1/orgs/{org_id}/bank-statement-imports/{import_id}` | 0.1 | `money.post` | optional |
| money | `money.getImportReconciliation` | `GET /v1/orgs/{org_id}/bank-statement-imports/{import_id}/reconciliation` | 0.1 | `money.read` | - |
| money | `money.listBankTransactions` | `GET /v1/orgs/{org_id}/bank-transactions` | 0.1 | `money.read` | - |
| money | `money.getBankTransaction` | `GET /v1/orgs/{org_id}/bank-transactions/{transaction_id}` | 0.1 | `money.read` | - |
| money | `money.matchBankTransaction` | `POST /v1/orgs/{org_id}/bank-transactions/{transaction_id}:match` | 0.1 | `payments.record` | required |
| money | `money.unmatchBankTransaction` | `POST /v1/orgs/{org_id}/bank-transactions/{transaction_id}:unmatch` | 0.1 | `payments.record` | required |
| money | `money.ignoreBankTransaction` | `POST /v1/orgs/{org_id}/bank-transactions/{transaction_id}:ignore` | 0.1 | `payments.record` | optional |
| money | `money.listExpenses` | `GET /v1/orgs/{org_id}/expenses` | 0.1 | `money.read` | - |
| money | `money.createExpense` | `POST /v1/orgs/{org_id}/expenses` | 0.1 | `money.post` | required |
| money | `money.getExpense` | `GET /v1/orgs/{org_id}/expenses/{expense_id}` | 0.1 | `money.read` | - |
| money | `money.voidExpense` | `POST /v1/orgs/{org_id}/expenses/{expense_id}:void` | 0.1 | `money.post` | required |
| money | `money.listRecurringExpenses` | `GET /v1/orgs/{org_id}/recurring-expenses` | 0.1 | `money.read` | - |
| money | `money.createRecurringExpense` | `POST /v1/orgs/{org_id}/recurring-expenses` | 0.1 | `money.post` | required |
| money | `money.getRecurringExpense` | `GET /v1/orgs/{org_id}/recurring-expenses/{recurring_expense_id}` | 0.1 | `money.read` | - |
| money | `money.updateRecurringExpense` | `PATCH /v1/orgs/{org_id}/recurring-expenses/{recurring_expense_id}` | 0.1 | `money.post` | optional |
| money | `money.deleteRecurringExpense` | `DELETE /v1/orgs/{org_id}/recurring-expenses/{recurring_expense_id}` | 0.1 | `money.post` | optional |
| money | `money.listBills` | `GET /v1/orgs/{org_id}/bills` | 0.1 | `money.read` | - |
| money | `money.createBill` | `POST /v1/orgs/{org_id}/bills` | 0.1 | `money.post` | required |
| money | `money.getBill` | `GET /v1/orgs/{org_id}/bills/{bill_id}` | 0.1 | `money.read` | - |
| money | `money.updateBill` | `PATCH /v1/orgs/{org_id}/bills/{bill_id}` | 0.1 | `money.post` | optional |
| money | `money.cancelBill` | `POST /v1/orgs/{org_id}/bills/{bill_id}:cancel` | 0.1 | `money.post` | required |
| money | `money.listBillPayments` | `GET /v1/orgs/{org_id}/bills/{bill_id}/payments` | 0.1 | `money.read` | - |
| money | `money.createBillPayment` | `POST /v1/orgs/{org_id}/bills/{bill_id}/payments` | 0.1 | `money.post` | required |
| money | `money.listDunningStages` | `GET /v1/orgs/{org_id}/dunning-stages` | 0.1 | `money.read` | - |
| money | `money.createDunningStage` | `POST /v1/orgs/{org_id}/dunning-stages` | 0.1 | `collections.manage` | optional |
| money | `money.getDunningStage` | `GET /v1/orgs/{org_id}/dunning-stages/{stage_id}` | 0.1 | `money.read` | - |
| money | `money.updateDunningStage` | `PATCH /v1/orgs/{org_id}/dunning-stages/{stage_id}` | 0.1 | `collections.manage` | optional |
| money | `money.deleteDunningStage` | `DELETE /v1/orgs/{org_id}/dunning-stages/{stage_id}` | 0.1 | `collections.manage` | optional |
| money | `money.previewDunningRun` | `POST /v1/orgs/{org_id}/dunning-runs:preview` | 0.1 | `collections.manage` | optional |
| money | `money.createDunningRun` | `POST /v1/orgs/{org_id}/dunning-runs` | 0.1 | `collections.manage` | optional |
| money | `money.listDunningEvents` | `GET /v1/orgs/{org_id}/dunning-events` | 0.1 | `money.read` | - |
| money | `money.listCollectionCases` | `GET /v1/orgs/{org_id}/collection-cases` | 0.1 | `money.read` | - |
| money | `money.createCollectionCase` | `POST /v1/orgs/{org_id}/collection-cases` | 0.1 | `collections.manage` | optional |
| money | `money.getCollectionCase` | `GET /v1/orgs/{org_id}/collection-cases/{case_id}` | 0.1 | `money.read` | - |
| money | `money.updateCollectionCase` | `PATCH /v1/orgs/{org_id}/collection-cases/{case_id}` | 0.1 | `collections.manage` | optional |
| money | `money.closeCollectionCase` | `POST /v1/orgs/{org_id}/collection-cases/{case_id}:close` | 0.1 | `collections.manage` | optional |
| money | `money.getDebtBreakdown` | `GET /v1/orgs/{org_id}/debt-breakdown` | 0.1 | `collections.manage` | - |
| money | `money.getUnitStatement` | `GET /v1/orgs/{org_id}/units/{unit_id}/statements/{debtor_kind}` | 0.1 | `statements.read`, `statements.read_own` | - |
| money | `money.getPersonStatement` | `GET /v1/orgs/{org_id}/persons/{person_id}/statement` | 0.1 | `statements.read`, `statements.read_own` | - |
| money | `money.sendStatement` | `POST /v1/orgs/{org_id}/statements:send` | 0.1 | `statements.read` | optional |
| money | `money.getMyBalances` | `GET /v1/orgs/{org_id}/me/balances` | 0.1 | `statements.read_own` | - |
| money | `money.getIncomeExpenseReport` | `GET /v1/orgs/{org_id}/reports/income-expense` | 0.1 | `reports.read`, `reports.aggregate` | - |
| money | `money.getDebtorReport` | `GET /v1/orgs/{org_id}/reports/debtors` | 0.1 | `reports.debtors` | - |
| money | `money.getBudgetVsActualReport` | `GET /v1/orgs/{org_id}/reports/budget-vs-actual` | 0.1 | `reports.read`, `reports.aggregate` | - |
| money | `money.getTrialBalanceReport` | `GET /v1/orgs/{org_id}/reports/trial-balance` | 0.1 | `reports.read` | - |
| money | `money.getReceivablesAgingReport` | `GET /v1/orgs/{org_id}/reports/receivables-aging` | 0.1 | `reports.read` | - |
| money | `money.getCollectorReconciliationReport` | `GET /v1/orgs/{org_id}/reports/collector-reconciliation` | 0.1 | `reports.read` | - |
| money | `money.getBankReconciliationReport` | `GET /v1/orgs/{org_id}/reports/bank-reconciliation` | 0.1 | `reports.read` | - |
| money | `money.getFundBalancesReport` | `GET /v1/orgs/{org_id}/reports/fund-balances` | 0.1 | `reports.read` | - |
| money | `money.getMoneyOverview` | `GET /v1/orgs/{org_id}/money/overview` | 0.1 | `money.read` | - |
| operations | `operations.getTicketWorkflow` | `GET /v1/orgs/{org_id}/ticket-workflow` | 0.1 | `tickets.read`, `tickets.open` | - |
| operations | `operations.updateTicketWorkflow` | `PUT /v1/orgs/{org_id}/ticket-workflow` | 0.1 | `tickets.manage` | optional |
| operations | `operations.listTicketCategories` | `GET /v1/orgs/{org_id}/ticket-categories` | 0.1 | `tickets.read`, `tickets.open` | - |
| operations | `operations.createTicketCategory` | `POST /v1/orgs/{org_id}/ticket-categories` | 0.1 | `tickets.manage` | optional |
| operations | `operations.getTicketCategory` | `GET /v1/orgs/{org_id}/ticket-categories/{category_id}` | 0.1 | `tickets.read`, `tickets.open` | - |
| operations | `operations.updateTicketCategory` | `PATCH /v1/orgs/{org_id}/ticket-categories/{category_id}` | 0.1 | `tickets.manage` | optional |
| operations | `operations.listTickets` | `GET /v1/orgs/{org_id}/tickets` | 0.1 | `tickets.read`, `tickets.open` | - |
| operations | `operations.createTicket` | `POST /v1/orgs/{org_id}/tickets` | 0.1 | `tickets.open` | optional |
| operations | `operations.getTicket` | `GET /v1/orgs/{org_id}/tickets/{ticket_id}` | 0.1 | `tickets.read`, `tickets.open` | - |
| operations | `operations.updateTicket` | `PATCH /v1/orgs/{org_id}/tickets/{ticket_id}` | 0.1 | `tickets.manage` | optional |
| operations | `operations.transitionTicket` | `POST /v1/orgs/{org_id}/tickets/{ticket_id}:transition` | 0.1 | `tickets.manage`, `tickets.work` | optional |
| operations | `operations.mergeTicket` | `POST /v1/orgs/{org_id}/tickets/{ticket_id}:merge` | 0.1 | `tickets.manage` | optional |
| operations | `operations.voteTicket` | `PUT /v1/orgs/{org_id}/tickets/{ticket_id}/vote` | 0.1 | `tickets.open` | optional |
| operations | `operations.unvoteTicket` | `DELETE /v1/orgs/{org_id}/tickets/{ticket_id}/vote` | 0.1 | `tickets.open` | optional |
| operations | `operations.listTicketComments` | `GET /v1/orgs/{org_id}/tickets/{ticket_id}/comments` | 0.1 | `tickets.read`, `tickets.open` | - |
| operations | `operations.createTicketComment` | `POST /v1/orgs/{org_id}/tickets/{ticket_id}/comments` | 0.1 | `tickets.read`, `tickets.open`, `tickets.work` | optional |
| operations | `operations.createTicketAttachment` | `POST /v1/orgs/{org_id}/tickets/{ticket_id}/attachments` | 0.1 | `tickets.open` | optional |
| operations | `operations.deleteTicketAttachment` | `DELETE /v1/orgs/{org_id}/tickets/{ticket_id}/attachments/{attachment_id}` | 0.1 | `tickets.open` | optional |
| operations | `operations.listTicketHistory` | `GET /v1/orgs/{org_id}/tickets/{ticket_id}/history` | 0.1 | `tickets.read`, `tickets.open` | - |
| operations | `operations.listAnnouncements` | `GET /v1/orgs/{org_id}/announcements` | 0.1 | `announcements.read`, `announcements.publish` | - |
| operations | `operations.createAnnouncement` | `POST /v1/orgs/{org_id}/announcements` | 0.1 | `announcements.publish` | optional |
| operations | `operations.getAnnouncement` | `GET /v1/orgs/{org_id}/announcements/{announcement_id}` | 0.1 | `announcements.read`, `announcements.publish` | - |
| operations | `operations.updateAnnouncement` | `PATCH /v1/orgs/{org_id}/announcements/{announcement_id}` | 0.1 | `announcements.publish` | optional |
| operations | `operations.deleteAnnouncement` | `DELETE /v1/orgs/{org_id}/announcements/{announcement_id}` | 0.1 | `announcements.publish` | optional |
| operations | `operations.previewAnnouncement` | `POST /v1/orgs/{org_id}/announcements:preview` | 0.1 | `announcements.publish` | optional |
| operations | `operations.publishAnnouncement` | `POST /v1/orgs/{org_id}/announcements/{announcement_id}:publish` | 0.1 | `announcements.publish` | optional |
| operations | `operations.archiveAnnouncement` | `POST /v1/orgs/{org_id}/announcements/{announcement_id}:archive` | 0.1 | `announcements.publish` | optional |
| operations | `operations.listAnnouncementReads` | `GET /v1/orgs/{org_id}/announcements/{announcement_id}/reads` | 0.1 | `announcements.publish` | - |
| operations | `operations.markAnnouncementRead` | `PUT /v1/orgs/{org_id}/announcements/{announcement_id}/read` | 0.1 | `announcements.read` | optional |
| operations | `operations.listFolders` | `GET /v1/orgs/{org_id}/folders` | 0.1 | `documents.read` | - |
| operations | `operations.createFolder` | `POST /v1/orgs/{org_id}/folders` | 0.1 | `documents.manage` | optional |
| operations | `operations.getFolder` | `GET /v1/orgs/{org_id}/folders/{folder_id}` | 0.1 | `documents.read` | - |
| operations | `operations.updateFolder` | `PATCH /v1/orgs/{org_id}/folders/{folder_id}` | 0.1 | `documents.manage` | optional |
| operations | `operations.deleteFolder` | `DELETE /v1/orgs/{org_id}/folders/{folder_id}` | 0.1 | `documents.manage` | optional |
| operations | `operations.listDocuments` | `GET /v1/orgs/{org_id}/documents` | 0.1 | `documents.read` | - |
| operations | `operations.createDocument` | `POST /v1/orgs/{org_id}/documents` | 0.1 | `documents.manage`, `documents.upload_own` | optional |
| operations | `operations.getDocument` | `GET /v1/orgs/{org_id}/documents/{document_id}` | 0.1 | `documents.read` | - |
| operations | `operations.updateDocument` | `PATCH /v1/orgs/{org_id}/documents/{document_id}` | 0.1 | `documents.manage`, `documents.upload_own` | optional |
| operations | `operations.deleteDocument` | `DELETE /v1/orgs/{org_id}/documents/{document_id}` | 0.1 | `documents.manage`, `documents.upload_own` | optional |
| operations | `operations.listNotifications` | `GET /v1/orgs/{org_id}/me/notifications` | 0.1 | `notifications.read` | - |
| operations | `operations.getUnreadCount` | `GET /v1/orgs/{org_id}/me/notifications/unread-count` | 0.1 | `notifications.read` | - |
| operations | `operations.markNotificationRead` | `POST /v1/orgs/{org_id}/me/notifications/{notification_id}:read` | 0.1 | `notifications.read` | optional |
| operations | `operations.markAllNotificationsRead` | `POST /v1/orgs/{org_id}/me/notifications:read-all` | 0.1 | `notifications.read` | optional |
| operations | `operations.getNotificationDefaults` | `GET /v1/orgs/{org_id}/notification-defaults` | 0.1 | `notifications.manage` | - |
| operations | `operations.setNotificationDefaults` | `PUT /v1/orgs/{org_id}/notification-defaults` | 0.1 | `notifications.manage` | optional |
| operations | `operations.getNotificationMutes` | `GET /v1/orgs/{org_id}/me/notification-mutes` | 0.1 | `notifications.read` | - |
| operations | `operations.setNotificationMutes` | `PUT /v1/orgs/{org_id}/me/notification-mutes` | 0.1 | `notifications.read` | optional |
| operations | `operations.getSmsUsage` | `GET /v1/orgs/{org_id}/sms-usage` | 0.1 | `notifications.manage` | - |
| operations | `operations.uploadFile` | `POST /v1/orgs/{org_id}/files` | 0.1 | `files.upload` | optional |
| operations | `operations.getFileMeta` | `GET /v1/orgs/{org_id}/files/{file_id}/meta` | 0.1 | `files.read` | - |
| operations | `operations.downloadFile` | `GET /v1/orgs/{org_id}/files/{file_id}` | 0.1 | `files.read` | - |
| operations | `operations.deleteFile` | `DELETE /v1/orgs/{org_id}/files/{file_id}` | 0.1 | `files.upload` | optional |
| operations | `operations.listJobs` | `GET /v1/orgs/{org_id}/jobs` | 0.1 | `jobs.read` | - |
| operations | `operations.getJob` | `GET /v1/orgs/{org_id}/jobs/{job_id}` | 0.1 | `jobs.read` | - |
| operations | `operations.createExport` | `POST /v1/orgs/{org_id}/exports` | 0.1 | `exports.create` | optional |
| audit | `audit.listEvents` | `GET /v1/orgs/{org_id}/audit-events` | 0.1 | `audit.read` | - |
| audit | `audit.getEvent` | `GET /v1/orgs/{org_id}/audit-events/{event_id}` | 0.1 | `audit.read` | - |
| audit | `audit.listNotes` | `GET /v1/orgs/{org_id}/audit-notes` | 0.1 | `audit.read` | - |
| audit | `audit.createNote` | `POST /v1/orgs/{org_id}/audit-notes` | 0.1 | `audit.report` | optional |
| governance | `governance.getGovernanceSettings` | `GET /v1/orgs/{org_id}/sites/{site_id}/governance-settings` | 0.2 | `meetings.read` | - |
| governance | `governance.setGovernanceSettings` | `PUT /v1/orgs/{org_id}/sites/{site_id}/governance-settings` | 0.2 | `meetings.manage` | optional |
| governance | `governance.listGovernancePresets` | `GET /v1/orgs/{org_id}/governance-presets` | 0.2 | `meetings.read` | - |
| governance | `governance.listMeetings` | `GET /v1/orgs/{org_id}/meetings` | 0.2 | `meetings.read` | - |
| governance | `governance.createMeeting` | `POST /v1/orgs/{org_id}/meetings` | 0.2 | `meetings.manage` | optional |
| governance | `governance.getMeeting` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}` | 0.2 | `meetings.read` | - |
| governance | `governance.updateMeeting` | `PATCH /v1/orgs/{org_id}/meetings/{meeting_id}` | 0.2 | `meetings.manage` | optional |
| governance | `governance.deleteMeeting` | `DELETE /v1/orgs/{org_id}/meetings/{meeting_id}` | 0.2 | `meetings.manage` | optional |
| governance | `governance.callMeeting` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}:call` | 0.2 | `meetings.manage` | optional |
| governance | `governance.startMeeting` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}:start` | 0.2 | `meetings.manage` | optional |
| governance | `governance.closeMeeting` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}:close` | 0.2 | `meetings.manage` | optional |
| governance | `governance.cancelMeeting` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}:cancel` | 0.2 | `meetings.manage` | optional |
| governance | `governance.getMeetingQuorum` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/quorum` | 0.2 | `meetings.manage`, `attendance.manage` | - |
| governance | `governance.getCallLetter` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/call-letter` | 0.2 | `meetings.read` | - |
| governance | `governance.getAttendanceSheet` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/attendance-sheet` | 0.2 | `attendance.manage` | - |
| governance | `governance.getMyMeeting` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/me` | 0.2 | `meetings.read` | - |
| governance | `governance.generateMinutes` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}/minutes:generate` | 0.2 | `meetings.manage` | optional |
| governance | `governance.getMinutes` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/minutes` | 0.2 | `meetings.read` | - |
| governance | `governance.listMeetingNotices` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/notices` | 0.2 | `meetings.manage` | - |
| governance | `governance.recordMeetingNotice` | `PUT /v1/orgs/{org_id}/meetings/{meeting_id}/notices/{person_id}` | 0.2 | `meetings.manage` | optional |
| governance | `governance.recordMeetingNoticesBulk` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}/notices:bulk` | 0.2 | `meetings.manage` | optional |
| governance | `governance.notifyMeetingDecisions` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}:notify-decisions` | 0.2 | `meetings.manage` | optional |
| governance | `governance.listAgendaItems` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/agenda-items` | 0.2 | `meetings.read` | - |
| governance | `governance.createAgendaItem` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}/agenda-items` | 0.2 | `meetings.manage` | optional |
| governance | `governance.updateAgendaItem` | `PATCH /v1/orgs/{org_id}/agenda-items/{agenda_item_id}` | 0.2 | `meetings.manage` | optional |
| governance | `governance.deleteAgendaItem` | `DELETE /v1/orgs/{org_id}/agenda-items/{agenda_item_id}` | 0.2 | `meetings.manage` | optional |
| governance | `governance.reorderAgendaItems` | `PUT /v1/orgs/{org_id}/meetings/{meeting_id}/agenda-items:order` | 0.2 | `meetings.manage` | optional |
| governance | `governance.getPoll` | `GET /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll` | 0.2 | `meetings.read` | - |
| governance | `governance.setPoll` | `PUT /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll` | 0.2 | `meetings.manage` | optional |
| governance | `governance.answerPoll` | `PUT /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll/answer` | 0.2 | `polls.answer` | optional |
| governance | `governance.getPollResults` | `GET /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll/results` | 0.2 | `meetings.manage`, `polls.answer` | - |
| governance | `governance.listPollAnswers` | `GET /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll/answers` | 0.2 | `meetings.manage` | - |
| governance | `governance.getVoteCounts` | `GET /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/vote-counts` | 0.2 | `meetings.read` | - |
| governance | `governance.recordVoteCounts` | `PUT /v1/orgs/{org_id}/agenda-items/{agenda_item_id}/vote-counts` | 0.2 | `decisions.manage` | optional |
| governance | `governance.listAttendance` | `GET /v1/orgs/{org_id}/meetings/{meeting_id}/attendance` | 0.2 | `attendance.manage` | - |
| governance | `governance.recordAttendance` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}/attendance` | 0.2 | `attendance.manage` | optional |
| governance | `governance.removeAttendance` | `DELETE /v1/orgs/{org_id}/meetings/{meeting_id}/attendance/{attendance_id}` | 0.2 | `attendance.manage` | optional |
| governance | `governance.checkInAttendance` | `POST /v1/orgs/{org_id}/meetings/{meeting_id}/attendance:checkin` | 0.2 | `attendance.manage` | optional |
| governance | `governance.listDecisions` | `GET /v1/orgs/{org_id}/decisions` | 0.2 | `meetings.read` | - |
| governance | `governance.createDecision` | `POST /v1/orgs/{org_id}/decisions` | 0.2 | `decisions.manage` | optional |
| governance | `governance.getDecision` | `GET /v1/orgs/{org_id}/decisions/{decision_id}` | 0.2 | `meetings.read` | - |
| governance | `governance.updateDecision` | `PATCH /v1/orgs/{org_id}/decisions/{decision_id}` | 0.2 | `decisions.manage` | optional |
| governance | `governance.applyDecision` | `POST /v1/orgs/{org_id}/decisions/{decision_id}:apply` | 0.2 | `decisions.manage` | optional |
| governance | `governance.listTermReminders` | `GET /v1/orgs/{org_id}/term-reminders` | 0.2 | `meetings.manage` | - |
| community | `community.listFacilities` | `GET /v1/orgs/{org_id}/facilities` | 0.3 | `bookings.read`, `facilities.manage` | - |
| community | `community.createFacility` | `POST /v1/orgs/{org_id}/facilities` | 0.3 | `facilities.manage` | optional |
| community | `community.getFacility` | `GET /v1/orgs/{org_id}/facilities/{facility_id}` | 0.3 | `bookings.read`, `facilities.manage` | - |
| community | `community.updateFacility` | `PATCH /v1/orgs/{org_id}/facilities/{facility_id}` | 0.3 | `facilities.manage` | optional |
| community | `community.deleteFacility` | `DELETE /v1/orgs/{org_id}/facilities/{facility_id}` | 0.3 | `facilities.manage` | optional |
| community | `community.listFacilityBlocks` | `GET /v1/orgs/{org_id}/facilities/{facility_id}/blocks` | 0.3 | `bookings.read`, `facilities.manage` | - |
| community | `community.createFacilityBlock` | `POST /v1/orgs/{org_id}/facilities/{facility_id}/blocks` | 0.3 | `facilities.manage` | optional |
| community | `community.deleteFacilityBlock` | `DELETE /v1/orgs/{org_id}/facilities/{facility_id}/blocks/{block_id}` | 0.3 | `facilities.manage` | optional |
| community | `community.getFacilityAvailability` | `GET /v1/orgs/{org_id}/facilities/{facility_id}/availability` | 0.3 | `bookings.create` | - |
| community | `community.listBookings` | `GET /v1/orgs/{org_id}/bookings` | 0.3 | `bookings.read` | - |
| community | `community.createBooking` | `POST /v1/orgs/{org_id}/bookings` | 0.3 | `bookings.create` | required |
| community | `community.getBooking` | `GET /v1/orgs/{org_id}/bookings/{booking_id}` | 0.3 | `bookings.read` | - |
| community | `community.cancelBooking` | `POST /v1/orgs/{org_id}/bookings/{booking_id}:cancel` | 0.3 | `bookings.manage`, `bookings.create` | required |
| community | `community.listVisitorEntries` | `GET /v1/orgs/{org_id}/visitor-entries` | 0.3 | `visitors.read`, `visitors.manage` | - |
| community | `community.createVisitorEntry` | `POST /v1/orgs/{org_id}/visitor-entries` | 0.3 | `visitors.manage` | optional |
| community | `community.getVisitorEntry` | `GET /v1/orgs/{org_id}/visitor-entries/{entry_id}` | 0.3 | `visitors.read`, `visitors.manage` | - |
| community | `community.updateVisitorEntry` | `PATCH /v1/orgs/{org_id}/visitor-entries/{entry_id}` | 0.3 | `visitors.manage` | optional |
| community | `community.cancelVisitorEntry` | `POST /v1/orgs/{org_id}/visitor-entries/{entry_id}:cancel` | 0.3 | `visitors.manage` | optional |
| community | `community.getVisitorEntryQr` | `GET /v1/orgs/{org_id}/visitor-entries/{entry_id}/qr` | 0.3 | `visitors.manage` | - |
| community | `community.listRecurringVisitors` | `GET /v1/orgs/{org_id}/recurring-visitors` | 0.3 | `visitors.read`, `visitors.manage` | - |
| community | `community.createRecurringVisitor` | `POST /v1/orgs/{org_id}/recurring-visitors` | 0.3 | `visitors.manage` | optional |
| community | `community.getRecurringVisitor` | `GET /v1/orgs/{org_id}/recurring-visitors/{recurring_visitor_id}` | 0.3 | `visitors.read`, `visitors.manage` | - |
| community | `community.updateRecurringVisitor` | `PATCH /v1/orgs/{org_id}/recurring-visitors/{recurring_visitor_id}` | 0.3 | `visitors.manage` | optional |
| community | `community.revokeRecurringVisitor` | `POST /v1/orgs/{org_id}/recurring-visitors/{recurring_visitor_id}:revoke` | 0.3 | `visitors.manage` | optional |
| community | `community.getRecurringVisitorQr` | `GET /v1/orgs/{org_id}/recurring-visitors/{recurring_visitor_id}/qr` | 0.3 | `visitors.manage` | - |
| community | `community.listVisitorApprovalRequests` | `GET /v1/orgs/{org_id}/visitor-approval-requests` | 0.3 | `visitors.decide` | - |
| community | `community.decideVisitorApprovalRequest` | `POST /v1/orgs/{org_id}/visitor-approval-requests/{request_id}:decide` | 0.3 | `visitors.decide` | optional |
| community | `community.listDeliveries` | `GET /v1/orgs/{org_id}/deliveries` | 0.3 | `deliveries.read`, `visitors.read` | - |
| community | `community.getGateConsole` | `GET /v1/orgs/{org_id}/gate/console` | 0.3 | `gate.operate` | - |
| community | `community.listGateVisitors` | `GET /v1/orgs/{org_id}/gate/visitors` | 0.3 | `gate.operate` | - |
| community | `community.getGateVisitor` | `GET /v1/orgs/{org_id}/gate/visitors/{entry_id}` | 0.3 | `gate.operate` | - |
| community | `community.scanGateVisitor` | `POST /v1/orgs/{org_id}/gate/visitors:scan` | 0.3 | `gate.operate` | optional |
| community | `community.createWalkInVisitor` | `POST /v1/orgs/{org_id}/gate/visitors:walk-in` | 0.3 | `gate.operate` | optional |
| community | `community.arriveGateVisitor` | `POST /v1/orgs/{org_id}/gate/visitors/{entry_id}:arrive` | 0.3 | `gate.operate` | optional |
| community | `community.leaveGateVisitor` | `POST /v1/orgs/{org_id}/gate/visitors/{entry_id}:leave` | 0.3 | `gate.operate` | optional |
| community | `community.getGateVisitorCallContact` | `GET /v1/orgs/{org_id}/gate/visitors/{entry_id}/call-contact` | 0.3 | `gate.operate` | - |
| community | `community.searchGatePlates` | `GET /v1/orgs/{org_id}/gate/plates` | 0.3 | `gate.operate` | - |
| community | `community.listGateDeliveries` | `GET /v1/orgs/{org_id}/gate/deliveries` | 0.3 | `gate.operate` | - |
| community | `community.createGateDelivery` | `POST /v1/orgs/{org_id}/gate/deliveries` | 0.3 | `gate.operate` | optional |
| community | `community.collectGateDelivery` | `POST /v1/orgs/{org_id}/gate/deliveries/{delivery_id}:collect` | 0.3 | `gate.operate` | optional |
| community | `community.createGuardSession` | `POST /v1/orgs/{org_id}/gate/sessions` | 0.3 | `gate.operate` | optional |
| community | `community.setGuardPin` | `PUT /v1/orgs/{org_id}/persons/{person_id}/gate-pin` | 0.3 | `gate.manage` | optional |
| access | `access.listAccessPoints` | `GET /v1/orgs/{org_id}/access-points` | 0.3 | `access.read` | - |
| access | `access.createAccessPoint` | `POST /v1/orgs/{org_id}/access-points` | 0.3 | `access.manage` | optional |
| access | `access.getAccessPoint` | `GET /v1/orgs/{org_id}/access-points/{point_id}` | 0.3 | `access.read` | - |
| access | `access.updateAccessPoint` | `PATCH /v1/orgs/{org_id}/access-points/{point_id}` | 0.3 | `access.manage` | optional |
| access | `access.deleteAccessPoint` | `DELETE /v1/orgs/{org_id}/access-points/{point_id}` | 0.3 | `access.manage` | optional |
| access | `access.listAgents` | `GET /v1/orgs/{org_id}/agents` | 0.3 | `access.read` | - |
| access | `access.createAgent` | `POST /v1/orgs/{org_id}/agents` | 0.3 | `access.manage` | optional |
| access | `access.getAgent` | `GET /v1/orgs/{org_id}/agents/{agent_id}` | 0.3 | `access.read` | - |
| access | `access.updateAgent` | `PATCH /v1/orgs/{org_id}/agents/{agent_id}` | 0.3 | `access.manage` | optional |
| access | `access.reenrolAgent` | `POST /v1/orgs/{org_id}/agents/{agent_id}:reenrol` | 0.3 | `access.manage` | optional |
| access | `access.revokeAgent` | `POST /v1/orgs/{org_id}/agents/{agent_id}:revoke` | 0.3 | `access.manage` | optional |
| access | `access.rotateAgentToken` | `POST /v1/orgs/{org_id}/agents/{agent_id}:rotate-token` | 0.3 | `access.manage` | optional |
| access | `access.rebuildAllowlist` | `POST /v1/orgs/{org_id}/access/allowlist:rebuild` | 0.3 | `access.manage` | optional |
| access | `access.listCredentials` | `GET /v1/orgs/{org_id}/credentials` | 0.3 | `access.read` | - |
| access | `access.getCredential` | `GET /v1/orgs/{org_id}/credentials/{credential_id}` | 0.3 | `access.read` | - |
| access | `access.listAccessEvents` | `GET /v1/orgs/{org_id}/access/events` | 0.3 | `access.view_events` | - |
| access | `access.listGuardEvents` | `GET /v1/orgs/{org_id}/gate/events` | 0.3 | `gate.operate` | - |
| access | `access.openAccessPoint` | `POST /v1/orgs/{org_id}/access-points/{point_id}:open` | 0.3 | `access.open` | required |
| access | `access.getAgentCommand` | `GET /v1/orgs/{org_id}/access/commands/{command_id}` | 0.3 | `access.open`, `access.manage` | - |
| agent | `agent.enrol` | `POST /v1/agent/enrol` | 0.3 | agent token | optional |
| agent | `agent.poll` | `POST /v1/agent/poll` | 0.3 | agent token | optional |
| agent | `agent.rotateToken` | `POST /v1/agent/token:rotate` | 0.3 | agent token | optional |
| agent | `agent.connect` | `GET /v1/agent/ws` | 0.3 | agent token | - |
| dashboards | `dashboards.getSiteDashboard` | `GET /v1/orgs/{org_id}/sites/{site_id}/dashboard` | 0.1 | `dashboard.site` | - |
| dashboards | `dashboards.getPortfolioDashboard` | `GET /v1/orgs/{org_id}/dashboard/portfolio` | 0.1 | `dashboard.portfolio` | - |
| dashboards | `dashboards.getMyDashboard` | `GET /v1/orgs/{org_id}/me/dashboard` | 0.1 | `me.dashboard` | - |
| codes | `codes.listCodes` | `GET /v1/orgs/{org_id}/codes` | 0.1 | `codes.read` | - |
| platform | `platform.listOrgs` | `GET /v1/platform/orgs` | 0.1 | `platform.read` | - |
| platform | `platform.createOrg` | `POST /v1/platform/orgs` | 0.1 | `platform.manage` | optional |
| platform | `platform.getOrg` | `GET /v1/platform/orgs/{org_id}` | 0.1 | `platform.read` | - |
| platform | `platform.updateOrg` | `PATCH /v1/platform/orgs/{org_id}` | 0.1 | `platform.manage` | optional |
| platform | `platform.getSettings` | `GET /v1/platform/settings` | 1.0 | `platform.read` | - |
| platform | `platform.updateSettings` | `PATCH /v1/platform/settings` | 1.0 | `platform.manage` | optional |
| platform | `platform.getHealth` | `GET /v1/platform/health` | 0.1 | `platform.read` | - |
| platform | `platform.getBackupStatus` | `GET /v1/platform/backup-status` | 0.1 | `platform.read` | - |
| platform | `platform.listJobs` | `GET /v1/platform/jobs` | 1.0 | `platform.read` | - |
| me | `me.updateProfile` | `PATCH /v1/me/profile` | 0.1 | public / self | optional |
| me | `me.startContactChange` | `POST /v1/me/contact-changes` | 0.1 | public / self | optional |
| me | `me.confirmContactChange` | `POST /v1/me/contact-changes/{change_id}:confirm` | 0.1 | public / self | optional |
| auth | `auth.requestPasswordReset` | `POST /v1/auth/password-reset` | 0.1 | public / self | optional |
| auth | `auth.resetPassword` | `POST /v1/auth/password-reset/consume` | 0.1 | public / self | optional |
