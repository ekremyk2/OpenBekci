import type { S } from './kit/api'

/**
 * Pure rules of the governance screens: quorum display, proxy caps, threshold evaluation and
 * delivery progress. The server is authoritative; these compute what the screen explains.
 */

export type Settings = S['GovernanceSettingsSet']

// ---------------------------------------------------------------- quorum

export interface QuorumView {
  call: 'first' | 'second'
  /** "3 of 6 owners" numbers */
  presentCount: number
  eligibleCount: number
  /** share of the land (arsa payi) present, in percent of the eligible share */
  presentSharePct: number
  countPct: number
  requiredCountPct: number | null
  requiredSharePct: number | null
  /** Does the number criterion hold ("more than" the required percentage)? null when none applies. */
  countOk: boolean | null
  shareOk: boolean | null
  reached: boolean
  /** Owners still needed to pass the number criterion (0 when none are needed). */
  moreOwnersNeeded: number | null
}

const pct = (part: number, whole: number) => (whole > 0 ? (part / whole) * 100 : 0)

/** Turns the server's quorum numbers into what the desk shows, by number and by land share. */
export function quorumView(q: S['Quorum']): QuorumView {
  const countPct = pct(q.present_count, q.eligible_count)
  const presentSharePct = pct(q.present_share, q.eligible_share)
  const countOk = q.required_by_count_pct === null ? null : countPct > q.required_by_count_pct
  const shareOk =
    q.required_by_share_pct === null ? null : presentSharePct > q.required_by_share_pct
  let moreOwnersNeeded: number | null = null
  if (q.required_by_count_pct !== null) {
    // smallest n with n/eligible > required%
    const needed = Math.floor((q.eligible_count * q.required_by_count_pct) / 100) + 1
    moreOwnersNeeded = Math.max(0, needed - q.present_count)
  }
  return {
    call: q.call,
    presentCount: q.present_count,
    eligibleCount: q.eligible_count,
    presentSharePct,
    countPct,
    requiredCountPct: q.required_by_count_pct,
    requiredSharePct: q.required_by_share_pct,
    countOk,
    shareOk,
    reached: q.reached,
    moreOwnersNeeded,
  }
}

// ---------------------------------------------------------------- owners and proxies

export interface OwnerRow {
  personId: string
  name: string
  units: { id: string; label: string }[]
  /** co-ownership share of the person in the units, when below 100 */
  sharePct: number | null
}

/** Groups owner occupancies into one row per owner with the units they hold. */
export function ownerRows(
  occupancies: Pick<
    S['Occupancy'],
    'person_id' | 'person_display_name' | 'unit_id' | 'unit_door_number' | 'kind' | 'share_pct'
  >[],
): OwnerRow[] {
  const by = new Map<string, OwnerRow>()
  for (const o of occupancies) {
    if (o.kind !== 'owner') continue
    let row = by.get(o.person_id)
    if (!row) {
      row = { personId: o.person_id, name: o.person_display_name, units: [], sharePct: null }
      by.set(o.person_id, row)
    }
    if (!row.units.some((u) => u.id === o.unit_id))
      row.units.push({ id: o.unit_id, label: o.unit_door_number })
    if (o.share_pct !== null && o.share_pct < 100) row.sharePct = o.share_pct
  }
  return [...by.values()].sort((a, b) => a.name.localeCompare(b.name))
}

export type ProxyWarning = 'proxy_cap' | 'max_votes'

/**
 * Warnings for a person who would hold `addUnits` more proxy votes. Law 634 defaults, all from site
 * settings: a person may represent at most `proxy_cap_pct` of all votes (buildings of
 * `small_building_unit_limit` units or fewer: at most `proxy_cap_small_building_max` others), and
 * hold at most `max_votes_per_person_pct` of all votes in total (own units plus proxies).
 */
export function proxyWarnings(args: {
  settings: Settings
  totalUnits: number
  holderOwnUnits: number
  holderProxyUnits: number
  addUnits: number
}): ProxyWarning[] {
  const { settings: s, totalUnits, holderOwnUnits, holderProxyUnits, addUnits } = args
  const warnings: ProxyWarning[] = []
  const proxies = holderProxyUnits + addUnits
  const cap =
    totalUnits <= s.small_building_unit_limit
      ? s.proxy_cap_small_building_max
      : Math.floor((totalUnits * s.proxy_cap_pct) / 100)
  if (proxies > cap) warnings.push('proxy_cap')
  if (
    totalUnits > 0 &&
    ((holderOwnUnits + proxies) / totalUnits) * 100 > s.max_votes_per_person_pct
  )
    warnings.push('max_votes')
  return warnings
}

/** The proxy limit shown next to the picker. */
export function proxyLimit(settings: Settings, totalUnits: number): number {
  return totalUnits <= settings.small_building_unit_limit
    ? settings.proxy_cap_small_building_max
    : Math.floor((totalUnits * settings.proxy_cap_pct) / 100)
}

// ---------------------------------------------------------------- thresholds

export type ThresholdRule = S['DecisionThreshold']['rule']

export interface CountsInput {
  yes: number
  no: number
  abstain: number
  /** land share voting yes, percent of the total (null when not entered) */
  yesSharePct: number | null
}

export interface ThresholdEvaluation {
  /** votes in favour needed by number; null when the rule counts only the present */
  neededYes: number | null
  /** land share in favour needed, percent; null when the rule has no share criterion */
  neededSharePct: number | null
  /** true / false when it can be told from the counts entered, null when the share is missing */
  met: boolean | null
}

/** What a rule demands of in-room counts, and whether the entered counts meet it. */
export function evaluateThreshold(
  rule: ThresholdRule | undefined,
  counts: CountsInput,
  eligibleUnits: number,
): ThresholdEvaluation {
  switch (rule) {
    case 'majority_count_and_share': {
      const neededYes = Math.floor(eligibleUnits / 2) + 1
      const byCount = counts.yes >= neededYes
      const met =
        counts.yesSharePct === null ? (byCount ? null : false) : byCount && counts.yesSharePct > 50
      return { neededYes, neededSharePct: 50, met }
    }
    case 'four_fifths': {
      const neededYes = Math.ceil((eligibleUnits * 4) / 5)
      const byCount = counts.yes >= neededYes
      const met =
        counts.yesSharePct === null ? (byCount ? null : false) : byCount && counts.yesSharePct >= 80
      return { neededYes, neededSharePct: 80, met }
    }
    case 'unanimity':
      return { neededYes: eligibleUnits, neededSharePct: 100, met: counts.yes >= eligibleUnits }
    case 'simple_majority_present':
      return { neededYes: null, neededSharePct: null, met: counts.yes > counts.no }
    default:
      return { neededYes: null, neededSharePct: null, met: null }
  }
}

// ---------------------------------------------------------------- notices

export function noticeProgress(notices: Pick<S['MeetingNotice'], 'status'>[]) {
  const total = notices.length
  const recorded = notices.filter((n) => n.status === 'recorded').length
  return { total, recorded, pct: total === 0 ? 0 : Math.round((recorded / total) * 100) }
}

/** Legal timing of the call, from settings: returns the broken rules (empty when fine). */
export function callTimingIssues(
  settings: Pick<Settings, 'call_min_days' | 'second_call_min_days' | 'second_call_max_days'>,
  scheduledAt: string,
  secondCallAt: string,
  now = new Date(),
): ('call_too_late' | 'second_call_timing')[] {
  const issues: ('call_too_late' | 'second_call_timing')[] = []
  const lead = (Date.parse(scheduledAt) - now.getTime()) / 86_400_000
  const gap = (Date.parse(secondCallAt) - Date.parse(scheduledAt)) / 86_400_000
  if (lead < settings.call_min_days) issues.push('call_too_late')
  if (gap < settings.second_call_min_days || gap > settings.second_call_max_days)
    issues.push('second_call_timing')
  return issues
}

/** Topic of an agenda item to the kind of change a decision on it can apply (G5). */
export function effectKindForTopic(
  topic: string | null | undefined,
): S['DecisionEffect']['kind'] | null {
  switch (topic) {
    case 'dues_rate':
      return 'dues_rate'
    case 'manager_election':
    case 'board_election':
    case 'auditor_election':
      return 'membership'
    case 'budget':
      return 'budget_final'
    case 'special_assessment':
      return 'special_assessment'
    default:
      return null
  }
}
