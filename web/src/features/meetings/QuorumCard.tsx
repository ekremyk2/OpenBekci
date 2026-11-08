import { CheckCircle2Icon, CircleAlertIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { useLocale } from '@/app/locale'
import type { S } from './kit/api'
import { quorumView } from './logic'

function Bar({
  value,
  required,
  ok,
  label,
}: {
  value: number
  required: number | null
  ok: boolean | null
  label: string
}) {
  const v = Math.max(0, Math.min(100, value))
  return (
    <div className="flex flex-col gap-1">
      <div
        role="progressbar"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(v)}
        className="relative h-3 overflow-hidden rounded-full bg-muted"
      >
        <div
          className={cn(
            'h-full rounded-full transition-all',
            ok === null ? 'bg-primary' : ok ? 'bg-success' : 'bg-warning',
          )}
          // width is data, not style policy: set through a CSS custom property class below
          data-pct={Math.round(v)}
          ref={(el) => el?.style.setProperty('width', `${v}%`)}
        />
        {required !== null ? (
          <div
            className="absolute inset-y-0 w-0.5 bg-foreground/60"
            ref={(el) => el?.style.setProperty('inset-inline-start', `${required}%`)}
            aria-hidden="true"
          />
        ) : null}
      </div>
    </div>
  )
}

/**
 * Live quorum under the site's rules, by number of owners and by land share (arsa payi).
 * First call: a majority by BOTH; second call: normally no quorum.
 */
export function QuorumCard({ quorum, className }: { quorum: S['Quorum']; className?: string }) {
  const { t } = useLingui()
  const { intl } = useLocale()
  const v = quorumView(quorum)
  const fmt = (n: number) => new Intl.NumberFormat(intl, { maximumFractionDigits: 1 }).format(n)
  return (
    <section
      aria-label={t`Quorum`}
      className={cn('flex flex-col gap-3 rounded-lg border bg-card p-4', className)}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-base font-semibold">
          <Trans>Quorum</Trans>
        </h2>
        <div className="flex items-center gap-2">
          <Badge variant="outline">{v.call === 'first' ? t`First call` : t`Second call`}</Badge>
          {v.reached ? (
            <Badge variant="success" className="gap-1">
              <CheckCircle2Icon className="size-3" aria-hidden="true" />
              <Trans>Quorum reached</Trans>
            </Badge>
          ) : (
            <Badge variant="warning" className="gap-1">
              <CircleAlertIcon className="size-3" aria-hidden="true" />
              <Trans>Quorum not reached</Trans>
            </Badge>
          )}
        </div>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="flex flex-col gap-1.5">
          <div className="flex items-baseline justify-between text-sm">
            <span className="text-muted-foreground">
              <Trans>By number of owners</Trans>
            </span>
            <span className="font-medium tabular-nums">
              <Trans>
                {v.presentCount} of {v.eligibleCount} owners ({fmt(v.countPct)}%)
              </Trans>
            </span>
          </div>
          <Bar
            value={v.countPct}
            required={v.requiredCountPct}
            ok={v.countOk}
            label={t`Share of owners present`}
          />
          {v.requiredCountPct !== null ? (
            <span className="text-xs text-muted-foreground">
              {v.moreOwnersNeeded && v.moreOwnersNeeded > 0 ? (
                <Trans>
                  More than {v.requiredCountPct}% needed; {v.moreOwnersNeeded} more owners.
                </Trans>
              ) : (
                <Trans>More than {v.requiredCountPct}% needed: met.</Trans>
              )}
            </span>
          ) : (
            <span className="text-xs text-muted-foreground">
              <Trans>No minimum number on this call.</Trans>
            </span>
          )}
        </div>
        <div className="flex flex-col gap-1.5">
          <div className="flex items-baseline justify-between text-sm">
            <span className="text-muted-foreground">
              <Trans>By land share (arsa payı)</Trans>
            </span>
            <span className="font-medium tabular-nums">
              <Trans>{fmt(v.presentSharePct)}% present</Trans>
            </span>
          </div>
          <Bar
            value={v.presentSharePct}
            required={v.requiredSharePct}
            ok={v.shareOk}
            label={t`Share of land present`}
          />
          {v.requiredSharePct !== null ? (
            <span className="text-xs text-muted-foreground">
              <Trans>More than {v.requiredSharePct}% needed.</Trans>
            </span>
          ) : (
            <span className="text-xs text-muted-foreground">
              <Trans>No minimum share on this call.</Trans>
            </span>
          )}
        </div>
      </div>
    </section>
  )
}
