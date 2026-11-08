import type { S } from '@/features/meetings/kit/api'

/** A guard's agent must have been heard from within this many seconds (contract: 90). */
export const AGENT_FRESH_SECONDS = 90

export type OpenBlock = 'no_permission' | 'point_disabled' | 'agent_offline' | 'clock_suspect'

export interface OpenState {
  enabled: boolean
  reason: OpenBlock | null
}

/**
 * Whether the manual "open" button of an access point is usable, and if not, why. The server stays
 * authoritative (it answers 409 `agent_offline`); the console only avoids offering a button that
 * cannot work and says why.
 *  - `canOpen`: the caller holds `access.open`
 *  - `agent`: the console's agent status (connected + last seen); null means no agent is known
 *  - `clockSuspect`: from the admin agent record when the caller may read it (guards cannot), or
 *    learned from a 409 `clock_suspect`
 */
export function manualOpenState(args: {
  canOpen: boolean
  point: Pick<S['GatePoint'], 'enabled'>
  agent: Pick<S['GateAgentStatus'], 'connected' | 'last_seen_at'> | null
  serverTime: string
  clockSuspect?: boolean
}): OpenState {
  if (!args.canOpen) return { enabled: false, reason: 'no_permission' }
  if (!args.point.enabled) return { enabled: false, reason: 'point_disabled' }
  const a = args.agent
  const fresh =
    a !== null &&
    a.connected &&
    a.last_seen_at !== null &&
    Date.parse(args.serverTime) - Date.parse(a.last_seen_at) <= AGENT_FRESH_SECONDS * 1000
  if (!fresh) return { enabled: false, reason: 'agent_offline' }
  if (args.clockSuspect) return { enabled: false, reason: 'clock_suspect' }
  return { enabled: true, reason: null }
}
