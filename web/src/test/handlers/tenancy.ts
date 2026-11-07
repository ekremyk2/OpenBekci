import { http, HttpResponse } from 'msw'
import type { Schema } from '@/api/draft'
import { parseCsv } from '@/features/import/parse'
import { db, ORG, recomputeOccupancy, SITE, type JobState } from './db'
import { matches, newId, nowIso, notFound, paginate, problem, versionConflict } from './http'

/* MSW handlers for the draft `tenancy` module, written from the spec (api/src/paths/tenancy). */

const o = (path: string) => `*/v1/orgs/:orgId/${path}`

export function requireReauth() {
  return Date.now() < db.reauthUntil
    ? null
    : problem(403, 'reauth_required', 'Confirm it is you to continue.')
}

function startJob(
  kind: string,
  work: () => void,
  result: Record<string, unknown> | null = null,
): string {
  const id = newId('job')
  const job: JobState = {
    polls: 0,
    onDone: work,
    job: {
      id,
      org_id: ORG,
      kind: kind as Schema<'Job'>['kind'],
      status: 'queued',
      progress: 0,
      result_file_id: null,
      result,
      error: null,
      started_at: null,
      finished_at: null,
      created_by: null,
      created_at: nowIso(),
      updated_at: nowIso(),
    },
  }
  db.jobs[id] = job
  return id
}

/** Wrong value for a unique `door_number` etc. */
const duplicate = (field: string, message: string) =>
  problem(422, 'validation_failed', 'The request has invalid fields.', [
    { field, code: 'duplicate', message },
  ])

type ImportRow = {
  row: number
  status: 'ok' | 'warning' | 'error'
  errors: { field: string | null; code: string; message: string | null; params: null }[]
  data: Record<string, unknown>
}

/** Minimal multipart reader (undici's `request.formData()` asserts under jsdom). */
async function readMultipart(request: Request): Promise<Map<string, string>> {
  const text = await request.text()
  const boundary = text.split('\r\n', 1)[0] ?? ''
  const fields = new Map<string, string>()
  for (const part of text.split(boundary).slice(1)) {
    if (part.startsWith('--')) break
    const [head = '', ...rest] = part.replace(/^\r\n/, '').split('\r\n\r\n')
    const name = /name="([^"]+)"/.exec(head)?.[1]
    if (name) fields.set(name, rest.join('\r\n\r\n').replace(/\r\n$/, ''))
  }
  return fields
}

async function readImport(request: Request) {
  const form = await readMultipart(request)
  const mappingRaw = form.get('mapping')
  const mapping = mappingRaw
    ? (JSON.parse(mappingRaw) as { columns?: Record<string, string>; delimiter?: string | null })
    : { columns: {} }
  const rows = parseCsv(form.get('file') ?? '', mapping.delimiter ?? undefined)
  const [head = [], ...rest] = rows
  const cols = mapping.columns ?? {}
  const pick = (r: string[], target: string) => {
    const src = cols[target]
    if (src === undefined) return ''
    const idx = /^\d+$/.test(src) && !head.includes(src) ? Number(src) : head.indexOf(src)
    return idx >= 0 ? (r[idx] ?? '').trim() : ''
  }
  return { form, rest, pick }
}

function report(rows: ImportRow[], dryRun: boolean, jobId: string | null) {
  const count = (s: string) => rows.filter((r) => r.status === s).length
  return {
    dry_run: dryRun,
    total_rows: rows.length,
    ok_rows: count('ok'),
    warning_rows: count('warning'),
    error_rows: count('error'),
    rows: [...rows].sort((a, b) => Number(b.status === 'error') - Number(a.status === 'error')),
    rows_truncated: false,
    job_id: jobId,
  }
}

export function importHandler(kind: 'units' | 'people' | 'opening') {
  return async ({ request }: { request: Request }) => {
    const dryRun = new URL(request.url).searchParams.get('dry_run') === 'true'
    const { form, rest, pick } = await readImport(request)
    const siteId = form.get('site_id') ?? SITE
    const rows: ImportRow[] = []
    const apply: (() => void)[] = []
    const seen = new Set<string>()
    rest.forEach((r, i) => {
      const row = i + 1
      const errors: ImportRow['errors'] = []
      const data: Record<string, unknown> = {}
      if (kind === 'units') {
        const door = pick(r, 'door_number')
        const type = pick(r, 'type') || 'flat'
        const area = pick(r, 'area_m2')
        const block = pick(r, 'block')
        Object.assign(data, {
          door_number: door,
          type,
          area_m2: area ? Number(area.replace(',', '.')) : null,
          block,
        })
        if (!door)
          errors.push({
            field: 'door_number',
            code: 'required',
            message: 'Door number is required',
            params: null,
          })
        else if (
          seen.has(door) ||
          db.units.some((u) => u.site_id === siteId && u.door_number === door)
        )
          errors.push({
            field: 'door_number',
            code: 'duplicate',
            message: `Door number ${door} already exists`,
            params: null,
          })
        if (area && Number.isNaN(Number(area.replace(',', '.'))))
          errors.push({
            field: 'area_m2',
            code: 'invalid',
            message: 'Area is not a number',
            params: null,
          })
        seen.add(door)
        if (errors.length === 0)
          apply.push(() => {
            let b =
              db.blocks.find(
                (x) => x.site_id === siteId && (x.name === block || (!block && x.is_default)),
              ) ?? db.blocks.find((x) => x.site_id === siteId)
            if (!b && block) {
              b = {
                id: newId('block'),
                org_id: ORG,
                site_id: siteId,
                name: block,
                is_default: false,
                created_at: nowIso(),
                updated_at: nowIso(),
                version: 1,
              }
              db.blocks.push(b)
            }
            db.units.push({
              id: newId('unit'),
              org_id: ORG,
              site_id: siteId,
              block_id: b?.id ?? 'block-a',
              door_number: door,
              legal_ref: pick(r, 'legal_ref') || null,
              type: type as Schema<'Unit'>['type'],
              area_m2: area ? Number(area.replace(',', '.')) : null,
              parent_unit_id: null,
              status: 'active',
              vacant: true,
              occupancy: { owners: 0, tenants: 0, household: 0 },
              created_at: nowIso(),
              updated_at: nowIso(),
              version: 1,
              payer_overrides: {},
            })
          })
      } else if (kind === 'people') {
        const name = pick(r, 'display_name')
        Object.assign(data, {
          display_name: name,
          phone: pick(r, 'phone'),
          email: pick(r, 'email'),
        })
        if (!name)
          errors.push({
            field: 'display_name',
            code: 'required',
            message: 'Name is required',
            params: null,
          })
        if (errors.length === 0)
          apply.push(() => {
            db.persons.push({
              id: newId('person'),
              org_id: ORG,
              user_id: null,
              display_name: name,
              contact: {
                email: pick(r, 'email') || null,
                phone: pick(r, 'phone') || null,
                preferred_channel: null,
                address: null,
              },
              national_id_masked: null,
              national_id_country: null,
              gate_contact_phone: null,
              notes: null,
              join_status: 'no_account',
              directory_consent: [],
              created_at: nowIso(),
              updated_at: nowIso(),
              version: 1,
            })
          })
      } else {
        const door = pick(r, 'door_number')
        Object.assign(data, {
          door_number: door,
          owner_principal: pick(r, 'owner_principal'),
          tenant_principal: pick(r, 'tenant_principal'),
        })
        if (!door)
          errors.push({
            field: 'door_number',
            code: 'required',
            message: 'Door number is required',
            params: null,
          })
        else if (!db.units.some((u) => u.site_id === siteId && u.door_number === door))
          errors.push({
            field: 'door_number',
            code: 'unknown_unit',
            message: `No unit ${door} in this site`,
            params: null,
          })
        for (const f of [
          'owner_principal',
          'owner_late_fee',
          'tenant_principal',
          'tenant_late_fee',
        ]) {
          const v = pick(r, f)
          if (v && Number.isNaN(Number(v.replace(',', '.'))))
            errors.push({ field: f, code: 'invalid', message: 'Not an amount', params: null })
        }
      }
      rows.push({ row, status: errors.length ? 'error' : 'ok', errors, data })
    })
    if (dryRun) return HttpResponse.json(report(rows, true, null), { status: 200 })
    if (rows.some((r) => r.status === 'error'))
      return problem(422, 'validation_failed', 'Fix the rows with errors first.')
    const jobId = startJob(
      'import',
      () => {
        apply.forEach((f) => f())
        recomputeOccupancy(db)
      },
      { rows: rows.length },
    )
    return HttpResponse.json(report(rows, false, jobId), { status: 202 })
  }
}

function keyValuesFor(keyId: string, blockId: string | null) {
  const key = db.keys.find((k) => k.id === keyId)!
  const units = db.units.filter(
    (u) =>
      u.site_id === key.site_id &&
      u.status === 'active' &&
      u.parent_unit_id === null &&
      (!blockId || u.block_id === blockId),
  )
  const stored = db.keyValues[keyId] ?? {}
  const items = units.flatMap((u) => {
    if (key.kind === 'equal') return [{ unit_id: u.id, value: 1, computed: true }]
    if (key.kind === 'area') return [{ unit_id: u.id, value: u.area_m2 ?? 0, computed: true }]
    return u.id in stored ? [{ unit_id: u.id, value: stored[u.id]!, computed: false }] : []
  })
  return {
    items,
    total: items.reduce((s, i) => s + i.value, 0),
    complete: items.length === units.length,
    next_cursor: null,
  }
}

export const tenancyHandlers = [
  // sites
  http.get(o('sites'), ({ request }) => {
    const url = new URL(request.url)
    const status = url.searchParams.get('status')
    const q = url.searchParams.get('q')
    return HttpResponse.json(
      paginate(
        db.sites.filter((s) => (!status || s.status === status) && matches(q, s.name)),
        url,
      ),
    )
  }),
  http.post(o('sites'), async ({ request }) => {
    const body = (await request.json()) as {
      name: string
      timezone: string
      currency: string
      address?: never
      fiscal_year_start_month?: number
    }
    if (!body.name?.trim())
      return problem(422, 'validation_failed', undefined, [
        { field: 'name', code: 'required', message: 'Name is required' },
      ])
    if (db.sites.some((s) => s.name.toLowerCase() === body.name.trim().toLowerCase()))
      return duplicate('name', 'A site with this name already exists')
    const site = {
      id: newId('site'),
      org_id: ORG,
      name: body.name.trim(),
      address: body.address ?? {
        line1: '',
        line2: null,
        district: null,
        city: null,
        postal_code: null,
        country_code: 'TR',
      },
      timezone: body.timezone,
      currency: body.currency,
      fiscal_year_start_month: body.fiscal_year_start_month ?? 1,
      lock_date: null,
      status: 'active',
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    }
    db.sites.push(site as never)
    db.settings[site.id] = { ...db.settings[SITE]!, directory_enabled: false }
    db.lockDates[site.id] = null
    db.blocks.push({
      id: newId('block'),
      org_id: ORG,
      site_id: site.id,
      name: 'Main',
      is_default: true,
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    })
    for (const code of ['arsa_payi', 'equal', 'area']) {
      const kind = code === 'arsa_payi' ? 'share' : code
      db.keys.push({
        id: newId('key'),
        org_id: ORG,
        site_id: site.id,
        code,
        name: code,
        kind: kind as never,
        is_system: true,
        created_at: nowIso(),
        updated_at: nowIso(),
        version: 1,
      })
    }
    return HttpResponse.json(site, { status: 201 })
  }),
  http.get(o('sites/:siteId'), ({ params }) => {
    const s = db.sites.find((x) => x.id === params.siteId)
    return s ? HttpResponse.json(s) : notFound()
  }),
  http.patch(o('sites/:siteId'), async ({ params, request }) => {
    const s = db.sites.find((x) => x.id === params.siteId)
    if (!s) return notFound()
    const conflict = versionConflict(request, s.version)
    if (conflict) return conflict
    const body = (await request.json()) as Partial<typeof s>
    if (body.name !== undefined && !body.name.trim())
      return problem(422, 'validation_failed', undefined, [
        { field: 'name', code: 'required', message: 'Name is required' },
      ])
    Object.assign(s, body, { version: s.version + 1, updated_at: nowIso() })
    return HttpResponse.json(s)
  }),
  http.get(o('sites/:siteId/settings'), ({ params }) =>
    HttpResponse.json(db.settings[String(params.siteId)] ?? notFound()),
  ),
  http.patch(o('sites/:siteId/settings'), async ({ params, request }) => {
    const s = db.settings[String(params.siteId)]
    if (!s) return notFound()
    Object.assign(s, await request.json())
    return HttpResponse.json(s)
  }),
  http.get(o('sites/:siteId/bank-details'), ({ params }) => {
    const b = db.bank[String(params.siteId)]
    return b ? HttpResponse.json(b) : problem(404, 'not_found', 'No bank details yet')
  }),
  http.put(o('sites/:siteId/bank-details'), async ({ params, request }) => {
    const body = (await request.json()) as {
      iban: string
      holder: string
      bank_name?: string | null
      reference_hint?: string | null
    }
    const iban = body.iban.replace(/\s+/g, '').toUpperCase()
    if (!/^[A-Z]{2}\d{2}[A-Z0-9]{11,30}$/.test(iban))
      return problem(422, 'validation_failed', undefined, [
        { field: 'iban', code: 'invalid_iban', message: 'This IBAN is not valid' },
      ])
    const saved = {
      iban,
      holder: body.holder,
      bank_name: body.bank_name ?? null,
      reference_hint: body.reference_hint ?? null,
      suggested_reference: null,
    }
    db.bank[String(params.siteId)] = saved
    return HttpResponse.json(saved)
  }),

  // money.getLockDate / setLockDate live in the money module; the sites screen needs them
  http.get(o('sites/:siteId/lock-date'), ({ params }) =>
    HttpResponse.json({
      site_id: String(params.siteId),
      lock_date: db.lockDates[String(params.siteId)] ?? null,
      changed_by: null,
      changed_at: null,
    }),
  ),
  http.put(o('sites/:siteId/lock-date'), async ({ params, request }) => {
    const body = (await request.json()) as { lock_date: string | null }
    const current = db.lockDates[String(params.siteId)] ?? null
    // moving the date back is sensitive (web plan §7)
    if (current && (!body.lock_date || body.lock_date < current)) {
      const r = requireReauth()
      if (r) return r
    }
    db.lockDates[String(params.siteId)] = body.lock_date
    const s = db.sites.find((x) => x.id === params.siteId)
    if (s) s.lock_date = body.lock_date
    return HttpResponse.json({
      site_id: String(params.siteId),
      lock_date: body.lock_date,
      changed_by: null,
      changed_at: nowIso(),
    })
  }),

  // blocks
  http.get(o('blocks'), ({ request }) => {
    const url = new URL(request.url)
    const site = url.searchParams.get('site_id')
    return HttpResponse.json(
      paginate(
        db.blocks.filter((b) => !site || b.site_id === site),
        url,
      ),
    )
  }),
  http.post(o('blocks'), async ({ request }) => {
    const body = (await request.json()) as { site_id: string; name: string }
    if (
      db.blocks.some(
        (b) => b.site_id === body.site_id && b.name.toLowerCase() === body.name.toLowerCase(),
      )
    )
      return duplicate('name', 'A block with this name already exists')
    const b = {
      id: newId('block'),
      org_id: ORG,
      site_id: body.site_id,
      name: body.name,
      is_default: false,
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    }
    db.blocks.push(b)
    return HttpResponse.json(b, { status: 201 })
  }),
  http.patch(o('blocks/:blockId'), async ({ params, request }) => {
    const b = db.blocks.find((x) => x.id === params.blockId)
    if (!b) return notFound()
    Object.assign(b, await request.json(), { version: b.version + 1 })
    return HttpResponse.json(b)
  }),
  http.delete(o('blocks/:blockId'), ({ params }) => {
    if (db.units.some((u) => u.block_id === params.blockId))
      return problem(409, 'conflict', 'The block still has units.')
    db.blocks = db.blocks.filter((b) => b.id !== params.blockId)
    return new HttpResponse(null, { status: 204 })
  }),

  // units
  http.get(o('units'), ({ request }) => {
    const url = new URL(request.url)
    const p = url.searchParams
    recomputeOccupancy(db)
    const items = db.units.filter(
      (u) =>
        u.site_id === p.get('site_id') &&
        (!p.get('block_id') || u.block_id === p.get('block_id')) &&
        (!p.get('type') || u.type === p.get('type')) &&
        (!p.get('status') || u.status === p.get('status')) &&
        (!p.get('parent_unit_id') || u.parent_unit_id === p.get('parent_unit_id')) &&
        (p.get('vacant') === null || String(u.vacant) === p.get('vacant')) &&
        (!p.get('tag_id') || (db.tagMembers[p.get('tag_id')!] ?? []).includes(u.id)) &&
        matches(p.get('q'), u.door_number, u.legal_ref),
    )
    return HttpResponse.json(paginate(items, url))
  }),
  http.post(o('units'), async ({ request }) => {
    const body = (await request.json()) as {
      site_id: string
      block_id: string
      door_number: string
      type: string
      legal_ref?: string | null
      area_m2?: number | null
      parent_unit_id?: string | null
    }
    if (!body.door_number?.trim())
      return problem(422, 'validation_failed', undefined, [
        { field: 'door_number', code: 'required', message: 'Door number is required' },
      ])
    if (
      db.units.some((u) => u.site_id === body.site_id && u.door_number === body.door_number.trim())
    )
      return duplicate('door_number', 'This door number is already used in the site')
    const u = {
      id: newId('unit'),
      org_id: ORG,
      site_id: body.site_id,
      block_id: body.block_id,
      door_number: body.door_number.trim(),
      legal_ref: body.legal_ref ?? null,
      type: body.type,
      area_m2: body.area_m2 ?? null,
      parent_unit_id: body.parent_unit_id ?? null,
      status: 'active',
      vacant: true,
      occupancy: { owners: 0, tenants: 0, household: 0 },
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
      payer_overrides: (body as { payer_overrides?: object }).payer_overrides ?? {},
    }
    db.units.push(u as never)
    return HttpResponse.json(u, { status: 201 })
  }),
  http.get(o('units/:unitId'), ({ params }) => {
    recomputeOccupancy(db)
    const u = db.units.find((x) => x.id === params.unitId)
    return u ? HttpResponse.json(u) : notFound()
  }),
  http.patch(o('units/:unitId'), async ({ params, request }) => {
    const u = db.units.find((x) => x.id === params.unitId)
    if (!u) return notFound()
    const conflict = versionConflict(request, u.version)
    if (conflict) return conflict
    const body = (await request.json()) as Partial<typeof u>
    if (
      body.door_number &&
      db.units.some(
        (x) => x.id !== u.id && x.site_id === u.site_id && x.door_number === body.door_number,
      )
    )
      return duplicate('door_number', 'This door number is already used in the site')
    Object.assign(u, body, { version: u.version + 1, updated_at: nowIso() })
    return HttpResponse.json(u)
  }),
  http.delete(o('units/:unitId'), ({ params }) => {
    db.units = db.units.filter((u) => u.id !== params.unitId)
    return new HttpResponse(null, { status: 204 })
  }),

  // imports
  http.post(o('units\\:import'), importHandler('units')),
  http.post(o('persons\\:import'), importHandler('people')),
  http.post(o('opening-balances\\:import'), importHandler('opening')),

  // allocation keys
  http.get(o('allocation-keys'), ({ request }) => {
    const url = new URL(request.url)
    const kind = url.searchParams.get('kind')
    return HttpResponse.json(
      paginate(
        db.keys.filter((k) => !kind || k.kind === kind),
        url,
      ),
    )
  }),
  http.post(o('allocation-keys'), async ({ request }) => {
    const body = (await request.json()) as {
      site_id: string
      code: string
      name: string
      kind: string
    }
    if (db.keys.some((k) => k.site_id === body.site_id && k.code === body.code))
      return duplicate('code', 'This code is already used')
    const k = {
      id: newId('key'),
      org_id: ORG,
      ...body,
      is_system: false,
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    }
    db.keys.push(k as never)
    return HttpResponse.json(k, { status: 201 })
  }),
  http.patch(o('allocation-keys/:keyId'), async ({ params, request }) => {
    const k = db.keys.find((x) => x.id === params.keyId)
    if (!k) return notFound()
    if (k.is_system) return problem(409, 'conflict', 'System keys cannot be changed.')
    Object.assign(k, await request.json(), { version: k.version + 1 })
    return HttpResponse.json(k)
  }),
  http.delete(o('allocation-keys/:keyId'), ({ params }) => {
    const k = db.keys.find((x) => x.id === params.keyId)
    if (k?.is_system) return problem(409, 'conflict', 'System keys cannot be deleted.')
    db.keys = db.keys.filter((x) => x.id !== params.keyId)
    return new HttpResponse(null, { status: 204 })
  }),
  http.get(o('allocation-keys/:keyId/values'), ({ params, request }) =>
    HttpResponse.json(
      keyValuesFor(String(params.keyId), new URL(request.url).searchParams.get('block_id')),
    ),
  ),
  http.put(o('allocation-keys/:keyId/values'), async ({ params, request }) => {
    const key = db.keys.find((k) => k.id === params.keyId)
    if (!key) return notFound()
    if (key.kind === 'equal' || key.kind === 'area')
      return problem(409, 'conflict', 'This key is computed; it has no stored values.')
    const body = (await request.json()) as { items: { unit_id: string; value: number }[] }
    const errors = body.items.flatMap((it, i) =>
      !(it.value >= 0)
        ? [{ field: `items.${i}.value`, code: 'invalid', message: 'Must be zero or more' }]
        : [],
    )
    if (errors.length) return problem(422, 'validation_failed', undefined, errors)
    db.keyValues[key.id] = Object.fromEntries(body.items.map((i) => [i.unit_id, i.value]))
    return HttpResponse.json({
      count: body.items.length,
      total: body.items.reduce((s, i) => s + i.value, 0),
    })
  }),
  http.post(o('allocation-keys/:keyId/values\\:import'), async ({ params, request }) => {
    const dryRun = new URL(request.url).searchParams.get('dry_run') === 'true'
    const { rest, pick } = await readImport(request)
    const key = db.keys.find((k) => k.id === params.keyId)
    if (!key) return notFound()
    const rows: ImportRow[] = rest.map((r, i) => {
      const door = pick(r, 'door_number')
      const raw = pick(r, 'value')
      const errors: ImportRow['errors'] = []
      if (!db.units.some((u) => u.site_id === key.site_id && u.door_number === door))
        errors.push({
          field: 'door_number',
          code: 'unknown_unit',
          message: `No unit ${door}`,
          params: null,
        })
      if (raw === '' || Number.isNaN(Number(raw.replace(',', '.'))))
        errors.push({ field: 'value', code: 'invalid', message: 'Not a number', params: null })
      return {
        row: i + 1,
        status: errors.length ? 'error' : 'ok',
        errors,
        data: { door_number: door, value: raw },
      }
    })
    if (!dryRun && !rows.some((r) => r.status === 'error')) {
      const values: Record<string, number> = { ...(db.keyValues[key.id] ?? {}) }
      for (const r of rows) {
        const u = db.units.find(
          (x) => x.site_id === key.site_id && x.door_number === r.data.door_number,
        )
        if (u) values[u.id] = Number(String(r.data.value).replace(',', '.'))
      }
      db.keyValues[key.id] = values
    }
    return HttpResponse.json(report(rows, dryRun, null), { status: dryRun ? 200 : 202 })
  }),

  // unit tags
  http.get(o('unit-tags'), ({ request }) =>
    HttpResponse.json(paginate(db.tags, new URL(request.url))),
  ),
  http.post(o('unit-tags'), async ({ request }) => {
    const body = (await request.json()) as { site_id: string; code: string; name: string }
    const t = {
      id: newId('tag'),
      org_id: ORG,
      ...body,
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    }
    db.tags.push(t)
    db.tagMembers[t.id] = []
    return HttpResponse.json(t, { status: 201 })
  }),
  http.delete(o('unit-tags/:tagId'), ({ params }) => {
    db.tags = db.tags.filter((t) => t.id !== params.tagId)
    return new HttpResponse(null, { status: 204 })
  }),
  http.get(o('unit-tags/:tagId/units'), ({ params }) =>
    HttpResponse.json({ unit_ids: db.tagMembers[String(params.tagId)] ?? [] }),
  ),
  http.put(o('unit-tags/:tagId/units'), async ({ params, request }) => {
    const body = (await request.json()) as { unit_ids: string[] }
    db.tagMembers[String(params.tagId)] = body.unit_ids
    return HttpResponse.json(body)
  }),

  // vehicles
  http.get(o('vehicles'), ({ request }) => {
    const url = new URL(request.url)
    const p = url.searchParams
    return HttpResponse.json(
      paginate(
        db.vehicles.filter(
          (v) =>
            (!p.get('site_id') || v.site_id === p.get('site_id')) &&
            (!p.get('unit_id') || v.unit_id === p.get('unit_id')) &&
            (p.get('archived') === null || String(v.archived) === p.get('archived')) &&
            matches(p.get('q'), v.plate, v.plate_normalized),
        ),
        url,
      ),
    )
  }),
  http.get(
    o('vehicles\\:export'),
    () =>
      new HttpResponse('plate,unit\r\n34ABC123,A-1\r\n', {
        headers: { 'Content-Type': 'text/csv' },
      }),
  ),
  http.post(o('vehicles'), async ({ request }) => {
    const body = (await request.json()) as {
      site_id: string
      unit_id: string
      plate: string
      description?: string | null
    }
    const normalized = body.plate.toUpperCase().replace(/[^A-Z0-9]/g, '')
    if (
      db.vehicles.some(
        (v) => !v.archived && v.plate_normalized === normalized && v.site_id === body.site_id,
      )
    )
      return duplicate('plate', 'This plate is already registered in the site')
    const v = {
      id: newId('veh'),
      org_id: ORG,
      site_id: body.site_id,
      unit_id: body.unit_id,
      plate: body.plate,
      plate_normalized: normalized,
      description: body.description ?? null,
      archived: false,
      warning: normalized ? null : 'Nothing remains after normalising; the plate is not projected.',
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    }
    db.vehicles.push(v)
    return HttpResponse.json(v, { status: 201 })
  }),
  http.patch(o('vehicles/:vehicleId'), async ({ params, request }) => {
    const v = db.vehicles.find((x) => x.id === params.vehicleId)
    if (!v) return notFound()
    const body = (await request.json()) as { plate?: string }
    Object.assign(
      v,
      body,
      body.plate ? { plate_normalized: body.plate.toUpperCase().replace(/[^A-Z0-9]/g, '') } : {},
      { version: v.version + 1 },
    )
    return HttpResponse.json(v)
  }),
  http.delete(o('vehicles/:vehicleId'), ({ params }) => {
    db.vehicles = db.vehicles.filter((v) => v.id !== params.vehicleId)
    return new HttpResponse(null, { status: 204 })
  }),

  // site transfers
  http.post(o('sites/:siteId\\:export'), ({ params }) => {
    const r = requireReauth()
    if (r) return r
    const jobId = startJob('site_export', () => {})
    const t = {
      id: newId('transfer'),
      org_id: ORG,
      direction: 'export',
      site_id: String(params.siteId),
      archive_file_id: null,
      source_org_id: ORG,
      target_org_id: null,
      status: 'queued',
      job_id: jobId,
      at: nowIso(),
      created_at: nowIso(),
      updated_at: nowIso(),
    }
    db.transfers.push(t as never)
    return HttpResponse.json(t, { status: 202 })
  }),
  http.post(o('sites/:siteId\\:offboard'), async ({ params, request }) => {
    const r = requireReauth()
    if (r) return r
    const site = db.sites.find((s) => s.id === params.siteId)
    if (!site) return notFound()
    const body = (await request.json()) as { confirm_site_name: string }
    if (body.confirm_site_name !== site.name)
      return problem(422, 'validation_failed', undefined, [
        { field: 'confirm_site_name', code: 'mismatch', message: 'Type the exact site name' },
      ])
    const jobId = startJob('site_export', () => {
      site.status = 'offboarded'
    })
    const t = {
      id: newId('transfer'),
      org_id: ORG,
      direction: 'offboard',
      site_id: site.id,
      archive_file_id: null,
      source_org_id: ORG,
      target_org_id: null,
      status: 'queued',
      job_id: jobId,
      at: nowIso(),
      created_at: nowIso(),
      updated_at: nowIso(),
    }
    db.transfers.push(t as never)
    return HttpResponse.json(t, { status: 202 })
  }),
  http.post(o('sites\\:import'), () => {
    const r = requireReauth()
    if (r) return r
    const jobId = startJob('site_import', () => {})
    const t = {
      id: newId('transfer'),
      org_id: ORG,
      direction: 'import',
      site_id: null,
      archive_file_id: null,
      source_org_id: null,
      target_org_id: ORG,
      status: 'queued',
      job_id: jobId,
      at: nowIso(),
      created_at: nowIso(),
      updated_at: nowIso(),
    }
    db.transfers.push(t as never)
    return HttpResponse.json(t, { status: 202 })
  }),
  http.get(o('site-transfers'), ({ request }) =>
    HttpResponse.json(paginate([...db.transfers].reverse(), new URL(request.url))),
  ),

  // jobs (operations.getJob): queued, then running, then succeeded on the third poll
  http.get(o('jobs/:jobId'), ({ params }) => {
    const j = db.jobs[String(params.jobId)]
    if (!j) return notFound()
    j.polls++
    if (j.polls === 1)
      Object.assign(j.job, { status: 'running', progress: 0.4, started_at: nowIso() })
    else if (j.polls >= 3 && j.job.status !== 'succeeded') {
      j.onDone?.()
      Object.assign(j.job, { status: 'succeeded', progress: 1, finished_at: nowIso() })
    }
    return HttpResponse.json(j.job)
  }),

  // dashboards.getPortfolioDashboard
  http.get(o('dashboard/portfolio'), () =>
    HttpResponse.json({
      as_of: nowIso(),
      sites: db.sites.map((s, i) => ({
        site_id: s.id,
        name: s.name,
        overdue: { amount: 12_500_00 + i * 100, currency: s.currency },
        overdue_units: 7 - i,
        cash: { amount: 3_200_00, currency: s.currency },
        bank: { amount: 84_000_00, currency: s.currency },
        open_tickets: 4 + i,
        collection_rate_pct: 91.5,
      })),
      totals: {
        overdue: { amount: 12_500_00, currency: 'TRY' },
        cash: { amount: 3_200_00, currency: 'TRY' },
        bank: { amount: 84_000_00, currency: 'TRY' },
        open_tickets: 4,
      },
    }),
  ),

  // auth.reauth / auth.requestOtp
  http.post('*/v1/auth/reauth', async ({ request }) => {
    const body = (await request.json()) as { method: string; password?: string; code?: string }
    const ok = body.method === 'password' ? body.password === db.password : body.code === '123456'
    if (!ok) return problem(401, 'invalid_grant', 'Wrong password or code.')
    db.reauthUntil = Date.now() + 10 * 60_000
    return HttpResponse.json({ reauth_until: new Date(db.reauthUntil).toISOString() })
  }),
  http.post('*/v1/auth/otp', () => new HttpResponse(null, { status: 202 })),
]
