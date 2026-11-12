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
  const { t } = useLingui()
  // field names and groups the catalog has no label for (the server sends none for these yet)
  const own = useMemo<Record<string, string>>(
    () => ({
      'tr.name': t`Turkey`,
      kvkk: t`KVKK`,
      tr_kvkk_data_controller: t`Data controller`,
      legal_name: t`Legal name`,
      address: t`Address`,
      contact: t`Contact`,
      verbis_no: t`VERBIS number`,
      tr_payments_iyzico_enabled: t`Accept card payments with iyzico`,
      tr_payments_iyzico_api_key: t`API key`,
      tr_payments_iyzico_secret_key: t`Secret key`,
      tr_payments_iyzico_sandbox: t`Sandbox (test mode)`,
      tr_payments_iyzico_callback_base_url: t`Callback base URL`,
      tr_sms_iletimerkezi_enabled: t`Send SMS with İleti Merkezi`,
      tr_sms_iletimerkezi_username: t`Username`,
      tr_sms_iletimerkezi_password: t`Password`,
      tr_sms_iletimerkezi_sender_id: t`Sender ID`,
      tr_bank_formats: t`Bank statement formats`,
    }),
    [t],
  )
  return useCallback((key: string) => map.get(key) ?? own[key], [map, own])
}
