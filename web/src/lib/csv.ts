/** Minimal RFC 4180 CSV: quoting on the way out, quote-aware parsing on the way in. */

export function csvCell(value: string | number | bigint | null | undefined): string {
  const s = value === null || value === undefined ? '' : String(value)
  return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
}

export function toCsv(rows: (string | number | bigint | null | undefined)[][]): string {
  return rows.map((r) => r.map(csvCell).join(',')).join('\r\n')
}

/** Parses CSV text into records keyed by the header row. Quoted cells may hold commas and newlines. */
export function parseCsv(text: string): Record<string, string>[] {
  const rows: string[][] = []
  let row: string[] = []
  let cell = ''
  let quoted = false
  for (let i = 0; i < text.length; i++) {
    const c = text[i]!
    if (quoted) {
      if (c === '"' && text[i + 1] === '"') {
        cell += '"'
        i++
      } else if (c === '"') quoted = false
      else cell += c
    } else if (c === '"') quoted = true
    else if (c === ',') {
      row.push(cell)
      cell = ''
    } else if (c === '\n' || c === '\r') {
      if (c === '\r' && text[i + 1] === '\n') i++
      row.push(cell)
      cell = ''
      rows.push(row)
      row = []
    } else cell += c
  }
  if (cell !== '' || row.length > 0) {
    row.push(cell)
    rows.push(row)
  }
  const [head, ...rest] = rows.filter((r) => r.some((x) => x !== ''))
  const cols = (head ?? []).map((s) => s.trim())
  return rest.map((r) => Object.fromEntries(r.map((v, i) => [cols[i] ?? `c${i}`, v.trim()])))
}
