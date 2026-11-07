import { http, HttpResponse } from 'msw'
import { isValidNationalId } from '@/features/people/tckn'
import { db, ORG, recomputeOccupancy, SITE } from './db'
import { matches, newId, nowIso, notFound, paginate, problem, versionConflict } from './http'
import { requireReauth } from './tenancy'

/* MSW handlers for the draft `people` module, written from the spec (api/src/paths/people). */

const o = (path: string) => `*/v1/orgs/:orgId/${path}`
const today = () => new Date().toISOString().slice(0, 10)

function fieldError(field: string, code: string, message: string) {
  return problem(422, 'validation_failed', 'The request has invalid fields.', [
    { field, code, message },
  ])
}

function unitDoor(id: string) {
  return db.units.find((u) => u.id === id)?.door_number ?? ''
}
function personName(id: string) {
  return db.persons.find((p) => p.id === id)?.display_name ?? ''
}

function previewFor(occupancyId: string, asOf: string) {
  const owes = occupancyId === 'occ-2' // the seeded tenant has an open balance
  return {
    occupancy_id: occupancyId,
    as_of: asOf,
    principal: { amount: owes ? 6_000_00 : 0, currency: 'TRY' },
    late_fee: { amount: owes ? 180_00 : 0, currency: 'TRY' },
    credit: { amount: 0, currency: 'TRY' },
    total: { amount: owes ? 6_180_00 : 0, currency: 'TRY' },
    default_open_charges: 'carry_to_person',
    carried: owes
      ? [
          {
            charge_id: 'charge-1',
            charge_type: 'dues',
            period: '2026-03',
            balance: { amount: 6_000_00, currency: 'TRY' },
            outcome: 'carried_to_person',
            person_id: 'person-2',
          },
        ]
      : [],
  }
}

export const peopleHandlers = [
  // persons
  http.get(o('persons'), ({ request }) => {
    const url = new URL(request.url)
    const p = url.searchParams
    const unit = p.get('unit_id')
    const items = db.persons.filter(
      (x) =>
        matches(p.get('q'), x.display_name, x.contact.email, x.contact.phone) &&
        (!p.get('join_status') || x.join_status === p.get('join_status')) &&
        (!unit || db.occupancies.some((oc) => oc.person_id === x.id && oc.unit_id === unit)),
    )
    return HttpResponse.json(paginate(items, url))
  }),
  http.post(o('persons'), async ({ request }) => {
    const body = (await request.json()) as {
      display_name: string
      contact?: Record<string, unknown>
      gate_contact_phone?: string | null
      notes?: string | null
    }
    if (!body.display_name?.trim())
      return fieldError('display_name', 'required', 'Name is required')
    const p = {
      id: newId('person'),
      org_id: ORG,
      user_id: null,
      display_name: body.display_name.trim(),
      contact: {
        email: null,
        phone: null,
        preferred_channel: null,
        address: null,
        ...(body.contact ?? {}),
      },
      national_id_masked: null,
      national_id_country: null,
      gate_contact_phone: body.gate_contact_phone ?? null,
      notes: body.notes ?? null,
      join_status: 'no_account',
      directory_consent: [],
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    }
    db.persons.push(p as never)
    return HttpResponse.json(p, { status: 201 })
  }),
  http.get(o('persons/:personId'), ({ params }) => {
    const p = db.persons.find((x) => x.id === params.personId)
    return p ? HttpResponse.json(p) : notFound()
  }),
  http.patch(o('persons/:personId'), async ({ params, request }) => {
    const p = db.persons.find((x) => x.id === params.personId)
    if (!p) return notFound()
    const conflict = versionConflict(request, p.version)
    if (conflict) return conflict
    const body = (await request.json()) as Partial<typeof p>
    if (body.display_name !== undefined && !body.display_name.trim())
      return fieldError('display_name', 'required', 'Name is required')
    Object.assign(p, body, { version: p.version + 1, updated_at: nowIso() })
    return HttpResponse.json(p)
  }),
  http.get(o('persons/:personId/national-id'), ({ params }) => {
    const r = requireReauth()
    if (r) return r
    const p = db.persons.find((x) => x.id === params.personId)
    const value = db.nationalIds[String(params.personId)]
    return HttpResponse.json({
      set: !!value,
      country_code: p?.national_id_country ?? null,
      masked: p?.national_id_masked ?? null,
      value: value ?? null,
    })
  }),
  http.put(o('persons/:personId/national-id'), async ({ params, request }) => {
    const p = db.persons.find((x) => x.id === params.personId)
    if (!p) return notFound()
    const body = (await request.json()) as { country_code: string; value: string }
    if (!isValidNationalId(body.country_code, body.value))
      return fieldError('value', 'invalid_checksum', 'This national id number is not valid')
    const value = body.value.replace(/\s+/g, '')
    db.nationalIds[p.id] = value
    p.national_id_country = body.country_code
    p.national_id_masked = `${'*'.repeat(Math.max(0, value.length - 4))}${value.slice(-4)}`
    return HttpResponse.json({
      set: true,
      country_code: p.national_id_country,
      masked: p.national_id_masked,
    })
  }),
  http.delete(o('persons/:personId/national-id'), ({ params }) => {
    const p = db.persons.find((x) => x.id === params.personId)
    if (p) {
      p.national_id_country = null
      p.national_id_masked = null
    }
    delete db.nationalIds[String(params.personId)]
    return new HttpResponse(null, { status: 204 })
  }),

  // occupancies
  http.get(o('occupancies'), ({ request }) => {
    const url = new URL(request.url)
    const p = url.searchParams
    const asOf = p.get('as_of') ?? today()
    const items = db.occupancies.filter((x) => {
      const active = x.valid_from <= asOf && (!x.valid_to || x.valid_to >= asOf)
      return (
        (!p.get('unit_id') || x.unit_id === p.get('unit_id')) &&
        (!p.get('person_id') || x.person_id === p.get('person_id')) &&
        (!p.get('kind') || x.kind === p.get('kind')) &&
        (!p.get('side') || x.side === p.get('side')) &&
        (p.get('active') === null || String(active) === p.get('active'))
      )
    })
    return HttpResponse.json(paginate(items, url))
  }),
  http.post(o('occupancies'), async ({ request }) => {
    const body = (await request.json()) as {
      person_id: string
      unit_id: string
      kind: 'owner' | 'tenant' | 'household'
      side?: 'owner' | 'tenant'
      share_pct?: number | null
      is_correspondent?: boolean
      may_see_money?: boolean
      valid_from: string
      valid_to?: string | null
    }
    if (body.valid_to && body.valid_to < body.valid_from)
      return fieldError('valid_to', 'before_start', 'The end date is before the start date')
    if (body.kind === 'owner' && body.share_pct != null) {
      const sum = db.occupancies
        .filter((x) => x.unit_id === body.unit_id && x.kind === 'owner' && !x.valid_to)
        .reduce((s, x) => s + (x.share_pct ?? 0), 0)
      if (sum + body.share_pct > 100)
        return fieldError(
          'share_pct',
          'shares_exceed_100',
          'Owner shares of this unit would exceed 100%',
        )
    }
    const oc = {
      id: newId('occ'),
      org_id: ORG,
      person_id: body.person_id,
      person_display_name: personName(body.person_id),
      unit_id: body.unit_id,
      unit_door_number: unitDoor(body.unit_id),
      kind: body.kind,
      side: body.side ?? (body.kind === 'tenant' ? 'tenant' : 'owner'),
      share_pct: body.share_pct ?? null,
      is_correspondent: body.is_correspondent ?? false,
      may_see_money: body.may_see_money ?? body.kind !== 'household',
      valid_from: body.valid_from,
      valid_to: body.valid_to ?? null,
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    }
    db.occupancies.push(oc as never)
    recomputeOccupancy(db)
    return HttpResponse.json(oc, { status: 201 })
  }),
  http.get(o('occupancies/:occId'), ({ params }) => {
    const oc = db.occupancies.find((x) => x.id === params.occId)
    return oc ? HttpResponse.json(oc) : notFound()
  }),
  http.patch(o('occupancies/:occId'), async ({ params, request }) => {
    const oc = db.occupancies.find((x) => x.id === params.occId)
    if (!oc) return notFound()
    const conflict = versionConflict(request, oc.version)
    if (conflict) return conflict
    Object.assign(oc, await request.json(), { version: oc.version + 1, updated_at: nowIso() })
    recomputeOccupancy(db)
    return HttpResponse.json(oc)
  }),
  http.delete(o('occupancies/:occId'), ({ params }) => {
    db.occupancies = db.occupancies.filter((x) => x.id !== params.occId)
    recomputeOccupancy(db)
    return new HttpResponse(null, { status: 204 })
  }),
  http.put(o('occupancies/:occId/money-access'), async ({ params, request }) => {
    const oc = db.occupancies.find((x) => x.id === params.occId)
    if (!oc) return notFound()
    const body = (await request.json()) as { may_see_money: boolean }
    oc.may_see_money = body.may_see_money
    oc.version++
    return HttpResponse.json(oc)
  }),
  http.post(o('occupancies/:occId/move-out\\:preview'), async ({ params, request }) => {
    const body = (await request.json()) as { as_of: string }
    return HttpResponse.json(previewFor(String(params.occId), body.as_of))
  }),
  http.post(o('occupancies/:occId/move-out'), async ({ params, request }) => {
    if (!request.headers.get('Idempotency-Key'))
      return fieldError('Idempotency-Key', 'required', 'Idempotency-Key is required')
    const oc = db.occupancies.find((x) => x.id === params.occId)
    if (!oc) return notFound()
    const body = (await request.json()) as {
      as_of: string
      open_charges?: string
      disposition_reason?: string | null
      transferred_to_person_id?: string | null
    }
    const preview = previewFor(oc.id, body.as_of)
    const choice = body.open_charges ?? preview.default_open_charges
    if (
      choice !== preview.default_open_charges &&
      !body.disposition_reason?.trim() &&
      preview.total.amount > 0
    )
      return fieldError('disposition_reason', 'required', 'Say why the debt is handled differently')
    oc.valid_to = body.as_of
    oc.version++
    recomputeOccupancy(db, body.as_of >= today() ? today() : body.as_of)
    const mo = {
      id: newId('moveout'),
      org_id: ORG,
      occupancy_id: oc.id,
      as_of: body.as_of,
      closing_statement_file_id: 'file-closing-1',
      certificate_file_id: 'file-cert-1',
      open_charges: choice,
      carried: preview.carried.map((c) => ({
        ...c,
        outcome:
          choice === 'keep_on_unit'
            ? 'kept_on_unit'
            : choice === 'write_off'
              ? 'written_off'
              : 'carried_to_person',
      })),
      disposition_reason: body.disposition_reason ?? null,
      transferred_to_person_id: body.transferred_to_person_id ?? null,
      decided_by: 'person-1',
      balance: preview,
      created_at: nowIso(),
      updated_at: nowIso(),
    }
    db.moveOuts.push(mo as never)
    return HttpResponse.json(mo, { status: 201 })
  }),
  http.get(o('move-outs'), ({ request }) => {
    const url = new URL(request.url)
    return HttpResponse.json(paginate(db.moveOuts, url))
  }),

  // memberships
  http.get(o('memberships'), ({ request }) => {
    const url = new URL(request.url)
    const p = url.searchParams
    const asOf = p.get('as_of') ?? today()
    return HttpResponse.json(
      paginate(
        db.memberships.filter((m) => {
          const active = m.valid_from <= asOf && (!m.valid_to || m.valid_to >= asOf)
          return (
            (!p.get('person_id') || m.person_id === p.get('person_id')) &&
            (!p.get('role') || m.role === p.get('role')) &&
            (!p.get('scope_type') || m.scope_type === p.get('scope_type')) &&
            (!p.get('scope_id') || m.scope_id === p.get('scope_id')) &&
            (p.get('active') === null || String(active) === p.get('active'))
          )
        }),
        url,
      ),
    )
  }),
  http.post(o('memberships'), async ({ request }) => {
    const body = (await request.json()) as {
      person_id: string
      role: string
      scope_type: string
      scope_id?: string | null
      valid_from: string
      valid_to?: string | null
      site_owned?: boolean
    }
    if (body.scope_type !== 'org' && !body.scope_id)
      return fieldError('scope_id', 'required', 'Choose the site or block this applies to')
    const site_owned = body.site_owned ?? (body.role === 'board_member' || body.role === 'auditor')
    const m = {
      id: newId('mem'),
      org_id: ORG,
      person_id: body.person_id,
      person_display_name: personName(body.person_id),
      role: body.role,
      scope_type: body.scope_type,
      scope_id: body.scope_id ?? null,
      valid_from: body.valid_from,
      valid_to: body.valid_to ?? null,
      decision_id: null,
      site_owned,
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    }
    db.memberships.push(m as never)
    return HttpResponse.json(m, { status: 201 })
  }),
  http.patch(o('memberships/:memId'), async ({ params, request }) => {
    const m = db.memberships.find((x) => x.id === params.memId)
    if (!m) return notFound()
    Object.assign(m, await request.json(), { version: m.version + 1 })
    return HttpResponse.json(m)
  }),
  http.delete(o('memberships/:memId'), ({ params }) => {
    const m = db.memberships.find((x) => x.id === params.memId)
    if (m?.site_owned) {
      const r = requireReauth()
      if (r) return r
    }
    db.memberships = db.memberships.filter((x) => x.id !== params.memId)
    return new HttpResponse(null, { status: 204 })
  }),

  // invitations
  http.get(o('invitations'), ({ request }) => {
    const url = new URL(request.url)
    const p = url.searchParams
    return HttpResponse.json(
      paginate(
        db.invitations.filter(
          (i) =>
            (!p.get('kind') || i.kind === p.get('kind')) &&
            (!p.get('status') || i.status === p.get('status')) &&
            (!p.get('person_id') || i.person_id === p.get('person_id')) &&
            (!p.get('unit_id') || i.unit_id === p.get('unit_id')),
        ),
        url,
      ),
    )
  }),
  http.post(o('invitations'), async ({ request }) => {
    const body = (await request.json()) as {
      site_id: string
      person_id: string
      channel: 'sms' | 'email'
      unit_id?: string | null
      destination?: string
    }
    const person = db.persons.find((x) => x.id === body.person_id)
    if (!person) return fieldError('person_id', 'not_found', 'Person not found')
    const destination =
      body.destination ?? (body.channel === 'sms' ? person.contact.phone : person.contact.email)
    if (!destination)
      return fieldError(
        'destination',
        'required',
        body.channel === 'sms'
          ? 'This person has no phone number'
          : 'This person has no email address',
      )
    const inv = {
      id: newId('inv'),
      org_id: ORG,
      kind: 'manager',
      site_id: body.site_id,
      person_id: body.person_id,
      unit_id: body.unit_id ?? null,
      channel: body.channel,
      destination,
      status: 'pending',
      expires_at: new Date(Date.now() + 7 * 86400_000).toISOString(),
      accepted_at: null,
      invited_by: 'person-1',
      created_at: nowIso(),
      updated_at: nowIso(),
    }
    db.invitations.push(inv as never)
    person.join_status = person.join_status === 'joined' ? 'joined' : 'invited'
    return HttpResponse.json(inv, { status: 201 })
  }),
  http.post(o('invitations/:invId\\:resend'), ({ params }) => {
    const i = db.invitations.find((x) => x.id === params.invId)
    if (!i) return notFound()
    i.status = 'pending'
    i.expires_at = new Date(Date.now() + 7 * 86400_000).toISOString()
    return HttpResponse.json(i)
  }),
  http.delete(o('invitations/:invId'), ({ params }) => {
    const i = db.invitations.find((x) => x.id === params.invId)
    if (i) i.status = 'revoked'
    return new HttpResponse(null, { status: 204 })
  }),

  // household (me.*): the caller's own unit
  http.get(o('me/household-invitations'), ({ request }) => {
    const unit = new URL(request.url).searchParams.get('unit_id')
    return HttpResponse.json(
      paginate(
        db.invitations.filter((i) => i.kind === 'household' && (!unit || i.unit_id === unit)),
        new URL(request.url),
      ),
    )
  }),
  http.delete(o('me/household-invitations/:invId'), ({ params }) => {
    const i = db.invitations.find((x) => x.id === params.invId && x.kind === 'household')
    if (!i) return notFound()
    i.status = 'revoked'
    return new HttpResponse(null, { status: 204 })
  }),
  http.delete(o('me/household-occupancies/:occId'), ({ params }) => {
    const oc = db.occupancies.find((x) => x.id === params.occId)
    if (!oc) return notFound()
    if (oc.kind !== 'household' || oc.valid_to)
      return problem(409, 'conflict', 'Not an active household occupancy.')
    oc.valid_to = today()
    recomputeOccupancy(db)
    return new HttpResponse(null, { status: 204 })
  }),
  http.post(o('units/:unitId/household-invitations'), async ({ params, request }) => {
    const body = (await request.json()) as {
      display_name: string
      channel: 'sms' | 'email'
      destination: string
    }
    const inv = {
      id: newId('inv'),
      org_id: ORG,
      kind: 'household',
      site_id: SITE,
      person_id: null,
      unit_id: String(params.unitId),
      channel: body.channel,
      destination: body.destination,
      status: 'pending',
      expires_at: new Date(Date.now() + 7 * 86400_000).toISOString(),
      accepted_at: null,
      invited_by: 'person-1',
      created_at: nowIso(),
      updated_at: nowIso(),
    }
    db.invitations.push(inv as never)
    return HttpResponse.json(inv, { status: 201 })
  }),

  // join code
  http.get(o('sites/:siteId/join-code'), () => HttpResponse.json(db.joinCode)),
  http.put(o('sites/:siteId/join-code'), async ({ request }) => {
    const body = (await request.json()) as { enabled: boolean }
    db.joinCode = { ...db.joinCode, enabled: body.enabled }
    return HttpResponse.json(db.joinCode)
  }),
  http.post(o('sites/:siteId/join-code\\:rotate'), () => {
    const code = `GUNES-${Math.random().toString(36).slice(2, 6).toUpperCase()}`
    db.joinCode = {
      ...db.joinCode,
      code,
      url: `http://localhost/join/${code}`,
      rotated_at: nowIso(),
    }
    return HttpResponse.json(db.joinCode)
  }),

  // unit claims
  http.get(o('unit-claims'), ({ request }) => {
    const url = new URL(request.url)
    const p = url.searchParams
    return HttpResponse.json(
      paginate(
        db.claims.filter(
          (c) =>
            (!p.get('status') || c.status === p.get('status')) &&
            (!p.get('unit_id') || c.unit_id === p.get('unit_id')),
        ),
        url,
      ),
    )
  }),
  http.post(o('unit-claims/:claimId\\:approve'), async ({ params, request }) => {
    const c = db.claims.find((x) => x.id === params.claimId)
    if (!c) return notFound()
    if (c.status !== 'pending') return problem(409, 'conflict', 'This claim was already decided.')
    const body = (await request.json().catch(() => ({}))) as {
      share_pct?: number
      valid_from?: string
      note?: string
    }
    if (body.share_pct !== undefined && (body.share_pct <= 0 || body.share_pct > 100))
      return fieldError('share_pct', 'out_of_range', 'Enter a share between 1 and 100')
    Object.assign(c, {
      status: 'approved',
      decided_by: 'person-1',
      decided_at: nowIso(),
      decision_note: body.note ?? null,
    })
    const pid = newId('person')
    db.persons.push({
      id: pid,
      org_id: ORG,
      user_id: c.user_id,
      display_name: c.display_name,
      contact: { email: null, phone: null, preferred_channel: null, address: null },
      national_id_masked: null,
      national_id_country: null,
      gate_contact_phone: null,
      notes: null,
      join_status: 'joined',
      directory_consent: [],
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    })
    db.occupancies.push({
      id: newId('occ'),
      org_id: ORG,
      person_id: pid,
      person_display_name: c.display_name,
      unit_id: c.unit_id,
      unit_door_number: c.unit_door_number,
      kind: c.claimed_role,
      side: c.claimed_role === 'tenant' ? 'tenant' : 'owner',
      share_pct: body.share_pct ?? null,
      is_correspondent: false,
      may_see_money: c.claimed_role !== 'household',
      valid_from: body.valid_from ?? today(),
      valid_to: null,
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    })
    recomputeOccupancy(db)
    return HttpResponse.json(c)
  }),
  http.post(o('unit-claims/:claimId\\:reject'), async ({ params, request }) => {
    const c = db.claims.find((x) => x.id === params.claimId)
    if (!c) return notFound()
    const body = (await request.json()) as { reason: string }
    if (!body.reason?.trim()) return fieldError('reason', 'required', 'Give a reason')
    Object.assign(c, {
      status: 'rejected',
      decided_by: 'person-1',
      decided_at: nowIso(),
      decision_note: body.reason,
    })
    return HttpResponse.json(c)
  }),

  // directory
  http.get(o('directory'), ({ request }) => {
    const url = new URL(request.url)
    const p = url.searchParams
    const entries = db.persons
      .map((pe) => ({
        person_id: pe.id,
        display_name: pe.display_name,
        units: db.occupancies
          .filter((x) => x.person_id === pe.id && !x.valid_to)
          .map((x) => ({ unit_id: x.unit_id, door_number: x.unit_door_number, kind: x.kind })),
        phone: pe.contact.phone,
        email: pe.contact.email,
      }))
      .filter(
        (e) =>
          e.units.length > 0 &&
          matches(p.get('q'), e.display_name) &&
          (!p.get('unit_id') || e.units.some((u) => u.unit_id === p.get('unit_id'))),
      )
    return HttpResponse.json(paginate(entries, url))
  }),

  // vendors
  http.get(o('vendors'), ({ request }) => {
    const url = new URL(request.url)
    const p = url.searchParams
    return HttpResponse.json(
      paginate(
        db.vendors.filter(
          (v) =>
            matches(p.get('q'), v.name, v.tax_id) &&
            (p.get('archived') === null || String(v.archived) === p.get('archived')),
        ),
        url,
      ),
    )
  }),
  http.post(o('vendors'), async ({ request }) => {
    const body = (await request.json()) as {
      name: string
      tax_id?: string | null
      contact?: Record<string, unknown>
    }
    if (!body.name?.trim()) return fieldError('name', 'required', 'Name is required')
    const v = {
      id: newId('vendor'),
      org_id: ORG,
      name: body.name.trim(),
      tax_id: body.tax_id ?? null,
      contact: {
        email: null,
        phone: null,
        preferred_channel: null,
        address: null,
        ...(body.contact ?? {}),
      },
      default_expense_category_id: null,
      archived: false,
      created_at: nowIso(),
      updated_at: nowIso(),
      version: 1,
    }
    db.vendors.push(v as never)
    return HttpResponse.json(v, { status: 201 })
  }),
  http.patch(o('vendors/:vendorId'), async ({ params, request }) => {
    const v = db.vendors.find((x) => x.id === params.vendorId)
    if (!v) return notFound()
    const conflict = versionConflict(request, v.version)
    if (conflict) return conflict
    Object.assign(v, await request.json(), { version: v.version + 1 })
    return HttpResponse.json(v)
  }),

  // roles
  http.get(o('roles'), () =>
    HttpResponse.json({
      roles: [
        {
          role: 'org_admin',
          description: 'Runs the organization',
          permissions: ['org.manage', 'sites.manage'],
        },
        {
          role: 'site_manager',
          description: 'Runs a site',
          permissions: ['sites.read', 'units.manage', 'people.manage'],
        },
      ],
    }),
  ),
]
