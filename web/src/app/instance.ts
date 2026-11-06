import { queryOptions, useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { DiscoveryDocument } from '@/api/schema'

/** Discovery document (backend §8): auth methods, branding, packs. Unauthenticated. */
export const instanceQueryOptions = queryOptions({
  queryKey: ['instance', 'discovery'],
  queryFn: async (): Promise<DiscoveryDocument> => {
    const { data } = await api.GET('/.well-known/openbekci.json')
    if (!data) throw new Error('Empty discovery document')
    return data
  },
  staleTime: 5 * 60_000,
})

export function useInstance() {
  return useQuery(instanceQueryOptions)
}

/** Auth method ids this build knows how to render; others (e.g. oidc:*) are ignored for now. */
export const KNOWN_AUTH_METHODS = ['password', 'sms_otp', 'magic_link'] as const
export type KnownAuthMethod = (typeof KNOWN_AUTH_METHODS)[number]

/** Known methods in display order: phone (SMS) first when the instance offers it (D5). */
export function supportedAuthMethods(doc: DiscoveryDocument | undefined): KnownAuthMethod[] {
  const ids = new Set((doc?.auth_methods ?? []).map((m) => m.id))
  return KNOWN_AUTH_METHODS.filter((m) => ids.has(m)).sort(
    (a, b) => (a === 'sms_otp' ? -1 : 0) - (b === 'sms_otp' ? -1 : 0),
  )
}
