import { useEffect, useState } from 'react'
import { useRouter } from '@tanstack/react-router'
import { ArrowLeftIcon, UsersRoundIcon } from 'lucide-react'
import { Trans } from '@lingui/react/macro'
import { useLocale } from '@/app/locale'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { Button } from '@/components/ui/button'
import { useSiteCtx } from '@/features/meetings/kit/api'
import { Callout } from '@/features/meetings/kit/widgets'
import { setGuardSession, useGuardSession } from './guardApi'
import { ExitGate } from './ExitGate'
import { PinSwitch } from './PinSwitch'
import {
  ExpectedList,
  OpenPanel,
  ParcelsPanel,
  ScanPanel,
  SearchPanel,
  useConsole,
  WalkInPanel,
} from './panels'

function Clock({ timeZone }: { timeZone: string }) {
  const { intl } = useLocale()
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 15_000)
    return () => clearInterval(id)
  }, [])
  return (
    <time className="text-2xl font-semibold tabular-nums" dateTime={now.toISOString()}>
      {new Intl.DateTimeFormat(intl, { timeStyle: 'short', timeZone }).format(now)}
    </time>
  )
}

/**
 * Guard console (C2): a full-screen, tablet-sized page with large targets. It covers the app shell
 * on purpose: the booth shows nothing but the gate. Data is minimized: names, plates, units and
 * windows; never balances, other contacts or national ids (S8).
 */
export function GuardConsolePage() {
  const ctx = useSiteCtx()!
  const guard = useGuardSession()
  const cq = useConsole(ctx, !!guard)
  const { can } = useSession()
  const router = useRouter()
  // a booth user (guard only) leaves with a PIN; anyone who also manages visitors just goes back
  const guardOnly = !can('visitors.read') && !can('visitors.manage') && !can('visitors.decide')
  const back = () =>
    router.history.length > 1
      ? router.history.back()
      : void router.navigate({ to: '/orgs/$orgId/visitors', params: { orgId: ctx.orgId } })
  return (
    <div className="fixed inset-0 z-50 overflow-y-auto bg-background" data-testid="guard-console">
      <header className="sticky top-0 z-10 flex flex-wrap items-center gap-3 border-b bg-background px-4 py-3">
        {!guardOnly ? (
          <Button variant="ghost" size="xl" className="min-h-11" onClick={back}>
            <ArrowLeftIcon className="rtl-flip" aria-hidden="true" />
            <Trans>Exit console</Trans>
          </Button>
        ) : guard ? (
          <ExitGate ctx={ctx} guard={guard} />
        ) : null}
        <h1 className="text-2xl font-semibold">
          <Trans>Gate</Trans>
        </h1>
        <span className="text-muted-foreground">{ctx.siteName}</span>
        <div className="ms-auto flex items-center gap-4">
          <Clock timeZone={ctx.timeZone} />
          {guard ? (
            <>
              <span className="flex items-center gap-2 text-lg" data-testid="guard-name">
                <UsersRoundIcon className="size-5" aria-hidden="true" />
                {guard.displayName}
              </span>
              <Button variant="outline" size="xl" onClick={() => setGuardSession(null)}>
                <Trans>Switch guard</Trans>
              </Button>
            </>
          ) : null}
        </div>
      </header>
      {!guard ? (
        <PinSwitch ctx={ctx} />
      ) : cq.isPending ? (
        <div className="p-6">
          <PageSkeleton />
        </div>
      ) : cq.isError ? (
        <div className="p-6">
          <ErrorState error={cq.error} onRetry={() => void cq.refetch()} />
        </div>
      ) : (
        <div className="grid gap-8 p-4 lg:grid-cols-3">
          {cq.data.pending_approvals.length > 0 ? (
            <div className="lg:col-span-3">
              <Callout tone="warning" role="status">
                <Trans>
                  {cq.data.pending_approvals.length} walk-in visitors are waiting for a resident's
                  answer.
                </Trans>
              </Callout>
            </div>
          ) : null}
          <div className="flex flex-col gap-8">
            <ScanPanel ctx={ctx} />
            <WalkInPanel ctx={ctx} />
          </div>
          <div className="flex flex-col gap-8">
            <SearchPanel ctx={ctx} />
            <ExpectedList ctx={ctx} visitors={cq.data.expected_visitors} />
          </div>
          <div className="flex flex-col gap-8">
            <OpenPanel ctx={ctx} console={cq.data} />
            <ParcelsPanel ctx={ctx} waiting={cq.data.waiting_deliveries} />
          </div>
        </div>
      )}
    </div>
  )
}
