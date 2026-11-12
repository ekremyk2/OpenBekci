import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
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
import { copyText } from '@/lib/clipboard'

type Provider = Schema<'ChatProviderSettings'>

/** The server sends English titles and hints in the provider schemas; these are their translations. */
const SCHEMA_TEXT: Record<string, MessageDescriptor> = {
  'Bot username': msg`Bot username`,
  "The bot's username without @ (for the t.me link).": msg`The bot's username without @ (for the t.me link).`,
  'Bot token': msg`Bot token`,
  'From @BotFather.': msg`From @BotFather.`,
  'Webhook secret': msg`Webhook secret`,
  '16 to 256 letters, digits, - or _. Telegram sends it back in a header on every update.': msg`16 to 256 letters, digits, - or _. Telegram sends it back in a header on every update.`,
  'Business number': msg`Business number`,
  'The WhatsApp number people write to, international format (for the wa.me link).': msg`The WhatsApp number people write to, international format (for the wa.me link).`,
  'Phone number id': msg`Phone number id`,
  "From the Meta app's WhatsApp setup.": msg`From the Meta app's WhatsApp setup.`,
  'Access token': msg`Access token`,
  'App secret': msg`App secret`,
  'Signs the webhook (X-Hub-Signature-256).': msg`Signs the webhook (X-Hub-Signature-256).`,
  'Webhook verify token': msg`Webhook verify token`,
  "The token you enter in Meta's webhook setup.": msg`The token you enter in Meta's webhook setup.`,
}

function localize(schema: JsonSchema, tr: (d: MessageDescriptor) => string): JsonSchema {
  const one = (x: string | undefined) => (x && SCHEMA_TEXT[x] ? tr(SCHEMA_TEXT[x]) : x)
  const properties = Object.fromEntries(
    Object.entries(schema.properties ?? {}).map(([k, v]) => [
      k,
      { ...v, title: one(v.title), description: one(v.description) },
    ]),
  )
  return { ...schema, properties }
}

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
  const { t, i18n } = useLingui()
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
            {p.webhook_url.replace(/\/orgs\/[0-9a-f-]{36}/i, '/orgs/…')}
          </code>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              void copyText(p.webhook_url).then((ok) => ok && setCopied(true))
            }}
          >
            {copied ? <CheckIcon /> : <CopyIcon />}
            {copied ? t`Copied` : t`Copy`}
          </Button>
        </div>
        <SchemaForm
          key={p.secrets_set.join(',')}
          schema={localize(p.schema as JsonSchema, (d) => i18n._(d))}
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
