import { registerReset } from '../reset'
import type { Schema } from '@/api/draft'

/**
 * In-memory backend for the draft contract (tenancy and people). Used by the MSW handlers in unit
 * tests and by `pnpm dev:mock`. `resetDb()` restores the seed; handlers read and write `db`.
 */

export const ORG = 'org-1'
export const SITE = 'site-a'
const T0 = '2026-03-01T09:00:00Z'

type Sites = Schema<'Site'>
type Unit = Schema<'Unit'>
type Person = Schema<'Person'>

export interface JobState {
  job: Schema<'Job'>
  polls: number
  /** Called when the job reaches `succeeded`, to apply the import. */
  onDone?: () => void
}

export interface Db {
  sites: Sites[]
  settings: Record<string, Schema<'SiteSettings'>>
  bank: Record<string, Schema<'BankDetails'>>
  blocks: Schema<'Block'>[]
  units: Unit[]
  keys: Schema<'AllocationKey'>[]
  keyValues: Record<string, Record<string, number>>
  tags: Schema<'UnitTag'>[]
  tagMembers: Record<string, string[]>
  vehicles: Schema<'Vehicle'>[]
  persons: Person[]
  nationalIds: Record<string, string>
  occupancies: Schema<'Occupancy'>[]
  memberships: Schema<'Membership'>[]
  invitations: Schema<'Invitation'>[]
  claims: Schema<'UnitClaim'>[]
  vendors: Schema<'Vendor'>[]
  moveOuts: Schema<'MoveOut'>[]
  joinCode: Schema<'JoinCode'>
  transfers: Schema<'SiteTransfer'>[]
  jobs: Record<string, JobState>
  /** Re-authentication window end (ms since epoch); sensitive endpoints answer 403 before it. */
  reauthUntil: number
  /** Accepted by `POST /v1/auth/reauth`. */
  password: string
}

const address = {
  line1: 'Atatürk Cd. 12',
  line2: null,
  district: 'Kadıköy',
  city: 'İstanbul',
  postal_code: '34710',
  country_code: 'TR',
}

function unit(
  id: string,
  block: string,
  door: string,
  type: string,
  area: number | null,
  parent: string | null = null,
): Unit {
  return {
    id,
    org_id: ORG,
    site_id: SITE,
    block_id: block,
    door_number: door,
    legal_ref: null,
    type: type as Unit['type'],
    area_m2: area,
    parent_unit_id: parent,
    status: 'active',
    vacant: false,
    occupancy: { owners: 0, tenants: 0, household: 0 },
    created_at: T0,
    updated_at: T0,
    version: 1,
    payer_overrides: {},
  }
}

function person(id: string, name: string, extra: Partial<Person> = {}): Person {
  return {
    id,
    org_id: ORG,
    user_id: null,
    display_name: name,
    contact: { email: null, phone: null, preferred_channel: null, address: null },
    national_id_masked: null,
    national_id_country: null,
    gate_contact_phone: null,
    notes: null,
    join_status: 'no_account',
    directory_consent: [],
    created_at: T0,
    updated_at: T0,
    version: 1,
    ...extra,
  }
}

function occ(
  id: string,
  p: string,
  name: string,
  u: string,
  door: string,
  kind: 'owner' | 'tenant' | 'household',
  extra: Partial<Schema<'Occupancy'>> = {},
): Schema<'Occupancy'> {
  return {
    id,
    org_id: ORG,
    person_id: p,
    person_display_name: name,
    unit_id: u,
    unit_door_number: door,
    kind,
    side: kind === 'tenant' ? 'tenant' : 'owner',
    share_pct: kind === 'owner' ? 100 : null,
    is_correspondent: kind === 'owner',
    may_see_money: kind !== 'household',
    valid_from: '2024-01-01',
    valid_to: null,
    created_at: T0,
    updated_at: T0,
    version: 1,
    ...extra,
  }
}

export function seed(): Db {
  const keys: Db['keys'] = (
    [
      ['key-arsa', 'arsa_payi', 'Arsa payı (land share)', 'share', true],
      ['key-equal', 'equal', 'Equal per unit', 'equal', true],
      ['key-area', 'area', 'By floor area', 'area', true],
      ['key-heat', 'heating', 'Heating meters', 'table', false],
    ] as const
  ).map(([id, code, name, kind, is_system]) => ({
    id,
    org_id: ORG,
    site_id: SITE,
    code,
    name,
    kind,
    is_system,
    created_at: T0,
    updated_at: T0,
    version: 1,
  }))
  const units = [
    unit('unit-a1', 'block-a', 'A-1', 'flat', 96.5),
    unit('unit-a2', 'block-a', 'A-2', 'flat', 96.5),
    unit('unit-a3', 'block-a', 'A-3', 'flat', 110),
    unit('unit-b1', 'block-b', 'B-1', 'flat', 85),
    unit('unit-b2', 'block-b', 'B-2', 'shop', 40),
    unit('unit-p1', 'block-a', 'P-1', 'parking', 12, 'unit-a1'),
  ]
  const occupancies = [
    occ('occ-1', 'person-1', 'Ayşe Yılmaz', 'unit-a1', 'A-1', 'owner'),
    occ('occ-2', 'person-2', 'Mehmet Kaya', 'unit-a1', 'A-1', 'tenant'),
    occ('occ-3', 'person-3', 'Zeynep Demir', 'unit-a2', 'A-2', 'owner'),
    occ('occ-4', 'person-4', 'Can Öztürk', 'unit-a3', 'A-3', 'owner', { valid_to: '2025-12-31' }),
  ]
  const db: Db = {
    sites: [
      {
        id: SITE,
        org_id: ORG,
        name: 'Güneş Sitesi',
        address,
        timezone: 'Europe/Istanbul',
        currency: 'TRY',
        fiscal_year_start_month: 1,
        lock_date: null,
        status: 'active',
        created_at: T0,
        updated_at: T0,
        version: 1,
      },
    ],
    settings: {
      [SITE]: {
        version: 1,
        tenants_are_portal_users: true,
        directory_enabled: false,
        owner_financial_transparency: true,
        parking_storage_default: 'appurtenance',
        bills_enabled: false,
        announcement_publishers: 'manager_only',
        ticket_archive_months: null,
        visitor_log_retention_days: 90,
        legal_preset: 'tr',
        access: {
          offline_grace_hours: 72,
          agent_silent_minutes: 5,
          plate_match: 'exact',
          maintenance_hour: null,
        },
        extra: null,
        payments: {
          card: { available: true, provider: 'iyzico' },
          merchant_of_record: 'site',
          merchant_of_record_override: null,
        },
      },
    },
    bank: {
      [SITE]: {
        version: 1,
        iban: 'TR330006100519786457841326',
        holder: 'Güneş Sitesi Yönetimi',
        bank_name: 'Örnek Bank',
        reference_hint: 'Daire no ve ad soyad',
        suggested_reference: null,
      },
    },
    blocks: [
      {
        id: 'block-a',
        org_id: ORG,
        site_id: SITE,
        name: 'A Blok',
        is_default: false,
        created_at: T0,
        updated_at: T0,
        version: 1,
      },
      {
        id: 'block-b',
        org_id: ORG,
        site_id: SITE,
        name: 'B Blok',
        is_default: false,
        created_at: T0,
        updated_at: T0,
        version: 1,
      },
    ],
    units,
    keys,
    keyValues: {
      'key-arsa': { 'unit-a1': 100, 'unit-a2': 100, 'unit-a3': 120, 'unit-b1': 90 },
      'key-heat': {},
    },
    tags: [
      {
        id: 'tag-lift',
        org_id: ORG,
        site_id: SITE,
        code: 'lift',
        name: 'Lift',
        created_at: T0,
        updated_at: T0,
        version: 1,
      },
    ],
    tagMembers: { 'tag-lift': ['unit-a1', 'unit-a2', 'unit-a3'] },
    vehicles: [
      {
        id: 'veh-1',
        org_id: ORG,
        site_id: SITE,
        unit_id: 'unit-a1',
        plate: '34 abc 123',
        plate_normalized: '34ABC123',
        description: 'Gri Renault',
        archived: false,
        warning: null,
        created_at: T0,
        updated_at: T0,
        version: 1,
      },
    ],
    persons: [
      person('person-1', 'Ayşe Yılmaz', {
        contact: {
          email: 'ayse@example.com',
          phone: '+905551112233',
          preferred_channel: 'sms',
          address: null,
        },
        national_id_masked: '*******1234',
        national_id_country: 'TR',
        join_status: 'joined',
        user_id: 'u1',
        directory_consent: [{ site_id: SITE, consent: true, show_phone: false }],
      }),
      person('person-2', 'Mehmet Kaya', {
        contact: { email: null, phone: '+905552223344', preferred_channel: 'sms', address: null },
        join_status: 'invited',
      }),
      person('person-3', 'Zeynep Demir', {
        contact: {
          email: 'zeynep@example.com',
          phone: null,
          preferred_channel: 'email',
          address: null,
        },
      }),
      person('person-4', 'Can Öztürk'),
    ],
    nationalIds: { 'person-1': '10000000146' },
    occupancies,
    memberships: [
      {
        id: 'mem-1',
        org_id: ORG,
        person_id: 'person-1',
        person_display_name: 'Ayşe Yılmaz',
        role: 'site_manager',
        scope_type: 'site',
        scope_id: SITE,
        valid_from: '2024-01-01',
        valid_to: null,
        decision_id: null,
        site_owned: false,
        created_at: T0,
        updated_at: T0,
        version: 1,
      },
      {
        id: 'mem-2',
        org_id: ORG,
        person_id: 'person-3',
        person_display_name: 'Zeynep Demir',
        role: 'board_member',
        scope_type: 'site',
        scope_id: SITE,
        valid_from: '2025-01-01',
        valid_to: '2026-12-31',
        decision_id: null,
        site_owned: true,
        created_at: T0,
        updated_at: T0,
        version: 1,
      },
    ],
    invitations: [
      {
        id: 'inv-1',
        org_id: ORG,
        kind: 'manager',
        site_id: SITE,
        person_id: 'person-2',
        unit_id: 'unit-a1',
        channel: 'sms',
        destination: '+905552223344',
        status: 'pending',
        expires_at: '2026-12-01T09:00:00Z',
        accepted_at: null,
        invited_by: 'person-1',
        created_at: T0,
        updated_at: T0,
      },
    ],
    claims: [
      claim('claim-1', 'Burak Şahin', 'unit-b1', 'B-1', 'owner', 'Daire sahibiyim, tapu elimde.'),
      claim('claim-2', 'Elif Arslan', 'unit-a2', 'A-2', 'tenant', null),
    ],
    vendors: [
      {
        id: 'vendor-1',
        org_id: ORG,
        name: 'Asansör Servisi Ltd.',
        tax_id: '1234567890',
        contact: {
          email: 'servis@example.com',
          phone: '+902161112233',
          preferred_channel: null,
          address: null,
        },
        default_expense_category_id: null,
        archived: false,
        created_at: T0,
        updated_at: T0,
        version: 1,
      },
    ],
    moveOuts: [],
    joinCode: {
      code: 'GUNES-7QK2',
      url: 'http://localhost/join/GUNES-7QK2',
      enabled: true,
      rotated_at: T0,
    },
    transfers: [],
    jobs: {},
    reauthUntil: 0,
    password: 'correct-password',
  }
  recomputeOccupancy(db)
  return db
}

function claim(
  id: string,
  name: string,
  unitId: string,
  door: string,
  role: 'owner' | 'tenant' | 'household',
  note: string | null,
): Schema<'UnitClaim'> {
  return {
    id,
    org_id: ORG,
    user_id: `user-${id}`,
    display_name: name,
    unit_id: unitId,
    unit_door_number: door,
    claimed_role: role,
    status: 'pending',
    decided_by: null,
    decided_at: null,
    decision_note: null,
    note,
    created_at: T0,
    updated_at: T0,
  }
}

/** Vacancy and the occupancy counters are derived, like on the server. */
export function recomputeOccupancy(db: Db, today = new Date().toISOString().slice(0, 10)) {
  for (const u of db.units) {
    const active = db.occupancies.filter(
      (o) => o.unit_id === u.id && o.valid_from <= today && (!o.valid_to || o.valid_to >= today),
    )
    u.occupancy = {
      owners: active.filter((o) => o.kind === 'owner').length,
      tenants: active.filter((o) => o.kind === 'tenant').length,
      household: active.filter((o) => o.kind === 'household').length,
    }
    u.vacant = active.length === 0
  }
}

export let db: Db = seed()
export function resetDb() {
  db = seed()
  return db
}
registerReset(() => resetDb())
