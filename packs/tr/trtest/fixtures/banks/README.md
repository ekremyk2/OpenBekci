# Bank statement fixtures (synthetic)

**Every file here is synthetic.** The accounts, names, descriptions and amounts
are invented; no real customer data is in this repository. The *shape* of each
file (header names, delimiter, encoding, amount columns) is what the importers
of `packs/tr` were built against, from public knowledge of the banks' exports,
because the owner has no real exports yet (decision in `docs/decisions/money.md`).
Replace or add real, anonymized exports when they exist and adjust the format
files in `packs/tr/bankformats/`.

| File | Format | What it exercises |
|---|---|---|
| `ziraat_hesap_hareketleri.csv` | `tr.ziraat`, UTF-8, `;` | preamble rows before the header, one signed amount column, Turkish letters, a row with an invalid date (reported as a row error), a `Toplam` footer, the reference code inside the description |
| `ziraat_hesap_hareketleri.xlsx` | `tr.ziraat`, workbook | numeric amount and balance cells, one amount stored as text (`1.250,00`) |
| `isbank_ekstre_cp1254.csv` | `tr.isbank`, windows-1254, `;` | separate debit and credit columns, date and time, newest line first (balances chain backwards), `Devreden Bakiye` footer, a counterparty column |
| `isbank_ekstre.xlsx` | `tr.isbank`, workbook | real date cells (day serials), numeric debit and credit cells with empty cells, newest first |

The workbooks were written with `excelize`; the windows-1254 file was encoded
from the UTF-8 text with `golang.org/x/text/encoding/charmap`.
The expectations (lines, total, row errors) are in `../banks.go`.
