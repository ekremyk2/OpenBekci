// Initial-load budget (web plan §14): the JS the browser needs for the first screen —
// the entry script and every modulepreload listed in dist/index.html — must stay ≤ 250 kB gzipped.
// Lazy route chunks are not counted. Run `pnpm build && pnpm size`.
import { readFileSync } from 'node:fs'

const html = readFileSync(new URL('./dist/index.html', import.meta.url), 'utf8')
const initial = [
  ...html.matchAll(
    /<(?:script[^>]*src|link[^>]*rel="modulepreload"[^>]*href)="\/(assets\/[^"]+\.js)"/g,
  ),
].map((m) => `dist/${m[1]}`)

export default [{ name: 'initial JS (gzip)', path: initial, limit: '250 kB', gzip: true }]
