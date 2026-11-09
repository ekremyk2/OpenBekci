import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { CheckIcon, CopyIcon } from 'lucide-react'
import { draftApi, type Schema } from '@/api/draft'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { SchemaForm } from '@/components/schema-form/SchemaForm'
import type { JsonSchema, Values } from '@/components/schema-form/schema'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { must } from '@/lib/pagination'
import { toast } from '@/lib/toast'

type Provider = Schema<'ChatProviderSettings'>

/** Bot credentials of the core chat providers (Telegram, WhatsApp); needs `chat.manage`. */
export function ChatSettingsPage() {
  const { org } = useSession()
  const orgId = org?.id ?? ''
  const query = useQuery({
    queryKey: ['operations.listChatSettings', orgId],
    enabled: !!orgId,
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/chat-settings', {
          params: { path: { org_id: orgId } },
        }),
      ).items,
  })
  if (query.isPending) return <PageSkeleton />
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  return (
    <div className="flex flex-col gap-6">
      {query.data.map((p) => (
        <ProviderCard key={p.provider} orgId={orgId} p={p} />
      ))}
    </div>
  )
}

function ProviderCard({ orgId, p }: { orgId: string; p: Provider }) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const [copied, setCopied] = useState(false)
  const name =
    p.provider === 'telegram' ? 'Telegram' : p.provider === 'whatsapp' ? 'WhatsApp' : p.provider
  const save = useMutation({
    gcTime: 0, // the variables hold secrets typed in the form: keep them out of the cache
    mutationFn: async (values: Values) =>
      must(
        await draftApi.PUT('/v1/orgs/{org_id}/chat-settings/{provider}', {
          params: { path: { org_id: orgId, provider: p.provider } },
          body: { values },
        }),
      ),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['operations.listChatSettings', orgId] })
      toast.info(t`Settings saved.`)
    },
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center gap-2">
          {name}
          {p.configured ? (
            <Badge variant="success">
              <Trans>Configured</Trans>
            </Badge>
          ) : (
            <Badge variant="secondary">
              <Trans>Not configured</Trans>
            </Badge>
          )}
        </CardTitle>
        <CardDescription>
          {p.provider === 'telegram' ? (
            <Trans>
              After saving, the operator registers the address below with{' '}
              <code>openbekci admin chat set-webhook</code>.
            </Trans>
          ) : (
            <Trans>
              Enter the address below and the verify token in the webhook settings of your Meta app.
            </Trans>
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="font-medium">
            <Trans>Webhook address</Trans>
          </span>
          <code className="break-all rounded bg-muted px-2 py-1 text-xs" dir="ltr">
            {p.webhook_url}
          </code>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              void navigator.clipboard?.writeText(p.webhook_url).then(() => setCopied(true))
            }}
          >
            {copied ? <CheckIcon /> : <CopyIcon />}
            {copied ? t`Copied` : t`Copy`}
          </Button>
        </div>
        <SchemaForm
          key={p.secrets_set.join(',')}
          schema={p.schema as JsonSchema}
          values={p.values as Values}
          secretsSet={p.secrets_set}
          idPrefix={`chat-${p.provider}`}
          busy={save.isPending}
          error={save.error}
          submitLabel={<Trans>Save settings</Trans>}
          onSubmit={async (values) => {
            await save.mutateAsync(values)
            save.reset() // drops the submitted variables, secrets included
          }}
        />
      </CardContent>
    </Card>
  )
}
