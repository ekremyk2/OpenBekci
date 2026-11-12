import { useState } from 'react'
import { CheckIcon, CopyIcon, MailIcon, SmartphoneIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Button } from '@/components/ui/button'
import { WhatsAppIcon } from '@/components/ui/brand-icons'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { QrCode } from '@/features/meetings/kit/QrCode'
import { Callout } from '@/features/meetings/kit/widgets'
import { copyText } from '@/lib/clipboard'

/** What the QR encodes: the token with the `obk1.` prefix the guard console expects. */
export const qrValue = (token: string) => (token.startsWith('obk1.') ? token : `obk1.${token}`)

/** Deep links that carry the pass text; the QR image itself cannot ride along, the code can. */
export function shareLinks(message: string, subject: string) {
  const text = encodeURIComponent(message)
  return {
    whatsapp: `https://wa.me/?text=${text}`,
    sms: `sms:?&body=${text}`,
    email: `mailto:?subject=${encodeURIComponent(subject)}&body=${text}`,
  }
}

/**
 * Shows a visitor's QR exactly once, right after it was created, with the ways to hand it over.
 * The token lives only in this component's props: closing the dialog drops it, and the history
 * list never renders it again (C2: shown once and shared).
 */
export function QrOnceDialog({
  token,
  name,
  validity,
  onClose,
}: {
  token: string
  name: string
  validity: string
  onClose: () => void
}) {
  const { t } = useLingui()
  const [copied, setCopied] = useState(false)
  const value = qrValue(token)
  const message = t`Visitor pass for ${name}, valid ${validity}. Show this QR code at the gate, or give the guard this code: ${value}`
  const links = shareLinks(message, t`Visitor pass`)
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>Visitor pass for {name}</Trans>
          </DialogTitle>
          <DialogDescription>{validity}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col items-center gap-3">
          <QrCode value={value} label={t`Visitor QR code`} size={224} />
          <code className="rounded bg-muted px-2 py-1 text-xs" dir="ltr" data-testid="qr-code-text">
            {value}
          </code>
        </div>
        <Callout tone="warning" role="status">
          <Trans>
            This code is shown only now. Send it to your guest before you close this window.
            WhatsApp, SMS and e-mail carry the code through those services.
          </Trans>
        </Callout>
        <div className="grid gap-2 sm:grid-cols-2">
          <Button asChild variant="outline">
            <a href={links.whatsapp} target="_blank" rel="noopener noreferrer">
              <WhatsAppIcon />
              <Trans>WhatsApp</Trans>
            </a>
          </Button>
          <Button asChild variant="outline">
            <a href={links.sms}>
              <SmartphoneIcon />
              <Trans>SMS</Trans>
            </a>
          </Button>
          <Button asChild variant="outline">
            <a href={links.email}>
              <MailIcon />
              <Trans>Email</Trans>
            </a>
          </Button>
          <Button
            variant="outline"
            onClick={() => {
              void copyText(message).then((ok) => ok && setCopied(true))
            }}
          >
            {copied ? <CheckIcon /> : <CopyIcon />}
            {copied ? t`Copied` : t`Copy the message`}
          </Button>
        </div>
        <DialogFooter>
          <Button onClick={onClose}>
            <Trans>Done</Trans>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
