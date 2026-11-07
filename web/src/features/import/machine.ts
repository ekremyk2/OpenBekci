import type { Schema } from '@/api/draft'
import { normalizeHeader, type ParsedTable } from './parse'

export type ImportKind = 'units' | 'people' | 'opening_balances'

export interface TargetField {
  id: string
  required?: boolean
  /** Normalized header names that map to this field automatically. */
  synonyms: string[]
}

/**
 * Target fields per import kind. The draft spec (`ColumnMapping.columns`) does not enumerate the
 * target names; these are this client's assumption and are listed in the PR description.
 */
export const TARGETS: Record<ImportKind, TargetField[]> = {
  units: [
    {
      id: 'door_number',
      required: true,
      synonyms: ['door number', 'door', 'daire', 'daire no', 'kapi no', 'unit', 'no'],
    },
    { id: 'block', synonyms: ['block', 'blok', 'bina'] },
    { id: 'type', synonyms: ['type', 'tur', 'tip', 'bagimsiz bolum tipi'] },
    { id: 'area_m2', synonyms: ['area', 'area m2', 'm2', 'alan', 'brut alan', 'net alan'] },
    {
      id: 'legal_ref',
      synonyms: ['legal ref', 'tapu', 'tapu no', 'bagimsiz bolum no', 'title deed'],
    },
    { id: 'parent_door_number', synonyms: ['parent', 'belongs to', 'bagli daire'] },
  ],
  people: [
    {
      id: 'display_name',
      required: true,
      synonyms: ['name', 'display name', 'ad soyad', 'adi soyadi', 'isim', 'full name'],
    },
    { id: 'phone', synonyms: ['phone', 'telefon', 'gsm', 'cep', 'mobile'] },
    { id: 'email', synonyms: ['email', 'e posta', 'eposta', 'mail'] },
    { id: 'national_id', synonyms: ['national id', 'tckn', 'tc', 'tc kimlik no', 'kimlik no'] },
    { id: 'door_number', synonyms: ['door number', 'door', 'daire', 'daire no', 'unit'] },
    { id: 'block', synonyms: ['block', 'blok'] },
    { id: 'kind', synonyms: ['kind', 'role', 'kat maliki kiraci', 'malik kiraci', 'iliski'] },
    { id: 'share_pct', synonyms: ['share', 'share pct', 'hisse', 'hisse orani', 'arsa payi'] },
  ],
  opening_balances: [
    {
      id: 'door_number',
      required: true,
      synonyms: ['door number', 'door', 'daire', 'daire no', 'unit'],
    },
    {
      id: 'owner_principal',
      synonyms: ['owner principal', 'owner debt', 'malik borc', 'kat maliki borcu'],
    },
    { id: 'owner_late_fee', synonyms: ['owner late fee', 'malik gecikme', 'malik gecikme zammi'] },
    {
      id: 'tenant_principal',
      synonyms: ['tenant principal', 'tenant debt', 'kiraci borc', 'kiraci borcu'],
    },
    {
      id: 'tenant_late_fee',
      synonyms: ['tenant late fee', 'kiraci gecikme', 'kiraci gecikme zammi'],
    },
  ],
}

/** Best-effort first guess of the mapping from the header row. */
export function guessMapping(kind: ImportKind, headers: string[]): Record<string, string> {
  const normalized = headers.map((h) => ({ h, n: normalizeHeader(h) }))
  const out: Record<string, string> = {}
  const used = new Set<string>()
  for (const t of TARGETS[kind]) {
    const hit = normalized.find(
      (x) => !used.has(x.h) && (x.n === t.id.replace(/_/g, ' ') || t.synonyms.includes(x.n)),
    )
    if (hit) {
      out[t.id] = hit.h
      used.add(hit.h)
    }
  }
  return out
}

/** Mapping remembered per organization and kind (web plan §9.1). */
export const mappingStore = {
  key: (orgId: string, kind: ImportKind) => `ob.importMapping.${orgId}.${kind}`,
  load(orgId: string, kind: ImportKind, headers: string[]): Record<string, string> {
    try {
      const raw = localStorage.getItem(this.key(orgId, kind))
      if (!raw) return {}
      const saved = JSON.parse(raw) as Record<string, string>
      return Object.fromEntries(Object.entries(saved).filter(([, src]) => headers.includes(src)))
    } catch {
      return {}
    }
  },
  save(orgId: string, kind: ImportKind, mapping: Record<string, string>) {
    try {
      localStorage.setItem(this.key(orgId, kind), JSON.stringify(mapping))
    } catch {
      /* ignore */
    }
  },
}

export type ImportState =
  | { step: 'upload'; error?: string }
  | { step: 'mapping'; file: File; table: ParsedTable; mapping: Record<string, string> }
  | { step: 'checking'; file: File; table: ParsedTable; mapping: Record<string, string> }
  | {
      step: 'report'
      file: File
      table: ParsedTable
      mapping: Record<string, string>
      report: Schema<'ImportReport'>
    }
  | {
      step: 'committing'
      file: File
      table: ParsedTable
      mapping: Record<string, string>
      report: Schema<'ImportReport'>
    }
  | { step: 'progress'; jobId: string }
  | { step: 'done'; jobId: string; ok: boolean }

export type ImportEvent =
  | { type: 'FILE_PARSED'; file: File; table: ParsedTable; mapping: Record<string, string> }
  | { type: 'FILE_FAILED'; error: string }
  | { type: 'MAP'; target: string; source: string | null }
  | { type: 'CHECK' }
  | { type: 'CHECKED'; report: Schema<'ImportReport'> }
  | { type: 'CHECK_FAILED'; error: string }
  | { type: 'BACK' }
  | { type: 'CONFIRM' }
  | { type: 'COMMITTED'; jobId: string }
  | { type: 'COMMIT_FAILED' }
  | { type: 'JOB_FINISHED'; ok: boolean }
  | { type: 'RESET' }

export const initialState: ImportState = { step: 'upload' }

export function requiredMapped(kind: ImportKind, mapping: Record<string, string>): boolean {
  return TARGETS[kind].every((t) => !t.required || !!mapping[t.id])
}

/**
 * The wizard's state machine: upload → mapping → checking (dry run) → report → committing →
 * progress → done. Events that do not apply to the current step are ignored, so a stray response
 * cannot move the wizard backwards or past a failed check.
 */
export function importReducer(kind: ImportKind) {
  return (state: ImportState, event: ImportEvent): ImportState => {
    if (event.type === 'RESET') return initialState
    switch (state.step) {
      case 'upload':
        if (event.type === 'FILE_PARSED')
          return { step: 'mapping', file: event.file, table: event.table, mapping: event.mapping }
        if (event.type === 'FILE_FAILED') return { step: 'upload', error: event.error }
        return state
      case 'mapping':
        if (event.type === 'MAP') {
          const mapping = { ...state.mapping }
          if (event.source) mapping[event.target] = event.source
          else delete mapping[event.target]
          return { ...state, mapping }
        }
        if (event.type === 'CHECK' && requiredMapped(kind, state.mapping))
          return { ...state, step: 'checking' }
        if (event.type === 'BACK') return initialState
        return state
      case 'checking':
        if (event.type === 'CHECKED')
          return {
            step: 'report',
            file: state.file,
            table: state.table,
            mapping: state.mapping,
            report: event.report,
          }
        if (event.type === 'CHECK_FAILED')
          return { step: 'mapping', file: state.file, table: state.table, mapping: state.mapping }
        return state
      case 'report':
        if (event.type === 'BACK')
          return { step: 'mapping', file: state.file, table: state.table, mapping: state.mapping }
        // rows with errors block the import: the report must be clean first
        if (event.type === 'CONFIRM' && state.report.error_rows === 0)
          return { ...state, step: 'committing' }
        return state
      case 'committing':
        if (event.type === 'COMMITTED') return { step: 'progress', jobId: event.jobId }
        if (event.type === 'COMMIT_FAILED')
          return {
            step: 'report',
            file: state.file,
            table: state.table,
            mapping: state.mapping,
            report: state.report,
          }
        return state
      case 'progress':
        if (event.type === 'JOB_FINISHED') return { step: 'done', jobId: state.jobId, ok: event.ok }
        return state
      case 'done':
        return state
    }
  }
}
