// Initial-route budget (web plan §14): the JS a first-time visitor downloads to see the first screen
// (the login page) must stay <= 250 kB gzipped. Counted:
//   - the entry script and every modulepreload listed in dist/index.html,
//   - the /login route chunk and everything it statically imports (not dynamic imports),
//   - the largest locale catalog chunk (one catalog is loaded per visitor; worst case counted).
// A second budget covers the heaviest lazy route: the largest non-initial chunk (route screens,
// qrcode, the XLSX reader) plus the non-initial chunks it imports statically, gzipped, <= 40 kB (measured 18.6 kB: the qrcode chunk).
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

const catalogs = [...files]
  .filter((f) => /^messages-.*\.js$/.test(f))
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
const lazy = [...files]
  .filter((f) => f.endsWith('.js') && !counted.has(f) && !/^messages-/.test(f))
  .sort((a, b) => gz(b) - gz(a))
const heavy = lazy[0]
const heavySet = new Set(heavy ? [...closure(heavy)].filter((f) => !counted.has(f)) : [])

export default [
  {
    name: 'initial route JS (entry + preloads + login route + catalog, gzip)',
    path: [...counted].sort().map((f) => `dist/assets/${f}`),
    limit: '250 kB',
    gzip: true,
  },
  {
    name: `largest lazy route chunk (${heavy ?? 'none'} + its own imports, gzip)`,
    path: [...heavySet].sort().map((f) => `dist/assets/${f}`),
    limit: '40 kB',
    gzip: true,
  },
]
