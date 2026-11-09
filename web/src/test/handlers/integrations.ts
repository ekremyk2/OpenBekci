import { http, HttpResponse } from 'msw'
import type { Schema } from '@/api/draft'
import { registerReset } from '../reset'
import { db as tenancyDb } from './db'
import { newId, nowIso, paginate, problem, versionConflict } from './http'
import { requireReauth } from './tenancy'

/*
 * In-memory model of the packs, API keys and webhooks modules (draft contract 0.4), written from
 * the spec (api/src/paths/packs, api/src/paths/integrations) and docs/integrations.md. Serves
 * Vitest and `pnpm dev:mock`. The `:verb` operations are matched with regular expressions because
 * MSW's path syntax cannot express them.
 */

type Pack = Schema<'Pack'>
type ApiKey = Schema<'ApiKey'>
type Endpoint = Schema<'WebhookEndpoint'>
type Delivery = Schema<'WebhookDelivery'>

const ORG = 'org-1'
const SITE = 'site-a'
const T0 = '2026-10-01T09:00:00Z'

/* ───────────── packs ───────────── */

const TR_ORG_SCHEMA = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  type: 'object',
  additionalProperties: false,
  required: ['tr_sms_provider', 'tr_sms_api_key'],
  properties: {
    tr_sms_provider: {
      type: 'string',
      enum: ['netgsm', 'iletimerkezi'],
      'x-ui': { order: 1, group: 'sms', widget: 'select', label: 'tr.label.sms_provider' },
    },
    tr_sms_api_key: {
      type: 'string',
      minLength: 8,
      'x-secret': true,
      'x-ui': { order: 2, group: 'sms', widget: 'password', help: 'tr.help.sms_api_key' },
    },
    tr_sms_sender: {
      type: 'string',
      maxLength: 11,
      'x-ui': { order: 3, group: 'sms', help: 'tr.help.sms_sender' },
    },
    tr_bank_ibans: {
      type: 'array',
      items: { type: 'string', pattern: '^TR[0-9]{24}$' },
      maxItems: 5,
      'x-ui': { order: 4, group: 'bank' },
    },
    tr_collect_by_card: { type: 'boolean', default: false, 'x-ui': { order: 5, group: 'bank' } },
    tr_late_fee: {
      type: 'object',
      title: 'Late fee',
      'x-ui': { order: 6 },
      properties: {
        monthly_rate_percent: { type: 'number', minimum: 0, maximum: 5, default: 5 },
        grace_days: { type: 'integer', minimum: 0 },
      },
    },
  },
  'x-ui': {
    groups: [
      { id: 'sms', title: 'tr.group.sms', order: 1 },
      { id: 'bank', title: 'tr.group.bank', order: 2 },
    ],
  },
}

const TR_SITE_SCHEMA = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  type: 'object',
  additionalProperties: false,
  properties: {
    tr_bank_account_label: { type: 'string', maxLength: 40, 'x-ui': { order: 1 } },
    tr_send_receipts: { type: 'boolean', default: true, 'x-ui': { order: 2 } },
  },
}

const EXAMPLE_ORG_SCHEMA = {
  type: 'object',
  properties: {
    example_endpoint: { type: 'string', format: 'uri', 'x-ui': { order: 1 } },
  },
}

const PACK_LABELS: Record<string, { en: string; tr: string }> = {
  'tr.label.sms_provider': { en: 'SMS provider', tr: 'SMS sağlayıcı' },
  'tr.help.sms_api_key': {
    en: 'The API key from your provider console. It is stored encrypted and never shown again.',
    tr: 'Sağlayıcı panelinizdeki API anahtarı. Şifreli saklanır ve bir daha gösterilmez.',
  },
  'tr.help.sms_sender': {
    en: 'Up to 11 characters, registered with the provider.',
    tr: 'En fazla 11 karakter, sağlayıcıda kayıtlı olmalı.',
  },
  'tr.group.sms': { en: 'SMS', tr: 'SMS' },
  'tr.group.bank': { en: 'Bank and card', tr: 'Banka ve kart' },
}

/** The labels of the packs for the `codes` answer (merged into the core catalog, plan 04 §3.9). */
export function packLabels(locale: string) {
  return Object.entries(PACK_LABELS).map(([code, l]) => ({
    code,
    label: locale === 'tr' ? l.tr : l.en,
  }))
}

interface StoredPack {
  id: string
  version: string
  countries: string[]
  provides: string[]
  orgSchema: object | null
  siteSchema: object | null
  enabled: boolean
  enabled_at: string | null
  settings: Record<string, unknown>
  secrets: Record<string, string>
  sites: Record<string, { values: Record<string, unknown>; secrets: Record<string, string> }>
}

function seedPacks(): StoredPack[] {
  return [
    {
      id: 'tr',
      version: '0.4.0',
      countries: ['TR'],
      provides: [
        'preset:quorum',
        'preset:late_fee',
        'sms_provider:netgsm',
        'importer:ziraat',
        'consent:kvkk',
      ],
      orgSchema: TR_ORG_SCHEMA,
      siteSchema: TR_SITE_SCHEMA,
      enabled: false,
      enabled_at: null,
      settings: {},
      secrets: {},
      sites: {},
    },
    {
      id: 'example',
      version: '0.4.0',
      countries: ['XX'],
      provides: ['labels:example'],
      orgSchema: EXAMPLE_ORG_SCHEMA,
      siteSchema: null,
      enabled: false,
      enabled_at: null,
      settings: {},
      secrets: {},
      sites: {},
    },
  ]
}

interface State {
  packs: StoredPack[]
  keys: ApiKey[]
  endpoints: Endpoint[]
  secrets: Record<string, string>
  deliveries: Delivery[]
  /** `ok` makes the ping succeed; `down` fails it (a mock switch for tests). */
  receiver: 'ok' | 'down'
}

function seed(): State {
  const keys: ApiKey[] = [
    {
      id: 'key-1',
      org_id: ORG,
      name: 'Accounting export',
      prefix: 'k3x9q7ab',
      scopes: ['payments.record', 'reports.read'],
      site_id: null,
      status: 'active',
      created_by: 'u1',
      created_at: T0,
      rotated_at: null,
      last_used_at: '2026-11-08T10:15:00Z',
      expires_at: '2027-06-30T00:00:00Z',
      revoked_at: null,
    },
    {
      id: 'key-2',
      org_id: ORG,
      name: 'Old dashboard',
      prefix: 'zz11aa22',
      scopes: ['tickets.read'],
      site_id: SITE,
      status: 'revoked',
      created_by: 'u1',
      created_at: T0,
      rotated_at: null,
      last_used_at: null,
      expires_at: null,
      revoked_at: '2026-10-20T08:00:00Z',
    },
  ]
  const endpoints: Endpoint[] = [
    {
      id: 'wh-1',
      org_id: ORG,
      url: 'https://hooks.example.org/openbekci',
      events: ['payment.confirmed', 'ticket.created'],
      active: true,
      description: 'Accounting',
      failing: false,
      failing_since: null,
      consecutive_failures: 0,
      created_by: 'u1',
      created_at: T0,
      updated_at: T0,
      version: 1,
    },
  ]
  const deliveries: Delivery[] = [
    {
      id: 'dl-1',
      endpoint_id: 'wh-1',
      event_id: 'ev-1',
      event_type: 'ticket.created',
      status: 'succeeded',
      attempts: 1,
      response_code: 204,
      last_error: null,
      next_retry_at: null,
      created_at: '2026-11-08T09:00:00Z',
      delivered_at: '2026-11-08T09:00:01Z',
    },
    {
      id: 'dl-2',
      endpoint_id: 'wh-1',
      event_id: 'ev-2',
      event_type: 'payment.confirmed',
      status: 'pending',
      attempts: 2,
      response_code: 500,
      last_error: 'http_status: 500',
      next_retry_at: '2026-11-09T12:02:00Z',
      created_at: '2026-11-09T12:00:00Z',
      delivered_at: null,
    },
  ]
  return {
    packs: seedPacks(),
    keys,
    endpoints,
    secrets: {},
    deliveries,
    receiver: 'ok',
  }
}

export let state: State = seed()
registerReset(() => {
  state = seed()
})

function packView(p: StoredPack): Pack {
  return {
    id: p.id,
    version: p.version,
    min_sdk: '1.0.0',
    countries: p.countries,
    provides: p.provides,
    enabled: p.enabled,
    enabled_at: p.enabled_at,
    settings_schemas: {
      ...(p.orgSchema ? { org: p.orgSchema } : {}),
      ...(p.siteSchema ? { site: p.siteSchema } : {}),
    },
    settings: p.settings,
    secrets_set: Object.keys(p.secrets),
    sites: Object.entries(p.sites).map(([site_id, s]) => ({
      site_id,
      values: s.values,
      secrets_set: Object.keys(s.secrets),
    })),
  }
}

type Schemaish = {
  required?: string[]
  properties?: Record<string, Record<string, unknown>>
}

/** A small validator for the mock schemas: required, maxLength, minLength, pattern, enum. */
function validate(
  schema: Schemaish | null,
  values: Record<string, unknown>,
  secrets: Record<string, string>,
  storedSecrets: Record<string, string>,
) {
  const errors: { field: string; code: string; message?: string }[] = []
  if (!schema) return errors
  for (const name of schema.required ?? []) {
    const isSecret = schema.properties?.[name]?.['x-secret'] === true
    const present = isSecret ? name in secrets || name in storedSecrets : values[name] !== undefined
    if (!present) errors.push({ field: 'settings', code: 'required' })
  }
  // a rule only the server knows (a live check against the provider)
  if (values.tr_sms_sender === 'TAKEN')
    errors.push({ field: 'settings.tr_sms_sender', code: 'invalid_value' })
  for (const [name, def] of Object.entries(schema.properties ?? {})) {
    const v = def['x-secret'] === true ? secrets[name] : values[name]
    if (typeof v === 'string') {
      if (typeof def.maxLength === 'number' && v.length > def.maxLength)
        errors.push({ field: `settings.${name}`, code: 'invalid_value' })
      else if (typeof def.minLength === 'number' && v.length < def.minLength)
        errors.push({ field: `settings.${name}`, code: 'invalid_value' })
      else if (Array.isArray(def.enum) && !def.enum.includes(v))
        errors.push({ field: `settings.${name}`, code: 'invalid_value' })
    }
    if (Array.isArray(v) && def.items) {
      const pattern = (def.items as { pattern?: string }).pattern
      v.forEach((row, i) => {
        if (pattern && typeof row === 'string' && !new RegExp(pattern).test(row))
          errors.push({ field: `settings.${name}.${i}`, code: 'invalid_value' })
      })
    }
  }
  return errors
}

/** Splits a settings document into its secret and non-secret fields by the schema's `x-secret`. */
function split(schema: Schemaish | null, doc: Record<string, unknown>) {
  const values: Record<string, unknown> = {}
  const secrets: Record<string, string | null> = {}
  for (const [k, v] of Object.entries(doc)) {
    if (schema?.properties?.[k]?.['x-secret'] === true) secrets[k] = v as string | null
    else values[k] = v
  }
  return { values, secrets }
}

function applySecrets(target: Record<string, string>, incoming: Record<string, string | null>) {
  for (const [k, v] of Object.entries(incoming)) {
    if (v === null) delete target[k]
    else target[k] = v
  }
}

/** Presets the enabled pack writes into the core site settings ("pack default" markers). */
function applyPresets(pack: StoredPack) {
  if (pack.id !== 'tr') return
  for (const site of tenancyDb.sites) {
    const s = tenancyDb.settings[site.id] as unknown as Record<string, unknown> | undefined
    if (!s) continue
    s.legal_preset = 'tr'
    s.pack_defaults = {
      late_fee_policy: { pack: 'tr', value: { monthly_rate_percent: 5, grace_days: 0 } },
      quorum: { pack: 'tr', value: { first_call: 'majority_of_units', second_call: 'none' } },
      ...((s.pack_defaults as object | undefined) ?? {}),
    }
  }
}

const packPath = /\/v1\/orgs\/[^/]+\/packs\/([^/]+)\/(enable|disable|settings)$/
const readJson = async (request: Request) =>
  (await request.json().catch(() => ({}))) as Record<string, unknown>

/* ───────────── API keys ───────────── */

const SECRET_ALPHABET = 'abcdefghijklmnopqrstuvwxyz0123456789'
function randomToken(n: number) {
  let out = ''
  for (let i = 0; i < n; i++)
    out += SECRET_ALPHABET[Math.floor(Math.random() * SECRET_ALPHABET.length)]
  return out
}

const keyOp = /\/v1\/orgs\/[^/]+\/api-keys\/([^/:]+):(rotate|revoke)$/
const endpointOp = /\/v1\/orgs\/[^/]+\/webhook-endpoints\/([^/:]+):(test|rotate-secret)$/

const KNOWN_PERMISSIONS = new Set([
  'payments.record',
  'reports.read',
  'tickets.read',
  'tickets.manage',
  'money.read',
  'units.read',
  'people.read',
  'announcements.read',
])

/* ───────────── webhooks ───────────── */

const WEBHOOK_EVENTS = new Set([
  'payment.recorded',
  'payment.confirmed',
  'charge.created',
  'charges.generated',
  'ticket.created',
  'ticket.status_changed',
  'announcement.published',
  'meeting.called',
  'decision.applied',
  'visitor.approved',
  'delivery.received',
])

function validateEndpoint(
  body: { url?: string; events?: string[]; description?: string | null },
  partial: boolean,
) {
  const errors: { field: string; code: string; message?: string }[] = []
  if (!partial || body.url !== undefined) {
    if (!body.url || !/^https:\/\/[^/\s]+/.test(body.url)) {
      errors.push({ field: 'url', code: 'invalid_format', message: 'The address must be https://' })
    } else if (/^https:\/\/(localhost|127\.|10\.|192\.168\.)/.test(body.url)) {
      errors.push({
        field: 'url',
        code: 'forbidden_target',
        message: 'This address is not reachable from the internet.',
      })
    }
  }
  if (!partial || body.events !== undefined) {
    if (!body.events || body.events.length === 0) errors.push({ field: 'events', code: 'required' })
    else
      body.events.forEach((e, i) => {
        if (!WEBHOOK_EVENTS.has(e)) errors.push({ field: `events[${i}]`, code: 'invalid_value' })
      })
  }
  return errors
}

function pingDelivery(endpointId: string): Delivery {
  const t = nowIso()
  const ok = state.receiver === 'ok'
  const d: Delivery = {
    id: newId('dl'),
    endpoint_id: endpointId,
    event_id: newId('ev'),
    event_type: 'ping',
    status: ok ? 'succeeded' : 'pending',
    attempts: 1,
    response_code: ok ? 204 : 500,
    last_error: ok ? null : 'http_status: 500',
    next_retry_at: ok ? null : new Date(Date.now() + 30_000).toISOString(),
    created_at: t,
    delivered_at: ok ? t : null,
  }
  state.deliveries.unshift(d)
  return d
}

const O = '*/v1/orgs/:orgId'

export const integrationsHandlers = [
  /* packs */
  http.get(`${O}/packs`, () => HttpResponse.json({ items: state.packs.map(packView) })),
  http.post(packPath, async ({ request }) => {
    const [, id, verb] = packPath.exec(new URL(request.url).pathname)!
    const pack = state.packs.find((p) => p.id === id)
    if (!pack) return problem(404, 'not_found')
    if (verb === 'disable') {
      pack.enabled = false
      return HttpResponse.json(packView(pack))
    }
    if (verb !== 'enable') return problem(404, 'not_found')
    const body = await readJson(request)
    const doc = (body.settings as Record<string, unknown> | undefined) ?? pack.settings
    const { values, secrets } = split(pack.orgSchema as Schemaish, doc)
    const secretValues: Record<string, string> = {}
    for (const [k, v] of Object.entries(secrets)) if (v) secretValues[k] = v
    const errors = validate(pack.orgSchema as Schemaish, values, secretValues, pack.secrets)
    if (errors.length > 0)
      return problem(422, 'validation_failed', 'The settings are not valid.', errors)
    pack.settings = values
    applySecrets(pack.secrets, secrets)
    pack.enabled = true
    pack.enabled_at ??= nowIso()
    applyPresets(pack)
    return HttpResponse.json(packView(pack))
  }),
  http.put(packPath, async ({ request }) => {
    const [, id] = packPath.exec(new URL(request.url).pathname)!
    const pack = state.packs.find((p) => p.id === id)
    if (!pack) return problem(404, 'not_found')
    if (!pack.enabled) return problem(409, 'conflict', 'The pack is not enabled.')
    const body = (await readJson(request)) as {
      scope: 'org' | 'site'
      site_id?: string
      values: Record<string, unknown>
    }
    const schema = (body.scope === 'site' ? pack.siteSchema : pack.orgSchema) as Schemaish | null
    const { values, secrets } = split(schema, body.values)
    const secretValues: Record<string, string> = {}
    for (const [k, v] of Object.entries(secrets)) if (v) secretValues[k] = v
    if (body.scope === 'site') {
      if (!body.site_id)
        return problem(422, 'validation_failed', undefined, [
          { field: 'site_id', code: 'required' },
        ])
      const entry = (pack.sites[body.site_id] ??= { values: {}, secrets: {} })
      const errors = validate(schema, values, secretValues, entry.secrets)
      if (errors.length > 0)
        return problem(422, 'validation_failed', 'The settings are not valid.', errors)
      entry.values = values
      applySecrets(entry.secrets, secrets)
      return HttpResponse.json({
        scope: 'site',
        site_id: body.site_id,
        values: entry.values,
        secrets_set: Object.keys(entry.secrets),
      })
    }
    const errors = validate(schema, values, secretValues, pack.secrets)
    if (errors.length > 0)
      return problem(422, 'validation_failed', 'The settings are not valid.', errors)
    pack.settings = values
    applySecrets(pack.secrets, secrets)
    return HttpResponse.json({
      scope: 'org',
      site_id: null,
      values: pack.settings,
      secrets_set: Object.keys(pack.secrets),
    })
  }),

  /* API keys */
  http.get(`${O}/api-keys`, ({ request }) => {
    const url = new URL(request.url)
    const status = url.searchParams.get('status')
    return HttpResponse.json(
      paginate(
        state.keys.filter((k) => !status || k.status === status),
        url,
      ),
    )
  }),
  http.post(`${O}/api-keys`, async ({ request }) => {
    const reauth = requireReauth()
    if (reauth) return reauth
    const body = (await readJson(request)) as {
      name?: string
      scopes?: string[]
      site_id?: string | null
      expires_at?: string | null
    }
    const errors: { field: string; code: string; message?: string }[] = []
    if (!body.name?.trim()) errors.push({ field: 'name', code: 'required' })
    if (!body.scopes?.length) errors.push({ field: 'scopes', code: 'required' })
    body.scopes?.forEach((s, i) => {
      if (/^(apikeys|webhooks\.manage)/.test(s))
        errors.push({ field: `scopes[${i}]`, code: 'not_allowed' })
      else if (!KNOWN_PERMISSIONS.has(s)) errors.push({ field: `scopes[${i}]`, code: 'not_held' })
    })
    if (body.expires_at && Date.parse(body.expires_at) <= Date.now())
      errors.push({ field: 'expires_at', code: 'invalid_value', message: 'Must be in the future' })
    if (errors.length > 0)
      return problem(422, 'validation_failed', 'The request has invalid fields.', errors)
    const prefix = randomToken(8)
    const key: ApiKey = {
      id: newId('key'),
      org_id: ORG,
      name: body.name!.trim(),
      prefix,
      scopes: body.scopes!,
      site_id: body.site_id ?? null,
      status: 'active',
      created_by: 'u1',
      created_at: nowIso(),
      rotated_at: null,
      last_used_at: null,
      expires_at: body.expires_at ?? null,
      revoked_at: null,
    }
    state.keys.unshift(key)
    return HttpResponse.json({ key, secret: `obk_${prefix}_${randomToken(32)}` }, { status: 201 })
  }),
  http.post(keyOp, ({ request }) => {
    const [, id, verb] = keyOp.exec(new URL(request.url).pathname)!
    const reauth = requireReauth()
    if (reauth) return reauth
    const key = state.keys.find((k) => k.id === id)
    if (!key) return problem(404, 'not_found')
    if (verb === 'revoke') {
      key.status = 'revoked'
      key.revoked_at = nowIso()
      return HttpResponse.json(key)
    }
    if (key.status === 'revoked') return problem(409, 'conflict', 'The key is revoked.')
    key.prefix = randomToken(8)
    key.rotated_at = nowIso()
    key.status = 'active'
    return HttpResponse.json({ key, secret: `obk_${key.prefix}_${randomToken(32)}` })
  }),

  /* webhooks */
  http.get(`${O}/webhook-endpoints`, ({ request }) =>
    HttpResponse.json(paginate(state.endpoints, new URL(request.url))),
  ),
  http.post(`${O}/webhook-endpoints`, async ({ request }) => {
    const body = (await readJson(request)) as {
      url: string
      events: string[]
      description?: string | null
      active?: boolean
    }
    if (state.endpoints.length >= 20) return problem(409, 'conflict', 'Too many endpoints.')
    const errors = validateEndpoint(body, false)
    if (errors.length > 0)
      return problem(422, 'validation_failed', 'The request has invalid fields.', errors)
    const t = nowIso()
    const endpoint: Endpoint = {
      id: newId('wh'),
      org_id: ORG,
      url: body.url,
      events: body.events,
      active: body.active ?? true,
      description: body.description ?? null,
      failing: false,
      failing_since: null,
      consecutive_failures: 0,
      created_by: 'u1',
      created_at: t,
      updated_at: t,
      version: 1,
    }
    state.endpoints.push(endpoint)
    const secret = `whsec_${randomToken(32)}`
    state.secrets[endpoint.id] = secret
    return HttpResponse.json({ endpoint, secret }, { status: 201 })
  }),
  http.get(`${O}/webhook-endpoints/:endpointId`, ({ params }) => {
    const e = state.endpoints.find((x) => x.id === params.endpointId)
    return e ? HttpResponse.json(e) : problem(404, 'not_found')
  }),
  http.patch(`${O}/webhook-endpoints/:endpointId`, async ({ params, request }) => {
    const e = state.endpoints.find((x) => x.id === params.endpointId)
    if (!e) return problem(404, 'not_found')
    const conflict = versionConflict(request, e.version)
    if (conflict) return conflict
    const body = (await readJson(request)) as Partial<Endpoint>
    const errors = validateEndpoint(body as { url?: string; events?: string[] }, true)
    if (errors.length > 0)
      return problem(422, 'validation_failed', 'The request has invalid fields.', errors)
    if (body.url !== undefined && body.url !== e.url) {
      e.failing = false
      e.failing_since = null
      e.consecutive_failures = 0
    }
    if (body.active === true && !e.active) {
      e.failing = false
      e.failing_since = null
    }
    Object.assign(e, {
      ...(body.url !== undefined ? { url: body.url } : {}),
      ...(body.events !== undefined ? { events: body.events } : {}),
      ...(body.description !== undefined ? { description: body.description } : {}),
      ...(body.active !== undefined ? { active: body.active } : {}),
      updated_at: nowIso(),
      version: e.version + 1,
    })
    return HttpResponse.json(e)
  }),
  http.delete(`${O}/webhook-endpoints/:endpointId`, ({ params }) => {
    const i = state.endpoints.findIndex((x) => x.id === params.endpointId)
    if (i < 0) return problem(404, 'not_found')
    state.endpoints.splice(i, 1)
    state.deliveries = state.deliveries.filter((d) => d.endpoint_id !== params.endpointId)
    return new HttpResponse(null, { status: 204 })
  }),
  http.post(endpointOp, ({ request }) => {
    const [, id, verb] = endpointOp.exec(new URL(request.url).pathname)!
    const e = state.endpoints.find((x) => x.id === id)
    if (!e) return problem(404, 'not_found')
    if (verb === 'test') return HttpResponse.json(pingDelivery(e.id))
    const secret = `whsec_${randomToken(32)}`
    state.secrets[e.id] = secret
    return HttpResponse.json({
      endpoint: e,
      secret,
      previous_secret_expires_at: new Date(Date.now() + 24 * 3600_000).toISOString(),
    })
  }),
  http.get(`${O}/webhook-endpoints/:endpointId/deliveries`, ({ params, request }) => {
    const url = new URL(request.url)
    const status = url.searchParams.get('status')
    const items = state.deliveries
      .filter((d) => d.endpoint_id === params.endpointId && (!status || d.status === status))
      .sort((a, b) => b.created_at.localeCompare(a.created_at))
    return HttpResponse.json(paginate(items, url))
  }),
]
