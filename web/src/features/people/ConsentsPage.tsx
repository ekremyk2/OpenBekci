import { lazy, Suspense, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { useLocale } from '@/app/locale'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { Field } from '@/components/forms/FormBits'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { must } from '@/lib/pagination'

// react-markdown is heavy: loaded only when this screen (or an announcement) is opened
const MarkdownView = lazy(() => import('@/features/announcements/Markdown'))

/** "tr.consent_identity" becomes "Consent identity": the codes endpoint has no label for it yet. */
function fallbackTitle(id: string): string {
  const name = id.includes('.') ? id.slice(id.indexOf('.') + 1) : id
  const text = name.replaceAll('_', ' ')
  return text.charAt(0).toUpperCase() + text.slice(1)
}

/**
 * The privacy notice and consent texts of the organization (`codes.listConsents`), per locale
 * with the version clients record acceptance of. Display only: the contract has no operation to
 * accept or decline a text yet, so this page cannot record a choice.
 */
export function ConsentsPage() {
  const { t } = useLingui()
  const { org } = useSession()
  const { locale } = useLocale()
  const orgId = org?.id ?? ''
  const [choice, setChoice] = useState<'en' | 'tr'>(locale === 'tr' ? 'tr' : 'en')
  const q = useQuery({
    queryKey: ['codes.listConsents', orgId, choice],
    enabled: orgId !== '',
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/privacy/consents', {
          params: { path: { org_id: orgId }, query: { locale: choice } },
        }),
      ),
  })
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="text-2xl font-semibold">
            <Trans>Privacy notice and consents</Trans>
          </h1>
          <p className="max-w-prose text-sm text-muted-foreground">
            <Trans>
              The texts your organization uses to tell you how your data is handled, in the language
              you pick.
            </Trans>
          </p>
        </div>
        <Field id="consent-locale" label={<Trans>Language</Trans>}>
          <Select
            id="consent-locale"
            className="w-auto"
            value={choice}
            onChange={(e) => setChoice(e.target.value === 'tr' ? 'tr' : 'en')}
          >
            <option value="tr">Türkçe</option>
            <option value="en">English</option>
          </Select>
        </Field>
      </div>
      <p role="note" className="rounded-md border bg-muted/40 p-3 text-sm">
        <Trans>
          These texts are shown for reading. Accepting or declining a consent is not possible in the
          web app yet: your manager records it for now.
        </Trans>
      </p>
      {q.isPending ? (
        <PageSkeleton />
      ) : q.isError ? (
        <ErrorState error={q.error} onRetry={() => void q.refetch()} />
      ) : q.data.items.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          <Trans>No privacy texts are published for this organization.</Trans>
        </p>
      ) : (
        q.data.items.map((c) => (
          <Card key={`${c.id}:${c.version}`}>
            <CardHeader>
              <CardTitle className="flex flex-wrap items-center gap-2">
                {c.id === 'core.privacy_notice' || c.id.endsWith('.privacy_notice')
                  ? t`Privacy notice`
                  : fallbackTitle(c.id)}
                <Badge variant="outline">
                  <Trans>Version {c.version}</Trans>
                </Badge>
                {c.pack ? <Badge variant="secondary">{c.pack}</Badge> : null}
              </CardTitle>
            </CardHeader>
            <CardContent>
              <Suspense fallback={<PageSkeleton />}>
                <MarkdownView source={c.markdown} />
              </Suspense>
            </CardContent>
          </Card>
        ))
      )}
      <p className="text-sm">
        <Link
          to="/orgs/$orgId/me"
          params={{ orgId }}
          className="text-primary underline-offset-4 hover:underline"
        >
          <Trans>Back to my account</Trans>
        </Link>
      </p>
    </div>
  )
}
