import { lazy, Suspense, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, type Schema } from '@/api/draft'
import { useLocale } from '@/app/locale'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { Field } from '@/components/forms/FormBits'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { toast } from '@/lib/toast'
import { must } from '@/lib/pagination'

// react-markdown is heavy: loaded only when this screen (or an announcement) is opened
const MarkdownView = lazy(() => import('@/features/announcements/Markdown'))

/** "tr.consent_identity" becomes "Consent identity": the codes endpoint has no label for it yet. */
function fallbackTitle(id: string): string {
  const name = id.includes('.') ? id.slice(id.indexOf('.') + 1) : id
  const text = name.replaceAll('_', ' ')
  return text.charAt(0).toUpperCase() + text.slice(1)
}

/** Accept or decline the current version of one text. */
function Decision({ orgId, c, locale }: { orgId: string; c: Schema<'Consent'>; locale: string }) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const decide = useMutation({
    mutationFn: async (verb: 'accept' | 'decline') => {
      const params = { path: { org_id: orgId, purpose: c.id } }
      const body = { version: c.version }
      return verb === 'accept'
        ? must(
            await draftApi.POST('/v1/orgs/{org_id}/privacy/consents/{purpose}:accept', {
              params,
              body,
            }),
          )
        : must(
            await draftApi.POST('/v1/orgs/{org_id}/privacy/consents/{purpose}:decline', {
              params,
              body,
            }),
          )
    },
    onSuccess: async () => {
      toast.info(t`Your choice is saved.`)
      await queryClient.invalidateQueries({ queryKey: ['codes.listConsents', orgId, locale] })
      await queryClient.invalidateQueries({ queryKey: ['me'] })
    },
  })
  return (
    <div className="mt-4 flex flex-wrap items-center gap-2">
      {c.state === 'accepted' ? (
        <Badge variant="success">
          <Trans>Accepted</Trans>
        </Badge>
      ) : c.state === 'declined' ? (
        <Badge variant="secondary">
          <Trans>Declined</Trans>
        </Badge>
      ) : (
        <Badge variant="outline">
          {c.required ? <Trans>Needs your answer</Trans> : <Trans>No answer yet</Trans>}
        </Badge>
      )}
      <Button
        size="sm"
        disabled={decide.isPending || c.state === 'accepted'}
        onClick={() => decide.mutate('accept')}
      >
        <Trans>Accept</Trans>
      </Button>
      <Button
        size="sm"
        variant="outline"
        disabled={decide.isPending || c.state === 'declined'}
        onClick={() => decide.mutate('decline')}
      >
        <Trans>Decline</Trans>
      </Button>
    </div>
  )
}

/** Asks for an export of my own data; it arrives in my documents and as a notification. */
function MyDataExport({ orgId, personId }: { orgId: string; personId: string }) {
  const { t } = useLingui()
  const ask = useMutation({
    mutationFn: async () =>
      must(
        await draftApi.POST('/v1/orgs/{org_id}/persons/{person_id}/data-export', {
          params: { path: { org_id: orgId, person_id: personId } },
        }),
      ),
    onSuccess: () =>
      toast.info(t`Your export is being prepared. You will be notified when it is ready.`),
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>My data</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col items-start gap-3 text-sm">
        <p className="max-w-prose text-muted-foreground">
          <Trans>
            Ask for a copy of the data this organization holds about you. A JSON file and a PDF are
            added to your documents and you get a notification when they are ready.
          </Trans>
        </p>
        <Button variant="outline" disabled={ask.isPending} onClick={() => ask.mutate()}>
          <Trans>Export my data</Trans>
        </Button>
      </CardContent>
    </Card>
  )
}

/**
 * My privacy: the privacy notice and consent texts of the organization (`codes.listConsents`) with
 * my decision on each, per locale with the version a decision is recorded for, and a request for
 * an export of my data.
 */
export function ConsentsPage() {
  const { t } = useLingui()
  const { org } = useSession()
  const { locale } = useLocale()
  const orgId = org?.id ?? ''
  const personId = org?.person_id ?? null
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
              you pick. Accept or decline each one.
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
              <Decision orgId={orgId} c={c} locale={choice} />
            </CardContent>
          </Card>
        ))
      )}
      {personId ? <MyDataExport orgId={orgId} personId={personId} /> : null}
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
