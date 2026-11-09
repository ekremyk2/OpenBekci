import { createFileRoute, Link, redirect } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { roleLabel } from '@/lib/labels'
import { Building2Icon } from 'lucide-react'
import { requireSession } from '@/app/guards'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

export const Route = createFileRoute('/select-org')({
  beforeLoad: async ({ context, location }) => {
    const me = await requireSession(context, location)
    const only = me.organizations.length === 1 ? me.organizations[0] : undefined
    if (only) throw redirect({ to: '/orgs/$orgId', params: { orgId: only.id }, replace: true })
    if (me.organizations.length === 0 && me.user.is_platform_admin) {
      throw redirect({ to: '/platform', replace: true })
    }
  },
  component: function SelectOrg() {
    const { organizations } = useSession()
    const { i18n } = useLingui()
    return (
      <div className="mx-auto flex min-h-dvh max-w-xl flex-col justify-center gap-4 p-4">
        <h1 className="text-2xl font-semibold">
          <Trans>Choose an organization</Trans>
        </h1>
        {organizations.length === 0 ? (
          <EmptyState
            icon={Building2Icon}
            title={<Trans>You are not part of any organization yet</Trans>}
            description={<Trans>Ask your building manager to invite you.</Trans>}
          />
        ) : (
          <ul className="flex flex-col gap-3">
            {organizations.map((org) => (
              <li key={org.id}>
                <Link
                  to="/orgs/$orgId"
                  params={{ orgId: org.id }}
                  className="block rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
                >
                  <Card className="transition-colors hover:bg-accent">
                    <CardHeader>
                      <CardTitle>{org.name}</CardTitle>
                    </CardHeader>
                    <CardContent className="text-sm text-muted-foreground">
                      {[...new Set(org.roles.map((r) => r.role))]
                        .map((r) => roleLabel(r, i18n))
                        .join(', ')}
                    </CardContent>
                  </Card>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </div>
    )
  },
})
