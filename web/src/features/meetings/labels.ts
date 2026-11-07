import type { MessageDescriptor } from '@lingui/core'
import { msg } from '@lingui/core/macro'
import { useLingui } from '@lingui/react/macro'

/* Labels of server-defined ids; unknown ids fall back to the raw string (open enums). */

const MEETING_STATUS: Record<string, MessageDescriptor> = {
  draft: msg`Draft`,
  called: msg`Called`,
  in_session: msg`In session`,
  closed: msg`Closed`,
  cancelled: msg`Cancelled`,
}
const MEETING_KIND: Record<string, MessageDescriptor> = {
  general_assembly: msg`Ordinary assembly`,
  extraordinary: msg`Extraordinary assembly`,
}
const OUTCOME: Record<string, MessageDescriptor> = {
  accepted: msg`Accepted`,
  rejected: msg`Rejected`,
  no_vote: msg`No vote`,
  noted: msg`Noted`,
  undetermined: msg`Not determined`,
}
const NOTICE_METHOD: Record<string, MessageDescriptor> = {
  registered_mail: msg`Registered mail`,
  signed: msg`Signed by hand`,
  email: msg`Email`,
  sms: msg`SMS`,
  other: msg`Other`,
}
const RULE: Record<string, MessageDescriptor> = {
  simple_majority_present: msg`Majority of those present`,
  majority_count_and_share: msg`Majority by number and by share`,
  four_fifths: msg`Four fifths`,
  unanimity: msg`Unanimity`,
}
const TOPIC: Record<string, MessageDescriptor> = {
  general: msg`General decision`,
  manager_election: msg`Election of the manager`,
  board_election: msg`Election of the board`,
  auditor_election: msg`Election of the auditor`,
  budget: msg`Budget`,
  dues_rate: msg`Dues rate`,
  special_assessment: msg`Special assessment`,
  common_area_change: msg`Change to common areas`,
  change_of_use: msg`Change of use`,
  noting: msg`For information`,
}
const EFFECT: Record<string, MessageDescriptor> = {
  dues_rate: msg`New dues rate`,
  membership: msg`Elected role with a term`,
  budget_final: msg`Budget marked final`,
  special_assessment: msg`Special assessment plan`,
}
const POLL_ANSWER: Record<string, MessageDescriptor> = {
  yes: msg`Yes`,
  no: msg`No`,
  abstain: msg`Abstain`,
}
const ROLE: Record<string, MessageDescriptor> = {
  site_manager: msg`Site manager`,
  board_member: msg`Board member`,
  auditor: msg`Auditor`,
}

export type Variant = 'success' | 'warning' | 'destructive' | 'info' | 'secondary'
export const MEETING_STATUS_VARIANT: Record<string, Variant> = {
  draft: 'secondary',
  called: 'info',
  in_session: 'warning',
  closed: 'success',
  cancelled: 'secondary',
}
export const OUTCOME_VARIANT: Record<string, Variant> = {
  accepted: 'success',
  rejected: 'destructive',
  no_vote: 'secondary',
  noted: 'secondary',
  undetermined: 'secondary',
}

export const TOPIC_IDS = Object.keys(TOPIC)
export const NOTICE_METHODS = ['registered_mail', 'signed', 'email', 'sms'] as const
export const ELECTED_ROLES = Object.keys(ROLE)

/** Translators bound to the active locale. */
export function useGovLabels() {
  const { i18n } = useLingui()
  const pick = (map: Record<string, MessageDescriptor>) => (id: string | null | undefined) => {
    if (!id) return ''
    const d = map[id]
    return d ? i18n._(d) : id
  }
  return {
    meetingStatus: pick(MEETING_STATUS),
    meetingKind: pick(MEETING_KIND),
    outcome: pick(OUTCOME),
    noticeMethod: pick(NOTICE_METHOD),
    rule: pick(RULE),
    topic: pick(TOPIC),
    effect: pick(EFFECT),
    pollAnswer: pick(POLL_ANSWER),
    role: pick(ROLE),
  }
}
