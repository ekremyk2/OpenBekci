import { useState, type ReactNode } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { CheckIcon, CopyIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'

/**
 * Shows a secret exactly once (API key token, webhook signing secret). It cannot be dismissed by
 * Escape or a click outside: the person copies it and confirms they stored it, and only then the
 * dialog closes. The secret lives in the caller's state only; the caller drops it on done.
 */
export function SecretRevealDialog({
  open,
  title,
  description,
  secret,
  extra,
  onDone,
}: {
  open: boolean
  title: ReactNode
  description: ReactNode
  secret: string
  extra?: ReactNode
  onDone: () => void
}) {
  const { t } = useLingui()
  const [copied, setCopied] = useState(false)
  const [stored, setStored] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(secret)
      setCopied(true)
    } catch {
      // no clipboard permission: select the text so Ctrl+C works
      const el = document.getElementById('secret-reveal-value') as HTMLInputElement | null
      el?.focus()
      el?.select()
    }
  }

  return (
    <Dialog open={open}>
      <DialogContent
        onEscapeKeyDown={(e) => e.preventDefault()}
        onPointerDownOutside={(e) => e.preventDefault()}
        onInteractOutside={(e) => e.preventDefault()}
        className="[&>button]:hidden"
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <div className="flex gap-2">
          <Input
            id="secret-reveal-value"
            readOnly
            dir="ltr"
            spellCheck={false}
            aria-label={t`The secret`}
            className="flex-1 font-mono text-xs"
            value={secret}
            onFocus={(e) => e.currentTarget.select()}
          />
          <Button type="button" variant="outline" onClick={() => void copy()}>
            {copied ? <CheckIcon aria-hidden="true" /> : <CopyIcon aria-hidden="true" />}
            {copied ? <Trans>Copied</Trans> : <Trans>Copy</Trans>}
          </Button>
        </div>
        <p role="status" className="text-sm font-medium text-warning">
          <Trans>This is the only time the secret is shown. It cannot be read back later.</Trans>
        </p>
        {extra}
        <label className="flex items-start gap-3">
          <Checkbox
            className="mt-1"
            checked={stored}
            onChange={(e) => setStored(e.target.checked)}
          />
          <span className="text-sm">
            <Trans>I have stored this secret in a safe place.</Trans>
          </span>
        </label>
        <DialogFooter>
          <Button type="button" disabled={!stored} onClick={onDone}>
            <Trans>Done</Trans>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
