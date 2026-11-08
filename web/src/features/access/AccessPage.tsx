import { useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useSiteCtx } from '@/features/meetings/kit/api'
import { PageHeader } from '@/features/meetings/kit/ui'
import { AgentsTab } from './AgentsTab'
import { CredentialsTab } from './CredentialsTab'
import { LogTab } from './LogTab'
import { PointsTab } from './PointsTab'

/** Gate control admin (D6): agents, access points, credentials and the opening log. */
export function AccessPage() {
  const { t } = useLingui()
  const { can } = useSession()
  const ctx = useSiteCtx()!
  const manage = can('access.manage')
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Gate control`}
        description={t`Agents on the gate computers, the gates and doors they open, who may pass, and what happened.`}
      />
      <Tabs defaultValue="agents">
        <TabsList className="h-auto flex-wrap">
          <TabsTrigger value="agents">{t`Agents`}</TabsTrigger>
          <TabsTrigger value="points">{t`Access points`}</TabsTrigger>
          <TabsTrigger value="credentials">{t`Credentials`}</TabsTrigger>
          {can('access.view_events') ? (
            <TabsTrigger value="log">{t`Opening log`}</TabsTrigger>
          ) : null}
        </TabsList>
        <TabsContent value="agents">
          <AgentsTab ctx={ctx} canManage={manage} />
        </TabsContent>
        <TabsContent value="points">
          <PointsTab ctx={ctx} canManage={manage} canOpen={can('access.open')} />
        </TabsContent>
        <TabsContent value="credentials">
          <CredentialsTab ctx={ctx} canManage={manage} />
        </TabsContent>
        {can('access.view_events') ? (
          <TabsContent value="log">
            <LogTab ctx={ctx} />
          </TabsContent>
        ) : null}
      </Tabs>
    </div>
  )
}
