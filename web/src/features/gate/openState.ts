import type { S } from '@/features/meetings/kit/api'

/** An admin-side agent record counts as online when heard from within this many seconds (contract: 90). */
export const AGENT_FRESH_SECONDS = 90

export type OpenBlock =
  'no_permission' | 'point_disabled' | 'agent_offline' | 'point_offline' | 'clock_suspect'

export interface OpenState {
  enabled: boolean
  reason: OpenBlock | null
}

/**
 * The booth console's `GateAgentStatus` carries `online` (judged by the server). The admin agent
 * record does not, so the access screens derive it the way the contract defines it: connected and
 * heard from recently.
 */
export function agentOnline(
  agent: Pick<S['Agent'], 'connected' | 'last_seen'>,
  now: number = Date.now(),
): boolean {
  return (
    agent.connected &&
    agent.last_seen !== null &&
    now - Date.parse(agent.last_seen) <= AGENT_FRESH_SECONDS * 1000
  )
}

/**
 * Whether the manual "open" button of an access point is usable, and if not, why. The server stays
 * authoritative (it answers 409 `agent_offline` or `clock_suspect`); the console only avoids
 * offering a button that cannot work and says why.
 *  - `canOpen`: the caller holds `access.open`
 *  - `point`: `enabled`, and from the booth console `online` (controller reachable) and `clock_suspect`
 *  - `agent`: `online` and `clock_suspect` of the agent; null means no agent is known
 *  - `clockSuspect`: learned from a 409 `clock_suspect`
 */
export function manualOpenState(args: {
  canOpen: boolean
  point: { enabled: boolean; online?: boolean; clock_suspect?: boolean }
  agent: Pick<S['GateAgentStatus'], 'online' | 'clock_suspect'> | null
  clockSuspect?: boolean
}): OpenState {
  if (!args.canOpen) return { enabled: false, reason: 'no_permission' }
  if (!args.point.enabled) return { enabled: false, reason: 'point_disabled' }
  if (args.agent === null || !args.agent.online) return { enabled: false, reason: 'agent_offline' }
  if (args.point.online === false) return { enabled: false, reason: 'point_offline' }
  if (args.clockSuspect || args.agent.clock_suspect || args.point.clock_suspect)
    return { enabled: false, reason: 'clock_suspect' }
  return { enabled: true, reason: null }
}
