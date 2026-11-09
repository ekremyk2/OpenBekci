import { MessageCircleIcon, SendIcon } from 'lucide-react'

/** Placeholder marks for third-party services: their brand color around a neutral glyph. */
function Mark({ color, children }: { color: string; children: React.ReactNode }) {
  return (
    <span
      className="inline-flex size-5 shrink-0 items-center justify-center rounded-full text-white [&_svg]:size-3"
      style={{ background: color }}
      aria-hidden="true"
    >
      {children}
    </span>
  )
}

export const WhatsAppIcon = () => (
  <Mark color="#128c7e">
    <MessageCircleIcon />
  </Mark>
)
export const TelegramIcon = () => (
  <Mark color="#1c7fb3">
    <SendIcon />
  </Mark>
)
