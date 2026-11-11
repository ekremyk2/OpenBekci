import { readdirSync, readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * Contract guard: the web client is typed from the DRAFT spec, so it can call operations the server
 * never served (404 at runtime). Every `.GET('/v1/...')`-style call in web/src must exist in the
 * served bundle (api/openapi.yaml, generated from api/src/openapi.yaml).
 */
const root = resolve(__dirname, '../..')

function servedOperations(): Set<string> {
  const served = new Set<string>()
  let path = ''
  for (const line of readFileSync(resolve(root, '../api/openapi.yaml'), 'utf8').split('\n')) {
    const p = line.match(/^ {2}(\/\S+):\s*$/)
    if (p) path = p[1]
    const m = line.match(/^ {4}(get|post|put|patch|delete):/)
    if (m && path) served.add(`${m[1].toUpperCase()} ${path}`)
  }
  return served
}

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const f = join(dir, e.name)
    if (e.isDirectory()) return e.name === 'test' ? [] : sources(f)
    return /\.tsx?$/.test(e.name) && !/\.(test|gen)\./.test(e.name) ? [f] : []
  })
}

/** Known gaps outside the money and resident screens (audit, people, platform, setup): they 404 today; shrink, never grow. */
const KNOWN_UNSERVED = new Set<string>([
  'GET /v1/orgs/{org_id}/audit-events',
  'POST /v1/orgs/{org_id}/persons:import',
  'GET /v1/orgs/{org_id}/jobs/{job_id}',
  'POST /v1/orgs/{org_id}/occupancies/{occupancy_id}/move-out:preview',
  'POST /v1/platform/orgs',
  'PATCH /v1/platform/orgs/{org_id}',
  'GET /v1/platform/orgs',
  'GET /v1/platform/health',
  'GET /v1/platform/backup-status',
  'GET /v1/platform/jobs',
  'GET /v1/platform/settings',
  'PATCH /v1/platform/settings',
  'POST /v1/orgs/{org_id}/allocation-keys/{key_id}/values:import',
  'GET /v1/orgs/{org_id}/unit-tags',
  'GET /v1/orgs/{org_id}/unit-tags/{tag_id}/units',
  'PUT /v1/orgs/{org_id}/unit-tags/{tag_id}/units',
])

describe('web client uses only served operations', () => {
  it('has no call to an operation absent from api/openapi.yaml', () => {
    const served = servedOperations()
    expect(served.size).toBeGreaterThan(300)
    const missing: string[] = []
    for (const f of sources(resolve(root, 'src'))) {
      const text = readFileSync(f, 'utf8')
      for (const m of text.matchAll(/\.(GET|POST|PUT|PATCH|DELETE)\(\s*['"`]([^'"`]+)['"`]/g)) {
        const op = `${m[1]} ${m[2]}`
        if (!served.has(op) && !KNOWN_UNSERVED.has(op)) missing.push(`${op}  (${f.slice(root.length + 1)})`)
      }
    }
    expect(missing).toEqual([])
  })
})
