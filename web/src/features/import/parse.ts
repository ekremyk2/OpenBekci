/** Spreadsheet parsing for the import wizard and the allocation-key grid (CSV here, XLSX lazily). */

export interface ParsedTable {
  headers: string[]
  rows: string[][]
  delimiter: string | null
  sheet: string | null
  /** Columns holding a non-empty cell that is not a whole number (set when the wizard trims the table). */
  nonIntegerColumns?: string[]
}

/** Picks the delimiter that splits the first line into the most cells (`,` `;` tab). */
export function detectDelimiter(text: string): string {
  const first = text.split(/\r?\n/, 1)[0] ?? ''
  let best = ','
  let bestCount = 0
  for (const d of [',', ';', '\t']) {
    const n = first.split(d).length - 1
    if (n > bestCount) {
      best = d
      bestCount = n
    }
  }
  return best
}

/** RFC 4180 style parser: quoted cells, doubled quotes, newlines inside quotes. */
export function parseCsv(input: string, delimiter = detectDelimiter(input)): string[][] {
  const text = input.charCodeAt(0) === 0xfeff ? input.slice(1) : input
  const rows: string[][] = []
  let row: string[] = []
  let cell = ''
  let quoted = false
  for (let i = 0; i < text.length; i++) {
    const c = text[i]!
    if (quoted) {
      if (c === '"') {
        if (text[i + 1] === '"') {
          cell += '"'
          i++
        } else quoted = false
      } else cell += c
    } else if (c === '"' && cell === '') quoted = true
    else if (c === delimiter) {
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
  return rows.filter((r) => r.some((c) => c.trim() !== ''))
}

export function toCsv(rows: (string | number | null | undefined)[][], delimiter = ','): string {
  const esc = (v: string | number | null | undefined) => {
    const s = v === null || v === undefined ? '' : String(v)
    return /["\n\r]/.test(s) || s.includes(delimiter) ? `"${s.replace(/"/g, '""')}"` : s
  }
  return rows.map((r) => r.map(esc).join(delimiter)).join('\r\n')
}

function table(rows: string[][], delimiter: string | null, sheet: string | null): ParsedTable {
  const [head = [], ...rest] = rows
  return { headers: head.map((h) => h.trim()), rows: rest, delimiter, sheet }
}

/** Reads a CSV or XLSX file's header row and data rows. XLSX support is loaded on demand. */
export async function parseFile(file: File): Promise<ParsedTable> {
  if (/\.xlsx$/i.test(file.name)) {
    const { readSheet } = await import('read-excel-file/browser')
    const data = await readSheet(file)
    const rows = data.map((r) =>
      r.map((cell) =>
        cell === null || cell === undefined
          ? ''
          : cell instanceof Date
            ? cell.toISOString().slice(0, 10)
            : String(cell),
      ),
    )
    return table(rows, null, null)
  }
  const text = await file.text()
  const delimiter = detectDelimiter(text)
  return table(parseCsv(text, delimiter), delimiter, null)
}

/** Guesses the source column for a target field from header names (case/diacritics-insensitive). */
export function normalizeHeader(h: string): string {
  return h
    .toLowerCase()
    .replace(/ı/g, 'i')
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, ' ')
    .trim()
}
