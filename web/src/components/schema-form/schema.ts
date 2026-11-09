/*
 * Pure helpers of the schema-driven form (web plan §12). Supported subset of JSON Schema 2020-12:
 *   - `type: object` (nested, rendered as a fieldset) with `properties` and `required`
 *   - `type: string` (`minLength`, `maxLength`, `pattern`, `format: uri | email`), `number`,
 *     `integer` (`minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`), `boolean`
 *   - `enum` (and `const` is ignored) on strings and numbers
 *   - `type: array` of primitives (`items` is a string/number/integer/enum schema, `minItems`,
 *     `maxItems`)
 *   - `title`, `description`, `default`
 * Extensions: `x-ui` { order, group, widget, help, label, placeholder, hidden, groups } and
 * `x-secret: true` (write-only: never prefilled, shown as "set / not set").
 * Anything else (oneOf, $ref, patternProperties, ...) is ignored: the server stays authoritative.
 */

export interface UiHints {
  /** Position among the siblings (ascending); unordered fields keep the schema's order, after. */
  order?: number
  /** Group id; fields with the same id are shown together under a heading. */
  group?: string
  /** `text` | `password` | `textarea` | `email` | `url` | `select` | `radio` | `switch` | `checkboxes`. */
  widget?: string
  /** Label key resolved through the pack's labels (`resolveText`). */
  label?: string
  /** Help text key resolved through the pack's labels; falls back to the schema `description`. */
  help?: string
  placeholder?: string
  hidden?: boolean
  /** On an object schema: the groups, as ids or `{id, title, order}` (title is a label key). */
  groups?: (string | { id: string; title?: string; order?: number })[]
}

export interface JsonSchema {
  type?: string | string[]
  title?: string
  description?: string
  properties?: Record<string, JsonSchema>
  required?: string[]
  enum?: unknown[]
  items?: JsonSchema
  default?: unknown
  minimum?: number
  maximum?: number
  exclusiveMinimum?: number
  exclusiveMaximum?: number
  minLength?: number
  maxLength?: number
  pattern?: string
  format?: string
  minItems?: number
  maxItems?: number
  'x-ui'?: UiHints
  'x-secret'?: boolean
  [key: string]: unknown
}

export type Values = Record<string, unknown>

/** Error codes of the client-side validation; the form translates them. */
export type IssueCode =
  | 'required'
  | 'invalid_number'
  | 'not_integer'
  | 'min'
  | 'max'
  | 'min_length'
  | 'max_length'
  | 'pattern'
  | 'invalid_url'
  | 'invalid_email'
  | 'invalid_value'
  | 'min_items'
  | 'max_items'

export interface Issue {
  code: IssueCode
  params?: Record<string, number | string>
}

export function typeOf(schema: JsonSchema): string | undefined {
  const t = schema.type
  if (Array.isArray(t)) return t.find((x) => x !== 'null')
  return t
}

export function isObjectSchema(schema: JsonSchema | undefined): boolean {
  return !!schema && (typeOf(schema) === 'object' || !!schema.properties)
}

export const uiOf = (schema: JsonSchema): UiHints => schema['x-ui'] ?? {}
export const isSecret = (schema: JsonSchema) => schema['x-secret'] === true

export interface FieldDef {
  name: string
  /** Dotted path from the root (`a.b`); array rows are `a.0`. */
  path: string
  schema: JsonSchema
  required: boolean
}

export interface GroupDef {
  id: string | null
  titleKey?: string
  fields: FieldDef[]
}

/** The visible properties of an object schema, ordered by `x-ui.order`, then declaration order. */
export function fieldsOf(schema: JsonSchema, base = ''): FieldDef[] {
  const required = new Set(schema.required ?? [])
  const entries = Object.entries(schema.properties ?? {}).map(([name, s], index) => ({
    def: {
      name,
      path: base ? `${base}.${name}` : name,
      schema: s,
      required: required.has(name),
    } satisfies FieldDef,
    index,
  }))
  return entries
    .filter((e) => !uiOf(e.def.schema).hidden)
    .sort((a, b) => {
      const oa = uiOf(a.def.schema).order
      const ob = uiOf(b.def.schema).order
      if (oa !== undefined && ob !== undefined && oa !== ob) return oa - ob
      if (oa !== undefined && ob === undefined) return -1
      if (oa === undefined && ob !== undefined) return 1
      return a.index - b.index
    })
    .map((e) => e.def)
}

/**
 * Splits the fields into groups. Fields without a group come first (no heading); declared groups
 * (`x-ui.groups` on the object, by `order`) follow in their order, then any group id only fields
 * use, in order of first appearance.
 */
export function groupsOf(schema: JsonSchema, base = ''): GroupDef[] {
  const fields = fieldsOf(schema, base)
  const declared = (uiOf(schema).groups ?? []).map((g, index) =>
    typeof g === 'string' ? { id: g, title: undefined, order: index } : { order: index, ...g },
  )
  declared.sort((a, b) => (a.order ?? 0) - (b.order ?? 0))
  const groups = new Map<string | null, GroupDef>()
  groups.set(null, { id: null, fields: [] })
  for (const g of declared) groups.set(g.id, { id: g.id, titleKey: g.title, fields: [] })
  for (const f of fields) {
    const id = uiOf(f.schema).group ?? null
    let group = groups.get(id)
    if (!group) {
      group = { id, fields: [] }
      groups.set(id, group)
    }
    group.fields.push(f)
  }
  return [...groups.values()].filter((g) => g.fields.length > 0)
}

/** `example_api_key` with prefix `example_` → "Api key". */
export function humanize(name: string, stripPrefix = ''): string {
  const bare = stripPrefix && name.startsWith(stripPrefix) ? name.slice(stripPrefix.length) : name
  const words = bare.replace(/[_-]+/g, ' ').trim()
  return words ? words.charAt(0).toUpperCase() + words.slice(1) : name
}

/* ───────────── path helpers ───────────── */

/** A copy of the record without `key`. */
export function omit<T>(record: Record<string, T>, key: string): Record<string, T> {
  const copy = { ...record }
  delete copy[key]
  return copy
}

const segments = (path: string) => path.split('.')

export function getAt(root: unknown, path: string): unknown {
  let cur: unknown = root
  for (const seg of segments(path)) {
    if (cur === null || typeof cur !== 'object') return undefined
    cur = (cur as Record<string, unknown>)[seg]
  }
  return cur
}

/** Immutable set; a missing container is created (object, or array for a numeric segment). */
export function setAt<T>(root: T, path: string, value: unknown): T {
  const segs = segments(path)
  const go = (node: unknown, i: number): unknown => {
    const seg = segs[i]!
    const isIndex = /^\d+$/.test(seg)
    const base: unknown = node ?? (isIndex ? [] : {})
    const copy = Array.isArray(base) ? [...base] : { ...(base as Record<string, unknown>) }
    const child = i === segs.length - 1 ? value : go((copy as Record<string, unknown>)[seg], i + 1)
    ;(copy as Record<string, unknown>)[seg] = child
    return copy
  }
  return go(root, 0) as T
}

/* ───────────── values ───────────── */

/** Schema defaults under the stored values. */
export function withDefaults(schema: JsonSchema, values: Values | undefined): Values {
  const out: Values = {}
  for (const [name, s] of Object.entries(schema.properties ?? {})) {
    if (isSecret(s)) continue // secrets are never prefilled, not even with a default
    const stored = values?.[name]
    if (isObjectSchema(s)) {
      out[name] = withDefaults(s, (stored as Values | undefined) ?? undefined)
    } else if (stored !== undefined && stored !== null) {
      out[name] = stored
    } else if (s.default !== undefined) {
      out[name] = s.default
    }
  }
  // keep stored keys the schema does not know (the server decides whether they are valid)
  for (const [k, v] of Object.entries(values ?? {})) if (!(k in out)) out[k] = v
  return out
}

export interface BuildInput {
  schema: JsonSchema
  /** Raw field state: strings for number inputs, as typed. */
  raw: Values
  /** Secret fields by path: typed text, or `null` to clear. A path that is absent is left alone. */
  secrets: Record<string, string | null>
  /** Paths of the secret fields that are set on the server. */
  secretsSet: ReadonlySet<string>
}

export interface BuildResult {
  /** The settings document to send: coerced values, and the secrets that were typed or cleared. */
  values: Values
  /** Client-side issues by field path. */
  issues: Record<string, Issue>
}

const isBlank = (v: unknown) =>
  v === undefined || v === null || (typeof v === 'string' && v.trim() === '')

/** Coerces the raw form state into a settings document and validates it against the schema subset. */
export function buildValues({ schema, raw, secrets, secretsSet }: BuildInput): BuildResult {
  const issues: Record<string, Issue> = {}

  function checkString(s: JsonSchema, v: string, path: string) {
    if (s.minLength !== undefined && v.length < s.minLength) {
      issues[path] = { code: 'min_length', params: { min: s.minLength } }
    } else if (s.maxLength !== undefined && v.length > s.maxLength) {
      issues[path] = { code: 'max_length', params: { max: s.maxLength } }
    } else if (s.pattern) {
      try {
        if (!new RegExp(s.pattern, 'u').test(v)) issues[path] = { code: 'pattern' }
      } catch {
        /* a pattern this browser cannot compile is the server's to judge */
      }
    }
    if (issues[path]) return
    if (s.format === 'uri' || s.format === 'url') {
      try {
        new URL(v)
      } catch {
        issues[path] = { code: 'invalid_url' }
      }
    } else if (s.format === 'email' && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v)) {
      issues[path] = { code: 'invalid_email' }
    }
  }

  function coerceNumber(s: JsonSchema, v: unknown, path: string): number | undefined {
    const n = typeof v === 'number' ? v : Number(String(v).trim().replace(',', '.'))
    if (!Number.isFinite(n)) {
      issues[path] = { code: 'invalid_number' }
      return undefined
    }
    if (typeOf(s) === 'integer' && !Number.isInteger(n)) {
      issues[path] = { code: 'not_integer' }
      return undefined
    }
    if (
      (s.minimum !== undefined && n < s.minimum) ||
      (s.exclusiveMinimum !== undefined && n <= s.exclusiveMinimum)
    ) {
      issues[path] = { code: 'min', params: { min: s.minimum ?? s.exclusiveMinimum ?? 0 } }
      return undefined
    }
    if (
      (s.maximum !== undefined && n > s.maximum) ||
      (s.exclusiveMaximum !== undefined && n >= s.exclusiveMaximum)
    ) {
      issues[path] = { code: 'max', params: { max: s.maximum ?? s.exclusiveMaximum ?? 0 } }
      return undefined
    }
    return n
  }

  /** One primitive; undefined means "leave the key out". */
  function coercePrimitive(s: JsonSchema, v: unknown, path: string): unknown {
    const type = typeOf(s)
    if (s.enum) {
      if (isBlank(v)) return undefined
      const match = s.enum.find((e) => String(e) === String(v))
      if (match === undefined) {
        issues[path] = { code: 'invalid_value' }
        return undefined
      }
      return match
    }
    if (type === 'boolean') return v === true || v === 'true'
    if (type === 'number' || type === 'integer') {
      return isBlank(v) ? undefined : coerceNumber(s, v, path)
    }
    if (isBlank(v)) return undefined
    const str = String(v)
    checkString(s, str, path)
    return str
  }

  function build(s: JsonSchema, value: unknown, base: string): Values {
    const out: Values = {}
    const required = new Set(s.required ?? [])
    for (const [name, ps] of Object.entries(s.properties ?? {})) {
      const path = base ? `${base}.${name}` : name
      const v = (value as Values | undefined)?.[name]
      if (isSecret(ps)) {
        const typed = secrets[path]
        if (typed === null) out[name] = null
        else if (typeof typed === 'string' && typed !== '') {
          checkString(ps, typed, path)
          out[name] = typed
        } else if (required.has(name) && !secretsSet.has(path)) {
          issues[path] = { code: 'required' }
        }
        continue
      }
      if (isObjectSchema(ps)) {
        const child = build(ps, v, path)
        if (Object.keys(child).length > 0 || required.has(name)) out[name] = child
        continue
      }
      if (typeOf(ps) === 'array') {
        const rows = Array.isArray(v) ? v : []
        const items = ps.items ?? {}
        const list: unknown[] = []
        rows.forEach((row, i) => {
          if (isBlank(row) && !items.enum) return
          const c = coercePrimitive(items, row, `${path}.${i}`)
          if (c !== undefined) list.push(c)
        })
        if (ps.minItems !== undefined && list.length < ps.minItems) {
          issues[path] =
            list.length === 0 && ps.minItems === 1
              ? { code: 'required' }
              : { code: 'min_items', params: { min: ps.minItems } }
        } else if (ps.maxItems !== undefined && list.length > ps.maxItems) {
          issues[path] = { code: 'max_items', params: { max: ps.maxItems } }
        } else if (list.length === 0 && required.has(name)) {
          issues[path] = { code: 'required' }
        }
        if (list.length > 0) out[name] = list
        continue
      }
      const c = coercePrimitive(ps, v, path)
      if (c === undefined) {
        if (required.has(name) && !issues[path]) issues[path] = { code: 'required' }
        continue
      }
      out[name] = c
    }
    // values of keys the schema does not describe pass through untouched
    for (const [k, v] of Object.entries((value as Values | undefined) ?? {})) {
      if (!(k in (s.properties ?? {}))) out[k] = v
    }
    return out
  }

  return { values: build(schema, raw, ''), issues }
}

/* ───────────── server errors ───────────── */

export interface ServerIssues {
  /** Messages by field path, the first issue of each field. */
  fields: Record<string, { code: string; message?: string }>
  /** Issues that name no known field (for example `required` on the parent object). */
  general: { code: string; message?: string }[]
}

/**
 * Maps problem+json field errors (`settings.example_sender`, `values.a.b`, `events[1]`) onto the
 * form's field paths. A path that is not a field of the schema (a `required` violation is reported
 * on the parent object) goes to `general`.
 */
export function mapServerIssues(
  schema: JsonSchema,
  fieldErrors: Record<string, { code: string; message?: string }[]>,
): ServerIssues {
  const known = new Set<string>()
  const collect = (s: JsonSchema, base: string) => {
    for (const [name, ps] of Object.entries(s.properties ?? {})) {
      const path = base ? `${base}.${name}` : name
      known.add(path)
      if (isObjectSchema(ps)) collect(ps, path)
    }
  }
  collect(schema, '')
  const out: ServerIssues = { fields: {}, general: [] }
  for (const [rawField, issues] of Object.entries(fieldErrors)) {
    const issue = issues[0]
    if (!issue) continue
    const path = rawField.replace(/\[(\d+)\]/g, '.$1').replace(/^(settings|values)\.?/, '')
    // an array row error lands on the row; the parent array is the known field
    const field = known.has(path) || /\.\d+$/.test(path) ? path : undefined
    if (path && field) out.fields[path] = issue
    else out.general.push(issue)
  }
  return out
}
