import { useCallback, useMemo } from 'react'
import { queryOptions, useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import { draftApi, type Schema } from '@/api/draft'
import { useSession } from '@/app/session'
import { must } from '@/lib/pagination'
import type { JsonSchema } from '@/components/schema-form/schema'

export type Pack = Schema<'Pack'>

export const packQueries = {
  list: (orgId: string) =>
    queryOptions({
      queryKey: ['packs.list', orgId],
      queryFn: async () =>
        must(await draftApi.GET('/v1/orgs/{org_id}/packs', { params: { path: { org_id: orgId } } }))
          .items,
    }),
}

/** The settings schema of one scope, or null when the pack has none there. */
export function schemaOf(pack: Pack, scope: 'org' | 'site'): JsonSchema | null {
  const s = (pack.settings_schemas as Record<string, unknown>)[scope]
  return s && typeof s === 'object' ? (s as JsonSchema) : null
}

/**
 * Labels of pack-provided keys (field labels, help texts, group titles): merged by the server into
 * `GET /v1/orgs/{org}/codes?locale=` (plan 04 §3.9). Every code of every enum is looked up; a key
 * without a label resolves to undefined and the form falls back to the schema's own text.
 */
export function usePackLabels(): (key: string) => string | undefined {
  const { org, can } = useSession()
  const { i18n } = useLingui()
  const orgId = org?.id ?? ''
  const locale = i18n.locale
  const query = useQuery({
    queryKey: ['codes.listCodes', orgId, locale],
    enabled: !!orgId && can('codes.read'),
    staleTime: 5 * 60_000,
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/codes', {
          params: { path: { org_id: orgId }, query: { locale } },
        }),
      ),
  })
  const map = useMemo(() => {
    const m = new Map<string, string>()
    const data = query.data
    if (!data) return m
    for (const list of Object.values(data.enums)) for (const c of list) m.set(c.code, c.label)
    return m
  }, [query.data])
  return useCallback((key: string) => map.get(key), [map])
}
