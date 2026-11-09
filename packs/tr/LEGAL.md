# Turkish pack: legal notes

Maintainer (legal): **to be named** before release (plan 04-packs.md §5). Status of every value below: **encoded as the
plan states it, awaiting the legal maintainer's sign-off** (packs plan §6.4, overview §11, governance.md "(verify)").

## Disclaimer

OpenBekci is software, not legal advice. The values in this pack are **defaults** for site settings: they are written
into a site's settings when the pack is enabled, shown as "pack default", and **editable per site** wherever the
site's yönetim planı (management plan) may deviate from the statute. Law and practice change; a manager who relies on a
date, a quorum or a rate should check it with the yönetim planı and a lawyer. The KVKK texts are templates for the
site's own review. Nothing here replaces the notarized karar defteri, the notary, or an avukat.

## Date of the law text consulted

The builder of this milestone **could not retrieve the consolidated text of Kat Mülkiyeti Kanunu No. 634 from
mevzuat.gov.tr** (the fetch failed), so no article below was compared with the text. Every row records the claim as the
plan states it. The legal maintainer fills the column "text consulted" with the date of the consolidated text and the
amendment history checked, and removes the "verify" mark from the row when it is confirmed.

## Presets (Kat Mülkiyeti Kanunu No. 634, "Law 634")

| Setting | Value encoded | Article (as claimed) | Text consulted | Known ambiguity | The yönetim planı may override |
|---|---|---|---|---|---|
| `quorum.first_call` | more than half of the owners by number **and** more than half of the arsa payı | art. 29 | not yet | whether "majority" means more than half (encoded) or at least half; whether the two bases are measured separately (encoded) | the planı may require a higher quorum; lowering needs a reason in the audit trail |
| `quorum.second_call` | no quorum | art. 29 | not yet | whether decisions of the second meeting are by a majority of those present (see thresholds) | no |
| `thresholds.default`, `budget`, `election` | more than half of all owners by number and by arsa payı | art. 32 | not yet | whether election of the yönetici and the board follows art. 32 or the planı | the planı |
| `thresholds.routine` | more than half of those present | art. 29 and the planı | not yet | routine matters is a site concept, not a statutory category | yes |
| `thresholds.plan_amendment` | four fifths of all owners by number and by arsa payı | art. 28 | not yet | none known | no (statute) |
| `thresholds.common_area_change` | four fifths | art. 19 | not yet | which changes count as significant | partly |
| `thresholds.main_structure_change` | all owners | art. 18 | not yet | which changes touch the main structure or an owner's rights | no |
| `vote_weight.weight` | one vote per unit, regardless of arsa payı | planı / art. 31 | not yet | many planları weigh by arsa payı | yes |
| `vote_weight.per_person_cap` | one third of all votes | art. 31 | not yet | whether the cap counts proxy votes (encoded: it does) | no |
| `vote_weight.co_owner_representation` | co-owners vote through one representative | art. 30 / 31 | not yet | none known | no |
| `vote_weight.proxy_cap_per_person` / `proxy_small_building_unit_limit` | in buildings of 40 units or fewer a person represents at most two other owners | not yet cited | not yet | the 40-unit threshold and the cap are from the plan's (verify) list | yes |
| `vote_weight.proxy_cap_percent` | in larger buildings a person represents at most 5 % of all votes | not yet cited | not yet | as above | yes |
| `schedule.min_notice_days` | the call goes out at least 15 days before the meeting | art. 29 | not yet | whether a call by email or SMS counts (the core setting `notice_electronic_allowed`, default off, decides) | yes (longer) |
| `schedule.second_meeting_min/max_days` | the second meeting 7 to 15 days after the first | art. 29 | not yet | whether the window is calendar days (encoded) | partly |
| `schedule.ordinary_assembly_month` | January | art. 29 / planı | not yet | statute says "yearly"; the month is the usual planı rule | yes |
| `transfer_rule` | the remaining debt stays with the leaving person | decision S7 | n/a | the buyer's liability for the seller's debt (art. 22 of Law 634 on joint liability of the new owner is **not** modelled) | the parties may agree otherwise; the manager overrides, audited |
| `late_fee_policy` | 5 % per month is the default **and the ceiling**; the site may set a lower rate, never a higher; accrued per day on principal only; no grace | art. 20 | not yet | whether pro-rating a month by days is allowed (the core never charges a full month more than 5 %, the strict reading); whether a partial month is a full month; whether the rate may be compounded (not modelled) | the site may lower the rate and add grace days |

Also in the plan's (verify) list, not encoded as a rule: a yearly reminder for the notary closing of the karar defteri
(the core's reminder job, 90 days default); the dissent window of 30 days after the meeting (a core default, not law).

The templates cite articles in their wording: the call letter (art. 29), the ihtarname (art. 20 and art. 22). They carry
the same (verify) status.

## The decision book

The software never claims to be the karar defteri. The minutes and the "karar defteri sayfa taslağı" are numbered drafts
"for transcription"; the notary closes the book. A decision is final in the system as the core's governance rules say.

## Reference data

- **İl / ilçe** (`data/admin_areas.json`, version 2026-11): 81 provinces by plate code and 973 districts, a static file
  compiled from a public list of administrative names and cross-checked for the counts. **Mahalle (neighbourhood) lists
  are not included**: the public datasets are about 11 MB, far above the 2 MB the pack may embed. Boundaries change by
  law; re-compile the file against the İçişleri Bakanlığı / MERNİS lists when a district is created.
- **Public holidays 2025 to 2027**: fixed national holidays plus Ramazan and Kurban Bayramı (with arife half days) as in
  the Diyanet calendar. Religious dates depend on the moon and are confirmed each year; **check them against the
  Diyanet calendar and the Resmî Gazete** (verify). Administrative extensions of a holiday ("köprü" days) are not listed.
- **Validators**: the vergi kimlik numarası check digit (GİB algorithm), the TCKN checksum as a tax number, the TR IBAN
  (26 characters, mod 97, reserved 10th character 0, bank code not 00000) and plates (province 01 to 81, the plate alphabet
  without Q, W, X, I, O, one to three letters and the permitted digit counts; diplomatic and special plates are not
  covered). They check form, not existence.

## KVKK (Law No. 6698)

- The pack ships the **aydınlatma metni** (KVKK art. 10) and one **açık rıza** text per processing purpose: identity,
  contact, financial, camera and visitor logs, in Turkish and English, versioned (`ConsentVersion`). Each site fills
  its data controller details in the site setting `tr_kvkk_data_controller` (legal name, address, contact, VERBİS
  number); the printed text refers to that setting.
- **VERBİS**: data controllers register with VERBİS (Veri Sorumluları Sicil Bilgi Sistemi) unless exempt. Whether a
  small condominium (a site management without a company) must register depends on the number of employees and the
  balance sheet thresholds the Kurul sets and on whether the management is a legal person (a management company
  usually is; verify the exemption for the site itself). The legal maintainer should state the rule used; the pack
  only stores the sicil number.
- **Cross-border transfer** (Law No. 7499, in force 2024): transfer needs an adequacy decision, or appropriate
  safeguards (standard contract notified to the Kurul within 5 working days), or the explicit consent only as an
  exceptional basis (verify the current wording). A self-hosted OpenBekci whose data stays in Turkey makes no
  transfer; an SMS or card provider abroad may.
- **Data residency recommendation**: host in Türkiye. The software keeps TCKN encrypted and masked, never lists debtors
  publicly (KVKK Board practice, verify), and keeps access to camera and visitor logs to authorised staff.
- **İYS**: messages that are operational (a call to a meeting, a receipt, a debt notice, an OTP) are exempt from the
  commercial message consent regime (verify); marketing messages are out of scope. Every message the software sends is
  operational. The İleti Merkezi provider (`tr.iletimerkezi`) enforces this in code: every message is sent with the
  API field `iys` = `"0"` (not a commercial message), there is no way to send with `"1"`, and the İYS consent and
  filtering flow (`iysList`, İYS registration of recipients) is not implemented. Whether a given notice (for example a
  payment reminder with a late-fee warning) is operational or commercial is the sender's responsibility; the legal
  maintainer should confirm the classification of each notice type before 1.0 (verify).
- **Card payments and bank statements** (TR-2): card payments go through iyzico, which is the payment institution and
  holds the card data; the software never sees a card number. It sends iyzico the payer's name, and e-mail and phone
  number when held, never the TCKN or an address (placeholders instead, see `PAYMENTS.md`). Bank statements contain
  personal data of third parties (payer names and descriptions); the raw rows are deleted after 90 days. Whether the
  management company or the site is the merchant, and therefore whose money the clearing account holds, is the owner's
  decision recorded in `docs/decisions/money.md` (M5); the software does not decide it and gives no tax or
  payment-institution advice (verify with the accountant whether collecting dues by card on behalf of the owners
  needs anything beyond the provider's own onboarding).

## What the software does NOT do (tax and employer obligations)

OpenBekci keeps a site's books and papers. It does **not** perform, file or remind of the following; the manager or an
accountant (mali müşavir) must:

- **Kimlik Bildirme Kanunu**: reporting residents and changes of residence to the authorities (the kat maliki and
  kiracı bildirimi). The occupancy records are not a notification.
- **Kapıcı and other employees**: SGK registration and notifications, monthly premium and muhtasar declarations,
  severance (kıdem tazminatı) and notice, annual leave, work-accident rules. The chart has a severance provision
  account; the software calculates nothing.
- **Withholding (stopaj)** on rent or interest the site pays, and any **tax or declaration** of the site: the software
  does not compute or file muhtasar, KDV, gelir/kurumlar vergisi or damga vergisi.
- **Common-area rent income** (for example a rooftop antenna or shop rent): whether it is taxable income of the owners
  or the site, and how it is declared (verify scope). The software records the income, not the tax.
- **E-Fatura / e-Arşiv**: issuing invoices. Outbound e-invoicing is a non-goal; inbound intake is post-1.0.
- **Notary and court steps**: karar defteri closing, ihtarname by noter, icra takibi and lawsuits under art. 20 and 22.
  The software drafts the letter; sending it by noter, and the follow-up, are the manager's.
- **Insurance obligations** (DASK and building insurance).
- **Aidat decisions' legality**: the software checks the thresholds a site configured; it does not decide whether a
  decision is valid.

## Sources the legal maintainer should use

Kat Mülkiyeti Kanunu No. 634 (mevzuat.gov.tr, consolidated), the site's yönetim planı, the KVKK No. 6698 and Law
No. 7499, the Kurul's VERBİS and transfer announcements, the Diyanet religious holidays calendar, and the GİB
algorithm for the vergi kimlik numarası.
