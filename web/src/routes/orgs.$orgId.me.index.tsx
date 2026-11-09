import { createFileRoute } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { occupancyKindLabel } from '@/lib/labels'
import { useSession } from '@/app/session'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { HouseholdPanel } from '@/features/people/HouseholdPanel'

export const Route = createFileRoute('/orgs/$orgId/me')({
  component: function MyAccount() {
    const { me, org } = useSession()
    const { i18n } = useLingui()
    if (!me) return null
    const units = me.occupancies.filter((o) => o.organization_id === org?.id)
    return (
      <div className="flex flex-col gap-6">
        <h1 className="text-2xl font-semibold">
          <Trans>My account</Trans>
        </h1>
        <Card>
          <CardHeader>
            <CardTitle>{me.user.display_name}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-1 text-sm text-muted-foreground">
            {me.user.email ? <span>{me.user.email}</span> : null}
            {me.user.phone ? <span dir="ltr">{me.user.phone}</span> : null}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>
              <Trans>My units</Trans>
            </CardTitle>
          </CardHeader>
          <CardContent>
            {units.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                <Trans>You are not linked to a unit in this organization.</Trans>
              </p>
            ) : (
              <ul className="flex flex-col gap-2">
                {units.map((o) => (
                  <li key={o.id} className="flex items-center gap-2 text-sm">
                    <span>{o.unit_label}</span>
                    <Badge variant="secondary">{occupancyKindLabel(o.kind, i18n)}</Badge>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
        <HouseholdPanel />
      </div>
    )
  },
})
