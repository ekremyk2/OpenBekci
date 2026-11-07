import { describe, expect, it } from 'vitest'
import {
  callTimingIssues,
  effectKindForTopic,
  evaluateThreshold,
  noticeProgress,
  ownerRows,
  proxyWarnings,
  quorumView,
  type Settings,
} from './logic'

const settings: Settings = {
  quorum: { first_call_by_count_pct: 50, first_call_by_share_pct: 50, second_call_requires_quorum: false },
  thresholds: [],
  max_votes_per_person_pct: 33.33,
  proxy_cap_pct: 5,
  proxy_cap_small_building_max: 2,
  small_building_unit_limit: 40,
  call_min_days: 15,
  second_call_min_days: 7,
  second_call_max_days: 15,
  term_reminder_days: 60,
}

describe('quorumView', () => {
  const base = {
    meeting_id: 'm',
    call: 'first' as const,
    eligible_count: 6,
    eligible_share: 100,
    required_by_count_pct: 50,
    required_by_share_pct: 50,
    as_of: '2026-01-01T00:00:00Z',
  }
  it('needs MORE than half by number and by share on the first call', () => {
    const v = quorumView({ ...base, present_count: 3, present_share: 55, reached: false })
    expect(v.countPct).toBe(50)
    expect(v.countOk).toBe(false) // exactly half is not a majority
    expect(v.shareOk).toBe(true)
    expect(v.moreOwnersNeeded).toBe(1)
  })
  it('reports met once both hold', () => {
    const v = quorumView({ ...base, present_count: 4, present_share: 70, reached: true })
    expect(v.countOk).toBe(true)
    expect(v.moreOwnersNeeded).toBe(0)
    expect(v.presentSharePct).toBe(70)
  })
  it('has no criteria on the second call', () => {
    const v = quorumView({ ...base, call: 'second', required_by_count_pct: null, required_by_share_pct: null, present_count: 1, present_share: 10, reached: true })
    expect(v.countOk).toBeNull()
    expect(v.moreOwnersNeeded).toBeNull()
  })
})

describe('proxyWarnings', () => {
  it('caps proxies at the small-building maximum', () => {
    const args = { settings: { ...settings, max_votes_per_person_pct: 100 }, totalUnits: 6, holderOwnUnits: 0, holderProxyUnits: 1 }
    expect(proxyWarnings({ ...args, addUnits: 1 })).toEqual([]) // 2 proxies: at the limit
    expect(proxyWarnings({ ...args, addUnits: 2 })).toContain('proxy_cap') // 3 proxies
  })
  it('caps a big building by percent of all votes', () => {
    const w = proxyWarnings({ settings, totalUnits: 100, holderOwnUnits: 1, holderProxyUnits: 5, addUnits: 1 })
    expect(w).toContain('proxy_cap') // 5% of 100 is 5
  })
  it('warns when one person would hold more than a third of the votes', () => {
    const w = proxyWarnings({ settings, totalUnits: 6, holderOwnUnits: 1, holderProxyUnits: 1, addUnits: 1 })
    expect(w).toContain('max_votes') // 3 of 6
  })
})

describe('evaluateThreshold', () => {
  it('majority by number and share', () => {
    expect(evaluateThreshold('majority_count_and_share', { yes: 4, no: 1, abstain: 0, yesSharePct: 60 }, 6).met).toBe(true)
    expect(evaluateThreshold('majority_count_and_share', { yes: 4, no: 1, abstain: 0, yesSharePct: 45 }, 6).met).toBe(false)
    expect(evaluateThreshold('majority_count_and_share', { yes: 4, no: 1, abstain: 0, yesSharePct: null }, 6).met).toBeNull()
    expect(evaluateThreshold('majority_count_and_share', { yes: 3, no: 1, abstain: 0, yesSharePct: null }, 6).neededYes).toBe(4)
  })
  it('four fifths and unanimity', () => {
    expect(evaluateThreshold('four_fifths', { yes: 5, no: 1, abstain: 0, yesSharePct: 85 }, 6).met).toBe(true)
    expect(evaluateThreshold('four_fifths', { yes: 4, no: 2, abstain: 0, yesSharePct: 90 }, 6).met).toBe(false)
    expect(evaluateThreshold('unanimity', { yes: 6, no: 0, abstain: 0, yesSharePct: 100 }, 6).met).toBe(true)
  })
  it('simple majority of those present', () => {
    expect(evaluateThreshold('simple_majority_present', { yes: 2, no: 1, abstain: 3, yesSharePct: null }, 6).met).toBe(true)
  })
})

describe('call timing and misc', () => {
  const now = new Date('2026-03-01T00:00:00Z')
  it('flags a late call and a second call outside the window', () => {
    expect(callTimingIssues(settings, '2026-03-10T18:00:00Z', '2026-03-20T18:00:00Z', now)).toEqual(['call_too_late'])
    expect(callTimingIssues(settings, '2026-03-20T18:00:00Z', '2026-04-20T18:00:00Z', now)).toEqual(['second_call_timing'])
    expect(callTimingIssues(settings, '2026-03-20T18:00:00Z', '2026-03-30T18:00:00Z', now)).toEqual([])
  })
  it('counts delivery progress', () => {
    expect(noticeProgress([{ status: 'recorded' }, { status: 'pending' }, { status: 'recorded' }, { status: 'pending' }])).toEqual({ total: 4, recorded: 2, pct: 50 })
    expect(noticeProgress([]).pct).toBe(0)
  })
  it('maps topics to effects and groups owners', () => {
    expect(effectKindForTopic('manager_election')).toBe('membership')
    expect(effectKindForTopic('general')).toBeNull()
    const rows = ownerRows([
      { person_id: 'p', person_display_name: 'P', unit_id: 'u1', unit_door_number: 'A1', kind: 'owner', share_pct: 100 },
      { person_id: 'p', person_display_name: 'P', unit_id: 'u2', unit_door_number: 'A2', kind: 'owner', share_pct: 100 },
      { person_id: 't', person_display_name: 'T', unit_id: 'u1', unit_door_number: 'A1', kind: 'tenant', share_pct: null },
    ])
    expect(rows).toHaveLength(1)
    expect(rows[0]!.units.map((u) => u.label)).toEqual(['A1', 'A2'])
  })
})
