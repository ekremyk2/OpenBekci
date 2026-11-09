import { useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { KeyRoundIcon, PlusIcon } from 'lucide-react'
import { draftApi, type Schema } from '@/api/draft'
import { AppError } from '@/api/errors'
import { runWithReauth } from '@/app/reauth'
import { useSession } from '@/app/session'
import { ConfirmDialog } from '@/components/data/ConfirmDialog'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { TableSkeleton } from '@/components/data/Skeletons'
import { FormError, FormField, SubmitButton } from '@/components/forms/FormField'
import { Badge } from '@/components/ui/badge'
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
import { Select } from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { withIdempotencyKey } from '@/lib/idempotency'
import { LoadMore, must, useCursorQuery } from '@/lib/pagination'
import { problemMessage } from '@/lib/labels'
import { endOfDayIso, formatInstant, todayLocal } from './format'
import { SecretRevealDialog } from './SecretReveal'

type ApiKey = Schema<'ApiKey'>

const listKey = (orgId: string) => ['integrations.listApiKeys', orgId] as const

/**
 * Permissions that can never be a scope of a key (docs/integrations.md): `apikeys.*`,
 * `webhooks.manage`, and the rights that exist only for people through an occupancy. The server
 * is authoritative (`not_allowed`); this only avoids offering them.
 */
const NOT_FOR_KEYS = new Set([
  'webhooks.manage',
  'statements.read_own',
  'payments.declare',
  'polls.answer',
  'invitations.household',
  'occupancies.household',
  'documents.read_addressed',
  'documents.upload_own',
  'deliveries.read',
  'me.read',
  'me.manage',
  'me.dashboard',
  'sessions.manage_own',
  'visitors.decide',
])
export const keyScopeAllowed = (permission: string) =>
  !permission.startsWith('apikeys.') && !NOT_FOR_KEYS.has(permission)

/** API keys of the organization (permissions `apikeys.read`, `apikeys.manage`). */
export function ApiKeysPage() {
  const { org, can } = useSession()
  const { t, i18n } = useLingui()
  const qc = useQueryClient()
  const orgId = org?.id ?? ''
  const canManage = can('apikeys.manage')
  const [createOpen, setCreateOpen] = useState(false)
  const [rotating, setRotating] = useState<ApiKey | null>(null)
  const [revoking, setRevoking] = useState<ApiKey | null>(null)
  const [revealed, setRevealed] = useState<{
    secret: string
    rotated: boolean
    name: string
  } | null>(null)

  const keys = useCursorQuery<ApiKey>(
    listKey(orgId),
    async (cursor) =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/api-keys', {
          params: { path: { org_id: orgId }, query: { limit: 50, cursor } },
        }),
      ),
    { enabled: !!orgId && (can('apikeys.read') || canManage) },
  )

  const rotate = useMutation({
    mutationFn: (key: ApiKey) =>
      runWithReauth(async () =>
        must(
          await draftApi.POST('/v1/orgs/{org_id}/api-keys/{key_id}:rotate', {
            params: { path: { org_id: orgId, key_id: key.id } },
          }),
        ),
      ),
    onSuccess: async (data) => {
      setRotating(null)
      setRevealed({ secret: data.secret, rotated: true, name: data.key.name })
      await qc.invalidateQueries({ queryKey: listKey(orgId) })
    },
  })
  const revoke = useMutation({
    mutationFn: (key: ApiKey) =>
      runWithReauth(async (idemKey) =>
        must(
          await draftApi.POST('/v1/orgs/{org_id}/api-keys/{key_id}:revoke', {
            params: { path: { org_id: orgId, key_id: key.id } },
            ...withIdempotencyKey(idemKey),
          }),
        ),
      ),
    onSuccess: async () => {
      setRevoking(null)
      await qc.invalidateQueries({ queryKey: listKey(orgId) })
    },
  })

  const statusBadge = (k: ApiKey) =>
    k.status === 'active' ? (
      <Badge variant="success">
        <Trans>Active</Trans>
      </Badge>
    ) : k.status === 'revoked' ? (
      <Badge variant="destructive">
        <Trans>Revoked</Trans>
      </Badge>
    ) : k.status === 'expired' ? (
      <Badge variant="warning">
        <Trans>Expired</Trans>
      </Badge>
    ) : (
      <Badge variant="secondary">{k.status}</Badge>
    )

  const siteName = (id: string | null) =>
    id ? (org?.sites.find((s) => s.id === id)?.name ?? id) : t`All sites`

  const mutationError = (e: unknown) =>
    e instanceof AppError && !e.isReauthRequired
      ? (problemMessage(e.code, i18n) ?? t`Something went wrong. Please try again.`)
      : undefined

  return (
    <section className="flex flex-col gap-4" aria-labelledby="keys-title">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h2 id="keys-title" className="text-lg font-semibold">
            <Trans>API keys</Trans>
          </h2>
          <p className="max-w-prose text-sm text-muted-foreground">
            <Trans>
              A key lets a script call the API without a person signing in. It never has more rights
              than the administrator who created it.
            </Trans>
          </p>
        </div>
        {canManage ? (
          <Button onClick={() => setCreateOpen(true)}>
            <PlusIcon aria-hidden="true" />
            <Trans>Create API key</Trans>
          </Button>
        ) : null}
      </div>

      {keys.isPending ? <TableSkeleton columns={6} /> : null}
      {keys.isError ? <ErrorState error={keys.error} onRetry={() => void keys.refetch()} /> : null}
      {keys.data && keys.items.length === 0 ? (
        <EmptyState
          icon={KeyRoundIcon}
          title={<Trans>No API keys yet</Trans>}
          description={<Trans>Create a key to connect a script or another tool.</Trans>}
        />
      ) : null}
      {keys.items.length > 0 ? (
        <div className="overflow-x-auto rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Name</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Key</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Scopes</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Site</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Last used</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Expires</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Status</Trans>
                </TableHead>
                {canManage ? (
                  <TableHead>
                    <span className="sr-only">
                      <Trans>Actions</Trans>
                    </span>
                  </TableHead>
                ) : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {keys.items.map((k) => (
                <TableRow key={k.id}>
                  <TableCell className="font-medium">{k.name}</TableCell>
                  <TableCell>
                    <code className="text-xs" dir="ltr">
                      obk_{k.prefix}_…
                    </code>
                  </TableCell>
                  <TableCell>
                    <ScopeBadges scopes={k.scopes} />
                  </TableCell>
                  <TableCell>{siteName(k.site_id)}</TableCell>
                  <TableCell>
                    {k.last_used_at ? formatInstant(k.last_used_at, i18n.locale) : t`Never`}
                  </TableCell>
                  <TableCell>
                    {k.expires_at ? formatInstant(k.expires_at, i18n.locale) : t`Never`}
                  </TableCell>
                  <TableCell>{statusBadge(k)}</TableCell>
                  {canManage ? (
                    <TableCell>
                      {k.status === 'revoked' ? null : (
                        <div className="flex justify-end gap-1">
                          <Button
                            variant="outline"
                            size="sm"
                            aria-label={t`Rotate ${k.name}`}
                            onClick={() => setRotating(k)}
                          >
                            <Trans>Rotate</Trans>
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            aria-label={t`Revoke ${k.name}`}
                            onClick={() => setRevoking(k)}
                          >
                            <Trans>Revoke</Trans>
                          </Button>
                        </div>
                      )}
                    </TableCell>
                  ) : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}
      <LoadMore query={keys} />

      {createOpen ? (
        <CreateKeyDialog
          orgId={orgId}
          onClose={() => setCreateOpen(false)}
          onCreated={async (created) => {
            setCreateOpen(false)
            setRevealed({ secret: created.secret, rotated: false, name: created.key.name })
            await qc.invalidateQueries({ queryKey: listKey(orgId) })
          }}
        />
      ) : null}

      <ConfirmDialog
        open={!!rotating}
        onOpenChange={(o) => !o && setRotating(null)}
        busy={rotate.isPending}
        title={<Trans>Rotate {rotating?.name}?</Trans>}
        description={
          <Trans>
            A new key is issued for the same name and scopes. The old key stops working immediately,
            so everything that uses it must be updated at once.
          </Trans>
        }
        confirmLabel={<Trans>Rotate key</Trans>}
        onConfirm={() => rotating && rotate.mutate(rotating)}
      >
        <FormError message={mutationError(rotate.error)} />
      </ConfirmDialog>
      <ConfirmDialog
        open={!!revoking}
        onOpenChange={(o) => !o && setRevoking(null)}
        destructive
        busy={revoke.isPending}
        title={<Trans>Revoke {revoking?.name}?</Trans>}
        description={
          <Trans>The key stops working immediately and cannot be switched on again.</Trans>
        }
        confirmLabel={<Trans>Revoke key</Trans>}
        onConfirm={() => revoking && revoke.mutate(revoking)}
      >
        <FormError message={mutationError(revoke.error)} />
      </ConfirmDialog>

      {revealed ? (
        <SecretRevealDialog
          open
          secret={revealed.secret}
          title={
            revealed.rotated ? (
              <Trans>New secret for {revealed.name}</Trans>
            ) : (
              <Trans>Your new API key</Trans>
            )
          }
          description={
            revealed.rotated ? (
              <Trans>The old key no longer works. Use this one from now on.</Trans>
            ) : (
              <Trans>
                Use it as a bearer token: <code>Authorization: Bearer …</code>. Only a hash is
                stored on the server.
              </Trans>
            )
          }
          onDone={() => setRevealed(null)}
        />
      ) : null}
    </section>
  )
}

function ScopeBadges({ scopes }: { scopes: string[] }) {
  const shown = scopes.slice(0, 2)
  const rest = scopes.length - shown.length
  return (
    <div className="flex max-w-64 flex-wrap gap-1">
      {shown.map((s) => (
        <Badge key={s} variant="secondary" className="font-mono">
          {s}
        </Badge>
      ))}
      {rest > 0 ? (
        <Badge variant="outline" title={scopes.join(', ')}>
          <Trans>+{rest} more</Trans>
        </Badge>
      ) : null}
    </div>
  )
}

/* ───────────── create ───────────── */

function CreateKeyDialog({
  orgId,
  onClose,
  onCreated,
}: {
  orgId: string
  onClose: () => void
  onCreated: (created: Schema<'ApiKeyCreated'>) => void | Promise<void>
}) {
  const { t, i18n } = useLingui()
  const { org } = useSession()
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState<string[]>([])
  const [filter, setFilter] = useState('')
  const [siteId, setSiteId] = useState('')
  const [expires, setExpires] = useState('')
  const [local, setLocal] = useState<Record<string, string>>({})

  const held = useMemo(
    () =>
      [...new Set((org?.permissions ?? []).map((p) => p.permission))]
        .filter(keyScopeAllowed)
        .sort(),
    [org],
  )
  const groups = useMemo(() => {
    const q = filter.trim().toLowerCase()
    const out = new Map<string, string[]>()
    for (const p of held) {
      if (q && !p.includes(q)) continue
      const g = p.split('.')[0]!
      out.set(g, [...(out.get(g) ?? []), p])
    }
    return [...out.entries()]
  }, [held, filter])

  const create = useMutation({
    mutationFn: (body: Schema<'ApiKeyCreate'>) =>
      runWithReauth(async () =>
        must(
          await draftApi.POST('/v1/orgs/{org_id}/api-keys', {
            params: { path: { org_id: orgId } },
            body,
          }),
        ),
      ),
    onSuccess: (data) => onCreated(data),
  })

  // server field errors by field; the scopes list shows each refused permission with its reason
  const remote = useMemo(() => {
    const out: Record<string, string> = {}
    const e = create.error
    if (e instanceof AppError && e.isValidation) {
      for (const [field, issues] of Object.entries(e.fieldErrors)) {
        const issue = issues[0]
        if (!issue) continue
        const scope = /^scopes\[(\d+)\]/.exec(field)
        if (scope) {
          const permission = scopes[Number(scope[1])]
          const reason =
            issue.message ??
            (issue.code === 'not_held'
              ? t`you do not hold this permission`
              : issue.code === 'not_allowed'
                ? t`a key cannot have this permission`
                : issue.code === 'unknown_permission'
                  ? t`unknown permission`
                  : t`not valid`)
          out.scopes = [out.scopes, `${permission ?? ''}: ${reason}`].filter(Boolean).join('; ')
        } else {
          out[field] =
            issue.message ??
            (issue.code === 'required' ? t`This field is required` : t`This value is not valid`)
        }
      }
    }
    return out
  }, [create.error, scopes, t])

  const rootError =
    create.error instanceof AppError
      ? create.error.isReauthRequired ||
        (create.error.isValidation && Object.keys(remote).length > 0)
        ? undefined
        : (problemMessage(create.error.code, i18n) ??
          (create.error.isForbidden
            ? t`You do not have permission to do this.`
            : t`Something went wrong. Please try again.`))
      : create.error
        ? t`Something went wrong. Please try again.`
        : undefined

  function submit(e: React.FormEvent) {
    e.preventDefault()
    const errs: Record<string, string> = {}
    if (!name.trim()) errs.name = t`Enter a name for the key`
    if (scopes.length === 0) errs.scopes = t`Choose at least one scope`
    setLocal(errs)
    if (Object.keys(errs).length > 0) return
    create.mutate({
      name: name.trim(),
      scopes,
      site_id: siteId || null,
      expires_at: expires ? endOfDayIso(expires) : null,
    })
  }
  const err = (field: string) => local[field] ?? remote[field]

  return (
    <Dialog open onOpenChange={(o) => !o && !create.isPending && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            <Trans>Create API key</Trans>
          </DialogTitle>
          <DialogDescription>
            <Trans>
              You will be asked to confirm your password. The key is shown once after you create it.
            </Trans>
          </DialogDescription>
        </DialogHeader>
        <form noValidate className="flex flex-col gap-4" onSubmit={submit}>
          <FormField id="key-name" label={<Trans>Name</Trans>} error={err('name')}>
            <Input
              id="key-name"
              maxLength={120}
              value={name}
              aria-invalid={!!err('name') || undefined}
              onChange={(e) => setName(e.target.value)}
              placeholder={t`Accounting export`}
            />
          </FormField>

          <fieldset className="flex flex-col gap-2" aria-describedby="key-scopes-error">
            <legend className="text-sm leading-none font-medium">
              <Trans>Scopes</Trans>{' '}
              <span className="font-normal text-muted-foreground">
                <Trans>{scopes.length} selected</Trans>
              </span>
            </legend>
            <Input
              aria-label={t`Filter permissions`}
              placeholder={t`Filter permissions`}
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
            <div className="max-h-56 overflow-y-auto rounded-md border p-3">
              {groups.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  <Trans>No permission matches.</Trans>
                </p>
              ) : (
                groups.map(([group, perms]) => (
                  <div key={group} className="mb-3 last:mb-0">
                    <div className="mb-1 flex items-center justify-between">
                      <span className="text-xs font-semibold text-muted-foreground uppercase">
                        {group}
                      </span>
                      <button
                        type="button"
                        className="text-xs underline-offset-4 hover:underline"
                        onClick={() =>
                          setScopes((s) =>
                            perms.every((p) => s.includes(p))
                              ? s.filter((p) => !perms.includes(p))
                              : [...new Set([...s, ...perms])],
                          )
                        }
                      >
                        {perms.every((p) => scopes.includes(p)) ? (
                          <Trans>Clear {group}</Trans>
                        ) : (
                          <Trans>Select all {group}</Trans>
                        )}
                      </button>
                    </div>
                    <div className="grid gap-1 sm:grid-cols-2">
                      {perms.map((p) => (
                        <label key={p} className="flex items-center gap-2 text-sm">
                          <Checkbox
                            checked={scopes.includes(p)}
                            onChange={(e) =>
                              setScopes((s) =>
                                e.target.checked ? [...s, p] : s.filter((x) => x !== p),
                              )
                            }
                          />
                          <span className="font-mono text-xs" dir="ltr">
                            {p}
                          </span>
                        </label>
                      ))}
                    </div>
                  </div>
                ))
              )}
            </div>
            <p className="text-xs text-muted-foreground">
              <Trans>
                You can only give permissions you hold yourself. A key never gets the rights to
                manage keys or webhooks.
              </Trans>
            </p>
            {err('scopes') ? (
              <p id="key-scopes-error" role="alert" className="text-sm text-destructive">
                {err('scopes')}
              </p>
            ) : null}
          </fieldset>

          <div className="grid gap-4 sm:grid-cols-2">
            <FormField
              id="key-site"
              label={<Trans>Restrict to a site (optional)</Trans>}
              error={err('site_id')}
            >
              <Select id="key-site" value={siteId} onChange={(e) => setSiteId(e.target.value)}>
                <option value="">{t`All sites`}</option>
                {(org?.sites ?? []).map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </FormField>
            <FormField
              id="key-expires"
              label={<Trans>Expires on (optional)</Trans>}
              error={err('expires_at')}
              hint={<Trans>Empty: the key does not expire.</Trans>}
            >
              <Input
                id="key-expires"
                type="date"
                min={todayLocal()}
                value={expires}
                aria-invalid={!!err('expires_at') || undefined}
                onChange={(e) => setExpires(e.target.value)}
              />
            </FormField>
          </div>

          <FormError message={rootError} />
          <DialogFooter>
            <Button type="button" variant="outline" disabled={create.isPending} onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <SubmitButton busy={create.isPending}>
              <Trans>Create key</Trans>
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
