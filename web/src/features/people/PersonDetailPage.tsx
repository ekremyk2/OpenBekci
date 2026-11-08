import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { PageHeader } from '@/components/layout/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { InvitationsPanel } from './InvitationsPanel'
import { joinStatusLabel, joinStatusVariant } from './labels'
import { MembershipsTab } from './MembershipsTab'
import { NationalIdPanel } from './NationalIdPanel'
import { OccupanciesTab } from './OccupanciesTab'
import type { PersonTab } from './tabs'
import { PersonForm } from './PersonForm'
import { personQueries } from './queries'

export function PersonDetailPage({ personId, tab }: { personId: string; tab: PersonTab }) {
  const { org, can } = useSession()
  const { t, i18n } = useLingui()
  const navigate = useNavigate()
  const orgId = org?.id ?? ''
  const query = useQuery({ ...personQueries.one(orgId, personId), enabled: !!org })
  if (query.isLoading) return <PageSkeleton />
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const person = query.data!
  const sites = org?.sites ?? []
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={
          <span className="flex items-center gap-3">
            {person.display_name}
            <Badge variant={joinStatusVariant(person.join_status)}>
              {joinStatusLabel(person.join_status, i18n)}
            </Badge>
          </span>
        }
        crumbs={[{ label: t`People`, to: '/orgs/$orgId/people' }, { label: person.display_name }]}
      />
      <Tabs
        value={tab}
        onValueChange={(v) => void navigate({ to: '.', search: { tab: v as PersonTab } as never })}
      >
        <TabsList className="flex-wrap">
          <TabsTrigger value="profile">{t`Profile`}</TabsTrigger>
          {can('occupancies.read') ? <TabsTrigger value="units">{t`Units`}</TabsTrigger> : null}
          {can('memberships.read') ? <TabsTrigger value="roles">{t`Roles`}</TabsTrigger> : null}
          {can('invitations.manage') ? (
            <TabsTrigger value="invitations">{t`Invitations`}</TabsTrigger>
          ) : null}
        </TabsList>
        <TabsContent value="profile" className="grid gap-8 pt-4">
          <PersonForm orgId={orgId} person={person} readOnly={!can('people.manage')} />
          {can('people.national_id') ? <NationalIdPanel orgId={orgId} person={person} /> : null}
          <section className="grid max-w-2xl gap-2" aria-labelledby="dir-h">
            <h2 id="dir-h" className="text-base font-semibold">
              <Trans>Directory</Trans>
            </h2>
            <p className="text-sm text-muted-foreground">
              <Trans>
                Whether this person appears in a site&apos;s resident directory. They choose this
                themselves under My account.
              </Trans>
            </p>
            <ul className="divide-y rounded-md border text-sm">
              {sites.map((s) => {
                const c = person.directory_consent.find((x) => x.site_id === s.id)
                return (
                  <li key={s.id} className="flex items-center justify-between gap-2 p-3">
                    <span>{s.name}</span>
                    {c?.consent ? (
                      <Badge variant="success">
                        {c.show_phone ? t`Listed with phone` : t`Listed`}
                      </Badge>
                    ) : (
                      <Badge variant="secondary">
                        <Trans>Not listed</Trans>
                      </Badge>
                    )}
                  </li>
                )
              })}
            </ul>
          </section>
        </TabsContent>
        {can('occupancies.read') ? (
          <TabsContent value="units" className="pt-4">
            <OccupanciesTab orgId={orgId} personId={personId} canEdit={can('occupancies.manage')} />
          </TabsContent>
        ) : null}
        {can('memberships.read') ? (
          <TabsContent value="roles" className="pt-4">
            <MembershipsTab orgId={orgId} personId={personId} canEdit={can('memberships.manage')} />
          </TabsContent>
        ) : null}
        {can('invitations.manage') ? (
          <TabsContent value="invitations" className="pt-4">
            <InvitationsPanel orgId={orgId} personId={personId} />
          </TabsContent>
        ) : null}
      </Tabs>
    </div>
  )
}
