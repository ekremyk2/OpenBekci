import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, type Schema } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { ConfirmDialog } from '@/components/data/ConfirmDialog'
import { Field } from '@/components/forms/FormBits'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { must } from '@/lib/pagination'
import { toast } from '@/lib/toast'

/**
 * Privacy actions of a manager on a person: export their data (a job files a JSON and a PDF in the
 * Privacy folder of their site) and erase them (irreversible; asks for re-authentication).
 */
export function PrivacyPanel({
  orgId,
  person,
  canExport,
  canErase,
}: {
  orgId: string
  person: Schema<'Person'>
  canExport: boolean
  canErase: boolean
}) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const [erasing, setErasing] = useState(false)
  const [reason, setReason] = useState('')
  const [override, setOverride] = useState(false)
  const path = { org_id: orgId, person_id: person.id }
  const ask = useMutation({
    mutationFn: async () =>
      must(
        await draftApi.POST('/v1/orgs/{org_id}/persons/{person_id}/data-export', {
          params: { path },
        }),
      ),
    onSuccess: () =>
      toast.info(
        t`The export is being prepared. The person and you are notified when it is ready.`,
      ),
  })
  const erase = useMutation({
    mutationFn: () =>
      runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/persons/{person_id}:erase', {
          params: { path },
          body: { reason: reason.trim(), override_open_balance: override },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      idem.renew()
      setErasing(false)
      setReason('')
      setOverride(false)
      toast.info(t`The person is erased.`)
      await queryClient.invalidateQueries({ queryKey: ['people.getPerson', orgId, person.id] })
    },
    // a 409 (open charges) is reported by the global handler; the dialog stays open to override
  })
  return (
    <section className="grid max-w-2xl gap-3" aria-labelledby="privacy-h">
      <h2 id="privacy-h" className="text-base font-semibold">
        <Trans>Privacy</Trans>
      </h2>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Answer a data-subject request: export everything held about this person, or erase them.
          Erasing cannot be undone; ledger entries and votes stay, under a neutral name.
        </Trans>
      </p>
      <div className="flex flex-wrap gap-2">
        {canExport ? (
          <Button variant="outline" disabled={ask.isPending} onClick={() => ask.mutate()}>
            <Trans>Export data</Trans>
          </Button>
        ) : null}
        {canErase ? (
          <Button variant="destructive" onClick={() => setErasing(true)}>
            <Trans>Erase person</Trans>
          </Button>
        ) : null}
      </div>
      <ConfirmDialog
        open={erasing}
        onOpenChange={setErasing}
        title={t`Erase ${person.display_name}?`}
        description={t`Their name, contact details, notes and national id are removed and their sign-in is closed. This cannot be undone.`}
        confirmLabel={t`Erase`}
        destructive
        busy={erase.isPending}
        confirmDisabled={reason.trim() === ''}
        onConfirm={() => erase.mutate()}
      >
        <div className="grid gap-3">
          <Field id="erase-reason" label={<Trans>Reason</Trans>}>
            <Input id="erase-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
          </Field>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={override}
              onChange={(e) => setOverride(e.target.checked)}
            />
            <Trans>Erase although they still have open charges</Trans>
          </label>
        </div>
      </ConfirmDialog>
    </section>
  )
}
