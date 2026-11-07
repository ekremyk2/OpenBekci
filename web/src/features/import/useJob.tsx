import { useQuery } from '@tanstack/react-query'
import { CheckCircle2Icon, LoaderCircleIcon, XCircleIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, type Schema } from '@/api/draft'
import { ErrorState } from '@/components/data/ErrorState'

/** Poll interval of `GET …/jobs/{id}`; tests lower it. */
export const jobPolling = { ms: 1000 }

export type JobStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'
export const isTerminal = (status: string | undefined) =>
  status === 'succeeded' || status === 'failed' || status === 'cancelled'

/** Polls a background job until it reaches a terminal state (`operations.getJob`). */
export function useJob(orgId: string, jobId: string | null | undefined) {
  return useQuery({
    queryKey: ['job', orgId, jobId],
    enabled: !!jobId,
    queryFn: async (): Promise<Schema<'Job'>> => {
      const { data } = await draftApi.GET('/v1/orgs/{org_id}/jobs/{job_id}', {
        params: { path: { org_id: orgId, job_id: jobId! } },
      })
      return data!
    },
    refetchInterval: (query) => (isTerminal(query.state.data?.status) ? false : jobPolling.ms),
    staleTime: 0,
  })
}

/** Progress of a job: bar while running, a result line when done, an error when it failed. */
export function JobProgress({ orgId, jobId }: { orgId: string; jobId: string }) {
  const { t } = useLingui()
  const job = useJob(orgId, jobId)
  if (job.error) return <ErrorState error={job.error} onRetry={() => void job.refetch()} />
  const data = job.data
  const pct = Math.round((data?.progress ?? 0) * 100)
  if (data?.status === 'succeeded') {
    return (
      <p role="status" className="flex items-center gap-2 text-sm text-success">
        <CheckCircle2Icon className="size-4" aria-hidden="true" />
        <Trans>Finished.</Trans>
      </p>
    )
  }
  if (data?.status === 'failed' || data?.status === 'cancelled') {
    return (
      <p role="alert" className="flex items-center gap-2 text-sm text-destructive">
        <XCircleIcon className="size-4" aria-hidden="true" />
        <Trans>The job did not finish. Nothing was changed.</Trans>
      </p>
    )
  }
  return (
    <div role="status" aria-live="polite" className="flex flex-col gap-2">
      <p className="flex items-center gap-2 text-sm">
        <LoaderCircleIcon className="size-4 animate-spin" aria-hidden="true" />
        <Trans>Working… {pct}%</Trans>
      </p>
      <progress className="h-2 w-full" max={100} value={pct} aria-label={t`Progress`} />
    </div>
  )
}
