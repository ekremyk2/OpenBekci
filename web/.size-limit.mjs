// Initial-route budget (web plan §14): the JS a first-time visitor downloads to see the first screen
// (the login page) must stay <= 260 kB gzipped (raised from 250 kB in 0.4: the Turkish and English catalogs carry every screen's strings). Counted:
//   - the entry script and every modulepreload listed in dist/index.html,
//   - the /login route chunk and everything it statically imports (not dynamic imports),
//   - the largest locale catalog chunk a person can pick in the UI (en or tr; one catalog is loaded
//     per visitor, worst case counted). The pseudo-locale catalog is developer-only (`?locale=`,
//     see src/lib/i18n.ts) and its accented, longer strings would dominate the budget, so it is
//     not counted.
// A second budget covers the heaviest lazy route: the largest non-initial chunk (route screens,
// qrcode, the XLSX reader) plus the non-initial chunks it imports statically, gzipped, <= 40 kB.
// The camera QR scanner (html5-qrcode, loaded only when someone starts scanning at the desk or the
// gate) is a vendor library of its own, 107 kB gzipped: it has a third budget of 115 kB and is left
// out of the 40 kB one, so a regression in any screen chunk still shows.
// Run `pnpm build && pnpm size`.
import { readdirSync, readFileSync } from 'node:fs'
import { gzipSync } from 'node:zlib'

const dist = new URL('./dist/', import.meta.url)
const html = readFileSync(new URL('./index.html', dist), 'utf8')
const files = new Set(readdirSync(new URL('./assets/', dist)))

const initial = new Set(
  [
    ...html.matchAll(
      /<(?:script[^>]*src|link[^>]*rel="modulepreload"[^>]*href)="\/assets\/([^"]+\.js)"/g,
    ),
  ].map((m) => m[1]),
)

/** `name` plus the chunks it imports statically (`from"./x.js"` / `import"./x.js"`). */
function closure(name, seen = new Set()) {
  if (seen.has(name)) return seen
  seen.add(name)
  const src = readFileSync(new URL(`./assets/${name}`, dist), 'utf8')
  for (const m of src.matchAll(/(?:from|import)\s*["']\.\/([^"']+\.js)["']/g)) {
    if (files.has(m[1])) closure(m[1], seen)
  }
  return seen
}

const login = [...files].find((f) => /^login\.index-.*\.js$/.test(f))
if (!login) throw new Error('size: login route chunk not found in dist/assets')

// the pseudo catalog is the only one with these letters (Turkish uses none of them)
const isPseudo = (f) => /[ƀŷŕĺ]/.test(readFileSync(new URL(`./assets/${f}`, dist), 'utf8'))
const catalogs = [...files]
  .filter((f) => /^messages-.*\.js$/.test(f) && !isPseudo(f))
  .sort(
    (a, b) =>
      readFileSync(new URL(`./assets/${b}`, dist)).length -
      readFileSync(new URL(`./assets/${a}`, dist)).length,
  )

const counted = new Set(initial)
for (const f of closure(login)) counted.add(f)
if (catalogs[0]) counted.add(catalogs[0])

// Largest lazy chunk (by gzip size) that is not part of the initial route or a locale catalog.
const gz = (f) => gzipSync(readFileSync(new URL(`./assets/${f}`, dist))).length
const scanner = [...files].find((f) => /^QrScannerCore-.*\.js$/.test(f))
const lazy = [...files]
  .filter((f) => f.endsWith('.js') && !counted.has(f) && !/^messages-/.test(f) && f !== scanner)
  .sort((a, b) => gz(b) - gz(a))
const heavy = lazy[0]
const heavySet = new Set(heavy ? [...closure(heavy)].filter((f) => !counted.has(f)) : [])

const scannerSet = new Set(scanner ? [...closure(scanner)].filter((f) => !counted.has(f)) : [])

export default [
  {
    name: 'initial route JS (entry + preloads + login route + catalog, gzip)',
    path: [...counted].sort().map((f) => `dist/assets/${f}`),
    limit: '260 kB',
    gzip: true,
  },
  {
    name: `largest lazy route chunk (${heavy ?? 'none'} + its own imports, gzip)`,
    path: [...heavySet].sort().map((f) => `dist/assets/${f}`),
    limit: '40 kB',
    gzip: true,
  },
  {
    name: `camera QR scanner (${scanner ?? 'none'} + its own imports, gzip)`,
    path: [...scannerSet].sort().map((f) => `dist/assets/${f}`),
    limit: '115 kB',
    gzip: true,
  },
]
