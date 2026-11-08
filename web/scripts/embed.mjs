// Copies web/dist into server/internal/webassets/dist so `go:embed` can see it (go:embed cannot
// reach outside its package directory). Keeps the tracked .gitkeep, replaces everything else.
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))
const src = join(root, 'dist')
const dest = join(root, '..', 'server', 'internal', 'webassets', 'dist')

if (!existsSync(join(src, 'index.html'))) {
  console.error(`embed: ${src}/index.html not found; run \`pnpm build\` first`)
  process.exit(1)
}
mkdirSync(dest, { recursive: true })
for (const name of readdirSync(dest)) {
  if (name !== '.gitkeep') rmSync(join(dest, name), { recursive: true, force: true })
}
cpSync(src, dest, { recursive: true })
console.log(`embed: copied ${src} -> ${dest}`)
