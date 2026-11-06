import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useRouter } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { toast } from '@/lib/toast'
import { onApiEvent } from '@/api/events'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { clearSession } from './session'

/** Reacts to cross-cutting API conditions (web plan §6 error table). Mounted once in the root route. */
export function ErrorEffects() {
  const router = useRouter()
  const queryClient = useQueryClient()
  const { t } = useLingui()
  const [conflictOpen, setConflictOpen] = useState(false)

  useEffect(
    () =>
      onApiEvent((event) => {
        switch (event.type) {
          case 'unauthorized': {
            clearSession(queryClient)
            const here = router.state.location
            if (!here.pathname.startsWith('/login')) {
              void router.navigate({
                to: '/login',
                search: { next: here.href },
                replace: true,
              })
            }
            break
          }
          case 'version-conflict':
            setConflictOpen(true)
            break
          case 'rate-limited': {
            const wait = event.error.retryAfter
            toast.warning(
              wait
                ? t`Too many requests. Try again in ${Math.ceil(wait)} seconds.`
                : t`Too many requests. Please wait a moment and try again.`,
            )
            break
          }
          case 'reauth-required':
            // The re-authentication modal arrives with the first sensitive action (web plan §7).
            toast.info(t`Please confirm it is you before continuing.`)
            break
          default:
            break
        }
      }),
    [router, queryClient, t],
  )

  return (
    <Dialog open={conflictOpen} onOpenChange={setConflictOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>This record changed</Trans>
          </DialogTitle>
          <DialogDescription>
            <Trans>
              Someone else updated it while you were editing. Reload to see the latest version; your
              unsaved changes will be lost.
            </Trans>
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={() => setConflictOpen(false)}>
            <Trans>Keep editing</Trans>
          </Button>
          <Button onClick={() => globalThis.location.reload()}>
            <Trans>Reload</Trans>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
