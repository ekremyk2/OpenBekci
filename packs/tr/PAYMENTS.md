# Card payments with iyzico (`tr.iyzico`)

Milestone TR-2. The provider id is `tr.iyzico`; a payment's method is `card:tr.iyzico`, its clearing account `1190.tr.iyzico`.
It runs on the settlement model of the SDK (`docs/plans/04-packs.md` section 3.8): the resident pays **gross**, iyzico
keeps a **fee**, the **net** is paid out to the bank later. The core posts gross against the receivable, the fee to
`5050` and the net to the clearing account; the settlement moves the clearing account to the bank.

**No iyzico sandbox account was available.** Everything below is built from the public documentation
(`docs.iyzico.com`, read on 2026-11-09) and tested against an in-process fake that reproduces the documented request and
response shapes and verifies the `IYZWSv2` signature of every request. Nothing has run against iyzico itself. The
assumptions are numbered in the table at the end; **before the first live payment run the sandbox smoke test below**.

## Flow

1. **Checkout.** The core asks the provider to `CreateCheckout`. The pack calls
   `POST /payment/iyzipos/checkoutform/initialize/auth/ecom` (Checkout Form, "hosted payment page") with
   `conversationId` = the checkout reference, price = paidPrice = the gross amount (`"100.00"`), `currency` TRY,
   `enabledInstallments` `[1]`, a `callbackUrl` and a `basketId` that carries the payment target (see below). iyzico answers
   with `token` and `paymentPageUrl`; the pack returns `Checkout{ProviderTxnID: token, CheckoutURL: paymentPageUrl}`.
   **The provider transaction id is the Checkout Form token**, from creation to refund.
2. **The resident pays** on iyzico's page. iyzico then does two things:
   - the **browser callback**: the resident's browser posts a form with `token` to the `callbackUrl`;
   - the **server notification** (webhook), if the merchant enabled it: a JSON body with `X-IYZ-SIGNATURE-V3`.
   Both arrive at the core route `POST /v1/orgs/{org}/packs/tr/payments/tr.iyzico/webhook` and reach `HandleWebhook`.
3. **`HandleWebhook`** never trusts the body for amounts. The webhook is authenticated by its signature
   (`HMAC-SHA256` hex, key = secret key, over `secretKey + iyziEventType + iyziPaymentId + token + paymentConversationId + status`,
   the documented HPP format); a missing or wrong signature is `ErrInvalidSignature` (401). The browser callback carries
   no signature; it is authenticated by what the pack does next. For both, the pack **retrieves the payment by token**
   (`POST /payment/iyzipos/checkoutform/auth/ecom/detail`, signed with the merchant's keys) and takes everything from that
   answer: `paymentStatus` (`SUCCESS` paid, `FAILURE` failed, anything else pending), `paidPrice` (gross),
   `iyziCommissionFee + iyziCommissionRateAmount + merchantCommissionRateAmount` (fee, rounded once on the sum) and
   `basketId` (the target). Gross minus fee is net. A paid answer becomes `EventKindPaid`, a failed one
   `EventKindFailed`; a pending one no event. Callback and webhook for the same payment give the same event, which the
   core records once (idempotent on `(org, provider, token)`).
   A browser callback is answered with a `303` to the return URL with `?status=paid|failed|pending` appended, but only when
   the return URL in the callback query carries the pack's HMAC (so the callback cannot be used as an open redirect); else
   `200 OK`.
4. **`Status`** retrieves by token. **`Refund`** (full only) retrieves the payment for its `paymentId`, looks at the
   transaction detail report (`GET /v2/reporting/payment/details?paymentId=`) and, when the payment is not refunded yet,
   calls `POST /v2/payment/refund` with `paymentId` and the full price. `ProviderRefundID` is `iyz-refund-<paymentId>`
   (a payment is refunded once, in full).
5. **`Settlements`** derives one settlement per day from the daily transaction report
   (`GET /v2/reporting/payment/transactions?transactionDate=&page=`): payments add, refunds and cancellations subtract
   `paidPrice` and `merchantPayoutAmount`; the reference is `iyzico-<date>`, deterministic, so a repeated sync posts nothing
   twice. **Limitation:** iyzico documents no payout report API (payouts are reconciled with downloadable settlement
   files) and pays out after a blockage period, so `SettledOn` is the transaction date and not the bank date, and
   `ProviderTxnIDs` is left empty (the report carries `paymentId`, the core keys payments by token). A manager reconciles
   the clearing account against the bank statement; a later release should read the settlement files.

### The payment target travels in `basketId`

The webhook must say which unit the money is for, and the conformance suite records a payment from a webhook alone.
The target is encoded in iyzico's free-text `basketId` and comes back on retrieve (an authenticated, server-to-server
answer, so it cannot be forged by the resident): `tr1.<site>.<unit>.<o|t>[.<charge>,<charge>...]`. A basket that is not of
this shape is refused (`ErrRejected`): a payment made on the same iyzico account for something else is never recorded.
The length is capped at 255 characters (assumption 9), about five charge ids; a checkout for more charges is refused with
a message to pay fewer at once.

### KVKK minimisation: placeholders

iyzico requires buyer fields OpenBekci does not hold or should not send. The pack sends the name (split into name and
surname), the e-mail and the phone number **only if the person has them**, and fixed placeholders for the rest:

| Field | Value |
|---|---|
| `identityNumber` | `11111111111` (iyzico's documented sample; the TCKN is never sent) |
| `registrationAddress`, billing and shipping `address` | `Adres bilgisi paylasilmadi` |
| `city`, `country` | `Istanbul`, `Turkey` |
| `email` (missing) | `sakin@example.invalid` |
| `gsmNumber` (missing) | `+905000000000` |
| `ip` | `85.34.78.112` (iyzico's documented sample; the core does not pass the resident's address) |
| basket item | one `VIRTUAL` item named "Aidat ve ortak gider odemesi" (en: "Dues and shared costs payment"), category `Aidat` |

Name, e-mail and phone number are personal data sent to iyzico, a processor in Türkiye; the aydınlatma metni should say so.

## Settings

| Key | Where | Meaning |
|---|---|---|
| `tr_payments_iyzico_enabled` | org, site | switch |
| `tr_payments_iyzico_api_key`, `tr_payments_iyzico_secret_key` | org, site (secret, write-only) | the merchant's keys |
| `tr_payments_iyzico_sandbox` | org, site | use `sandbox-api.iyzipay.com` instead of `api.iyzipay.com` |
| `tr_payments_iyzico_callback_base_url` | org, site | the public address of the OpenBekci server, for example `https://api.example.com`. iyzico must reach `<base>/v1/orgs/{org}/packs/tr/payments/tr.iyzico/webhook` (valid TLS certificate required). Without it the origin of the checkout's return URL is used, which is right only when the web app and the API share one host |

For the server notification, enter the callback URL in the iyzico merchant panel (Ayarlar, Bildirim/Webhook) and ask
iyzico to enable the signature header (`entegrasyon@iyzico.com`): **without `X-IYZ-SIGNATURE-V3` the pack rejects every JSON
notification**; the browser callback does not need it.

`ValidateSettings` checks the shape only (both keys, https address); it does not call iyzico.

### Merchant of record

Decision M5: the merchant is per organization (the management company's iyzico account) with the site's own account as
the alternative; the money is the client's and goes through the clearing account either way. The pack reads the keys at
site scope first and falls back to the organization's. **TODO(PR #35):** when `sdk.AccountScope` lands, replace that lookup
with the scope it returns for the core's merchant-of-record setting, and make `Status`, `Refund` and `Settlements` (which
today know no site and read the organization scope) resolve the same account as the checkout did. Until then a site that
keeps its own iyzico account works for checkout and webhooks (the callback URL carries `site=`) but not for `Status`,
`Refund` and `Settlements`.

## Tests, and switching the fake for a recording

`packs/tr/trtest` holds the fake (`trtest.NewIyzico(apiKey, secret)`): it implements initialize, retrieve, refund, the
detail and the daily reports with the documented shapes and rejects any request whose `IYZWSv2` signature does not verify.
The pack is tested with it directly (`iyzico_test.go`) and through the SDK conformance suite and the core
(`server/internal/sdktest/tr_p2_test.go`).

Every call goes through one `*http.Client`, given by `tr.WithHTTPClient`. To record against the sandbox with
[`go-vcr`](https://github.com/dnaeon/go-vcr) (not vendored: it was not available offline) and replay in CI:

```go
rec, _ := recorder.New("testdata/iyzico-sandbox")            // replay by default
if os.Getenv("OPENBEKCI_IYZICO_RECORD") != "" {               // absent in CI and in this repository
	rec, _ = recorder.NewWithOptions(&recorder.Options{CassetteName: "testdata/iyzico-sandbox", Mode: recorder.ModeRecordOnly})
}
// scrub the Authorization and x-iyzi-rnd headers in a hook before saving
pack := tr.New(tr.WithHTTPClient(&http.Client{Transport: rec}))
```

then run the same flow as the tests with the sandbox keys. The `IYZWSv2` random key makes request bodies differ per call,
so match requests on method and path only.

**Sandbox smoke test** (needs the sandbox merchant account, https://sandbox-merchant.iyzipay.com): create a checkout,
pay with a sandbox test card and OTP `123456`, check that the callback records the payment with the right fee, retrieve,
refund, and read the daily report. Assumptions 1 to 10 should each be confirmed or corrected there.

## Assumptions (not verified against iyzico)

| # | Assumption | If wrong |
|---|---|---|
| 1 | Sandbox base URL is `https://sandbox-api.iyzipay.com` (the docs fetched do not list it) | change `iyzicoSandboxBase` |
| 2 | The query string is not part of the signed uri path of the GET report calls | reports fail with 401: sign the path with the query |
| 3 | `paymentStatus` of an initialized but unpaid token is empty or not `SUCCESS`/`FAILURE` (pending); `SUCCESS` and `FAILURE` are the final values | an unpaid token reading as `FAILURE` would be recorded as failed (no money moves, so harmless); a status poll then never shows pending |
| 4 | An unknown token is answered with a failure whose message mentions the token (`bulunamad`, `invalid`, `not found`, `geçersiz`); credential failures mention `api key`, `imza` or `signature` (or are HTTP 401); all other failures are `ErrRejected` | `Status` of an unknown token is a 422 instead of a 404 |
| 5 | The merchant commission (`merchantCommissionRateAmount`, instalment cost) belongs to the fee as the plan says; with `enabledInstallments [1]` it should be 0 | the fee is overstated by the instalment cost: sum and `price` vs `paidPrice` need a look |
| 6 | `paymentRefundStatus` of the detail report is one of `FULL`, `FULLY_REFUNDED`, `REFUNDED` once a payment is refunded in full; iyzico documents no idempotency for refunds | a replayed refund calls iyzico again and gets an error |
| 7 | The daily report has `transactionType` `PAYMENT`, `REFUND`, `CANCEL`, and numeric `paidPrice`, `iyzicoFee`, `iyzicoCommission`, `merchantPayoutAmount` per transaction; refund lines may be positive or negative (the pack subtracts the absolute value) | settlement nets are off |
| 8 | A server notification for a Checkout Form payment has `iyziEventType` `CHECKOUT_FORM_AUTH`, the fields of the documented HPP payload and no amounts; refunds started in the merchant panel are **not** reported by webhook | a panel-side refund must be recorded by hand (a refund event kind exists in the SDK for providers that report them) |
| 9 | `basketId` accepts at least 255 characters of `[A-Za-z0-9.,-]` and is returned unchanged on retrieve; the `signature` of the retrieve answer is not verified (the call is authenticated and over TLS) | long baskets are refused by iyzico; verify the signature as iyzico's SDKs do if desired |
| 10 | Buyer `ip` is accepted as the documented sample address; `tokenExpireTime` is not used | the checkout is rejected for the placeholder: pass the resident's address through the SDK |
