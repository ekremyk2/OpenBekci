# Money

## Who pays what

When a unit has a tenant, the **tenant owes regular dues** (aidat) and the **owner owes special assessments** (demirbaş, big repairs). Every charge type has a default payer, and the manager can override it per unit. A unit therefore has two running balances, an owner side and a tenant side; the owner sees both, the tenant only their own.

## How costs are split

All splitting rules are available and each site picks one per dues plan: by land share (arsa payı), equal per unit, a fixed amount per unit from an uploaded table (for example a heating-meter company's monthly file), or by area (m²). Multi-block sites can split a cost among one block only. New sites start with the statutory defaults (staff costs equal per unit, everything else by land share), so nothing needs configuring.

## Where dues come from

The manager sets a monthly dues amount directly (per unit, per share or per plan). Dues derived from a yearly budget (işletme projesi) are a later feature.

## Late fees

The statutory late fee is 5 % per month (a site may set a lower rate, never a higher one; this default comes from the Turkish pack). It is computed automatically, shown continuously, booked once a month, and can be waived.

## How residents pay

| Channel | How the system learns about it |
|---|---|
| **Cash** | The manager or collector records it; a sequentially numbered receipt (aidat makbuzu) is printed or sent; the collector is tracked for reconciliation. |
| **Bank transfer** | The manager uploads the bank's CSV or Excel statement. The system suggests the unit for each line (reference text, payer name, amount) and the manager confirms. Two Turkish banks are supported by the Turkish pack; other banks are a format file. |
| **Card, from the app** | A payment provider's hosted checkout (iyzico, in the Turkish pack). Gross amount, provider fee, net settlement and refunds are all recorded. |

Card payments go to a **merchant of record**: by default each site's own account; an organization may instead collect centrally and hold each site's net as client money.

## Expenses

Expense entry is simple: what, who was paid, how much, when, from which account (bank or cash) and an optional receipt photo. Recurring expenses (electricity, caretaker salary, elevator maintenance) repeat monthly. There is no approval workflow; spending approval is a board decision made in meetings.

## Moving an existing site

On day one a site imports per-unit debts from an Excel sheet, its bank and cash balances at the go-live date and, when sample files exist, an export from another program. Bills unpaid at go-live are entered by hand.

## Reports

Cash-basis income and expense for any period by category (the sheet read at the assembly); the **debtor list** (principal, late fee, total, months overdue, visible to manager and board only); a per-unit **statement as PDF**; and budget against actual.
