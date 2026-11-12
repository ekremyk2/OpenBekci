import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'

/** Frame of every public auth page: one `main` landmark, one `h1`, one card. */
export function AuthShell({
  title,
  description,
  logo,
  children,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  logo?: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <main id="main" className="flex min-h-dvh items-center justify-center p-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          {logo}
          <h1 className="text-xl leading-none font-semibold">{title}</h1>
          {description ? <CardDescription>{description}</CardDescription> : null}
        </CardHeader>
        <CardContent className="flex flex-col gap-4 text-sm">{children}</CardContent>
      </Card>
    </main>
  )
}
