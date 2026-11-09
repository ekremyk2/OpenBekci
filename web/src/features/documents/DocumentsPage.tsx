import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import {
  FileTextIcon,
  FolderIcon,
  FolderLockIcon,
  FolderPlusIcon,
  PencilIcon,
  SettingsIcon,
  Trash2Icon,
} from 'lucide-react'
import { draftApi, ifMatch, type Document, type DocumentVisibility, type Folder } from '@/api/draft'
import { fileUrl } from '@/api/files'
import { useSession } from '@/app/session'
import { ConfirmDialog } from '@/components/data/ConfirmDialog'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { SiteDate } from '@/components/data/SiteDate'
import { TableSkeleton } from '@/components/data/Skeletons'
import { UploadField } from '@/components/files/UploadField'
import { useUploads } from '@/components/files/useUploads'
import { FormError } from '@/components/forms/FormField'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { SitePicker, useSite } from '@/features/dashboard/site'
import { useApplyErrorMessage } from '@/features/tickets/errors'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { LoadMore, must, useCursorQuery } from '@/lib/pagination'
import { toast } from '@/lib/toast'
import { VisibilityEditor } from './VisibilityEditor'

function useVisibilityText() {
  const { t } = useLingui()
  return (v: DocumentVisibility | null | undefined) => {
    switch (v?.level) {
      case 'board':
        return t`Board only`
      case 'owners':
        return t`Owners`
      case 'everyone':
        return t`Everyone`
      case 'units':
        return t`Chosen units`
      default:
        return v?.level ?? ''
    }
  }
}

function SourceBadge({ doc }: { doc: Document }) {
  if (doc.source !== 'generated') return null
  const type = doc.generated_from?.type
  return (
    <Badge variant="info">
      {type === 'statement' ? (
        <Trans>Generated: statement</Trans>
      ) : type === 'receipt' ? (
        <Trans>Generated: receipt</Trans>
      ) : type === 'certificate' ? (
        <Trans>Generated: certificate</Trans>
      ) : (
        <Trans>Generated</Trans>
      )}
    </Badge>
  )
}

/** Folder settings: rename and visibility (board / owners / everyone / chosen units). */
export function FolderSettingsDialog({
  orgId,
  siteId,
  folder,
  onClose,
}: {
  orgId: string
  siteId: string
  folder: Folder
  onClose: () => void
}) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const describe = useApplyErrorMessage()
  const [name, setName] = useState(folder.name)
  const [visibility, setVisibility] = useState<DocumentVisibility>(folder.visibility)
  const [error, setError] = useState<string>()
  const save = useMutation({
    mutationFn: async () =>
      must(
        await draftApi.PATCH('/v1/orgs/{org_id}/folders/{folder_id}', {
          params: {
            path: { org_id: orgId, folder_id: folder.id },
            header: ifMatch(folder.version),
          },
          body: {
            name: folder.is_system ? undefined : name.trim(),
            visibility,
          },
        }),
      ),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['operations.listFolders'] })
      await qc.invalidateQueries({ queryKey: ['operations.getFolder'] })
      toast.info(t`Folder saved.`)
      onClose()
    },
    onError: (e) => setError(describe(e)),
  })
  const invalid = visibility.level === 'units' && !(visibility.unit_ids?.length ?? 0)
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>Folder settings</Trans>
          </DialogTitle>
          <DialogDescription>
            <Trans>Choose who may open this folder and the documents in it.</Trans>
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-2">
          <Label htmlFor="folder-name">
            <Trans>Name</Trans>
          </Label>
          <Input
            id="folder-name"
            value={name}
            disabled={folder.is_system}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className="flex flex-col gap-2">
          <Label>
            <Trans>Visible to</Trans>
          </Label>
          <VisibilityEditor
            orgId={orgId}
            siteId={siteId}
            value={visibility}
            onChange={setVisibility}
          />
          {invalid ? (
            <p role="alert" className="text-sm text-destructive">
              <Trans>Choose at least one unit.</Trans>
            </p>
          ) : null}
        </div>
        <FormError message={error} />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            <Trans>Cancel</Trans>
          </Button>
          <Button
            disabled={invalid || !name.trim() || save.isPending}
            onClick={() => save.mutate()}
          >
            <Trans>Save</Trans>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function NewFolderDialog({
  orgId,
  siteId,
  parentId,
  onClose,
}: {
  orgId: string
  siteId: string
  parentId: string | null
  onClose: () => void
}) {
  const qc = useQueryClient()
  const idem = useIdempotencyKey()
  const describe = useApplyErrorMessage()
  const [name, setName] = useState('')
  const [error, setError] = useState<string>()
  const create = useMutation({
    mutationFn: async () =>
      must(
        await draftApi.POST('/v1/orgs/{org_id}/folders', {
          params: { path: { org_id: orgId } },
          body: { site_id: siteId, parent_id: parentId, name: name.trim() },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['operations.listFolders'] })
      onClose()
    },
    onError: (e) => setError(describe(e)),
  })
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>New folder</Trans>
          </DialogTitle>
          <DialogDescription>
            <Trans>
              It starts with the visibility of the folder it is in. You can change it after.
            </Trans>
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (name.trim()) create.mutate()
          }}
        >
          <Label htmlFor="new-folder-name">
            <Trans>Name</Trans>
          </Label>
          <Input id="new-folder-name" value={name} onChange={(e) => setName(e.target.value)} />
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={!name.trim() || create.isPending}>
              <Trans>Create folder</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function PreviewDialog({
  orgId,
  doc,
  onClose,
}: {
  orgId: string
  doc: Document
  onClose: () => void
}) {
  const { t } = useLingui()
  const url = fileUrl(orgId, doc.file_id)
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>{doc.title}</DialogTitle>
          <DialogDescription className="sr-only">
            <Trans>Document preview</Trans>
          </DialogDescription>
        </DialogHeader>
        {doc.mime === 'application/pdf' ? (
          <iframe
            title={t`Preview of ${doc.title}`}
            src={url}
            className="h-[70dvh] w-full rounded border"
          />
        ) : doc.mime.startsWith('image/') ? (
          <img
            src={fileUrl(orgId, doc.file_id, 'medium')}
            alt={doc.title}
            className="max-h-[70dvh] object-contain"
          />
        ) : (
          <p className="text-sm text-muted-foreground">
            <Trans>This file type cannot be previewed.</Trans>
          </p>
        )}
        <a
          href={url}
          download={doc.title}
          className="w-fit text-sm text-primary underline-offset-4 hover:underline"
        >
          <Trans>Download</Trans>
        </a>
      </DialogContent>
    </Dialog>
  )
}

function DocumentRow({
  orgId,
  doc,
  folders,
  canManage,
  timeZone,
  onPreview,
}: {
  orgId: string
  doc: Document
  folders: Folder[]
  canManage: boolean
  timeZone: string
  onPreview: () => void
}) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const [renaming, setRenaming] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [title, setTitle] = useState(doc.title)
  const refresh = () => qc.invalidateQueries({ queryKey: ['operations.listDocuments'] })
  const patch = useMutation({
    mutationFn: async (body: { title?: string; folder_id?: string }) =>
      must(
        await draftApi.PATCH('/v1/orgs/{org_id}/documents/{document_id}', {
          params: {
            path: { org_id: orgId, document_id: doc.id },
            header: ifMatch(doc.version),
          },
          body,
        }),
      ),
    onSuccess: async () => {
      setRenaming(false)
      await refresh()
    },
    onError: () => toast.error(t`Could not update the document.`),
  })
  const remove = useMutation({
    mutationFn: async () =>
      draftApi.DELETE('/v1/orgs/{org_id}/documents/{document_id}', {
        params: {
          path: { org_id: orgId, document_id: doc.id },
          header: ifMatch(doc.version),
        },
      }),
    onSuccess: refresh,
    onError: () => toast.error(t`Could not delete the document.`),
  })
  return (
    <li className="flex flex-wrap items-center gap-2 p-3">
      <FileTextIcon className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
      {renaming ? (
        <form
          className="flex flex-1 gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (title.trim()) patch.mutate({ title: title.trim() })
          }}
        >
          <Input
            aria-label={t`Document title`}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
          />
          <Button type="submit" size="sm">
            <Trans>Save</Trans>
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={() => setRenaming(false)}>
            <Trans>Cancel</Trans>
          </Button>
        </form>
      ) : (
        <button
          type="button"
          onClick={onPreview}
          className="min-w-0 flex-1 truncate text-start font-medium outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        >
          {doc.title}
        </button>
      )}
      <SourceBadge doc={doc} />
      <SiteDate
        value={doc.created_at}
        timeZone={timeZone}
        className="text-xs text-muted-foreground"
      />
      {canManage && !renaming ? (
        <>
          <Button
            variant="ghost"
            size="icon"
            aria-label={t`Rename ${doc.title}`}
            onClick={() => setRenaming(true)}
          >
            <PencilIcon />
          </Button>
          <Select
            className="w-36"
            aria-label={t`Move ${doc.title} to`}
            value=""
            onChange={(e) => e.target.value && patch.mutate({ folder_id: e.target.value })}
          >
            <option value="">{t`Move to…`}</option>
            {folders
              .filter((f) => f.id !== doc.folder_id)
              .map((f) => (
                <option key={f.id} value={f.id}>
                  {f.name}
                </option>
              ))}
          </Select>
          <Button
            variant="ghost"
            size="icon"
            aria-label={t`Delete ${doc.title}`}
            onClick={() => setConfirmDelete(true)}
          >
            <Trash2Icon />
          </Button>
          <ConfirmDialog
            open={confirmDelete}
            onOpenChange={setConfirmDelete}
            title={<Trans>Delete this document?</Trans>}
            description={<Trans>"{doc.title}" will be removed. This cannot be undone.</Trans>}
            confirmLabel={<Trans>Delete</Trans>}
            destructive
            busy={remove.isPending}
            onConfirm={() => remove.mutate(undefined, { onSettled: () => setConfirmDelete(false) })}
          />
        </>
      ) : null}
    </li>
  )
}

/** Folder explorer (O8): breadcrumb, subfolders, documents, upload, visibility. */
export function DocumentsPage({ folderId }: { folderId?: string }) {
  const { t } = useLingui()
  const { org, can } = useSession()
  const { site } = useSite()
  const qc = useQueryClient()
  const visibilityText = useVisibilityText()
  const orgId = org?.id ?? ''
  const canManage = can('documents.manage')
  const canRead = can('documents.read')
  const tz = site?.timezone ?? 'UTC'
  const [dialog, setDialog] = useState<'new' | 'settings' | null>(null)
  const [preview, setPreview] = useState<Document | null>(null)
  const idem = useIdempotencyKey()

  const folder = useQuery({
    queryKey: ['operations.getFolder', orgId, folderId],
    enabled: !!folderId,
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/folders/{folder_id}', {
          params: { path: { org_id: orgId, folder_id: folderId! } },
        }),
      ),
  })
  const subfolders = useCursorQuery<Folder>(
    ['operations.listFolders', orgId, site?.id, folderId ?? null],
    async (cursor) =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/folders', {
          params: {
            path: { org_id: orgId },
            query: { site_id: site!.id, parent_id: folderId, limit: 100, cursor },
          },
        }),
      ),
    { enabled: !!site && canRead },
  )
  const docs = useCursorQuery<Document>(
    ['operations.listDocuments', orgId, site?.id, folderId ?? null],
    async (cursor) =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/documents', {
          params: {
            path: { org_id: orgId },
            query: { site_id: site!.id, folder_id: folderId, limit: 100, cursor },
          },
        }),
      ),
    { enabled: !!site && canRead && !!folderId },
  )

  // Upload through the shared component, then file each upload as a document in this folder.
  const uploads = useUploads({
    orgId,
    siteId: site?.id,
    scope: 'document',
    profile: 'document',
  })
  const [filing, setFiling] = useState(false)
  const names = new Map(uploads.items.map((i) => [i.file?.id, i.name]))
  async function fileUploads() {
    setFiling(true)
    try {
      for (const file of uploads.files) {
        await draftApi.POST('/v1/orgs/{org_id}/documents', {
          params: { path: { org_id: orgId } },
          body: {
            site_id: site!.id,
            folder_id: folderId!,
            title: names.get(file.id) ?? file.id,
            file_id: file.id,
          },
          ...withIdempotencyKey(`${idem.key}-${file.id}`),
        })
      }
      uploads.clear()
      idem.renew()
      await qc.invalidateQueries({ queryKey: ['operations.listDocuments'] })
    } catch {
      toast.error(t`Could not add the files. Please try again.`)
    } finally {
      setFiling(false)
    }
  }

  const canUploadHere =
    !!folderId &&
    !!folder.data &&
    (canManage || (can('documents.upload_own') && folder.data.kind === 'unit'))

  if (!canRead) {
    return (
      <EmptyState
        icon={FolderLockIcon}
        title={<Trans>You do not have access to documents</Trans>}
        description={<Trans>Ask your manager if you think you should.</Trans>}
      />
    )
  }
  const crumbs = folder.data?.breadcrumb ?? []

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="me-auto text-2xl font-semibold">
          <Trans>Documents</Trans>
        </h1>
        <SitePicker />
        {canManage ? (
          <Button variant="outline" onClick={() => setDialog('new')}>
            <FolderPlusIcon aria-hidden="true" />
            <Trans>New folder</Trans>
          </Button>
        ) : null}
        {canManage && folder.data ? (
          <Button variant="outline" onClick={() => setDialog('settings')}>
            <SettingsIcon aria-hidden="true" />
            <Trans>Folder settings</Trans>
          </Button>
        ) : null}
      </div>

      <nav aria-label={t`Folder path`}>
        <ol className="flex flex-wrap items-center gap-1 text-sm">
          <li>
            <Link
              to="/orgs/$orgId/documents"
              params={{ orgId }}
              className="text-primary hover:underline"
            >
              <Trans>All folders</Trans>
            </Link>
          </li>
          {crumbs.map((c, i) => (
            <li key={c.id} className="flex items-center gap-1">
              <span aria-hidden="true">/</span>
              {i === crumbs.length - 1 ? (
                <span aria-current="page" className="font-medium">
                  {c.name}
                </span>
              ) : (
                <Link
                  to="/orgs/$orgId/documents/f/$folderId"
                  params={{ orgId, folderId: c.id }}
                  className="text-primary hover:underline"
                >
                  {c.name}
                </Link>
              )}
            </li>
          ))}
        </ol>
      </nav>

      {folderId && folder.isError ? (
        <ErrorState error={folder.error} onRetry={() => void folder.refetch()} />
      ) : null}

      {subfolders.isPending ? (
        <TableSkeleton rows={3} columns={2} />
      ) : subfolders.isError ? (
        <ErrorState error={subfolders.error} onRetry={() => void subfolders.refetch()} />
      ) : subfolders.items.length > 0 ? (
        <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3" aria-label={t`Folders`}>
          {subfolders.items.map((f) => (
            <li key={f.id}>
              <Link
                to="/orgs/$orgId/documents/f/$folderId"
                params={{ orgId, folderId: f.id }}
                className="flex items-center gap-3 rounded-lg border p-3 outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
              >
                {f.kind === 'unit' ? (
                  <FolderLockIcon className="size-5 text-muted-foreground" aria-hidden="true" />
                ) : (
                  <FolderIcon className="size-5 text-muted-foreground" aria-hidden="true" />
                )}
                <span className="min-w-0 flex-1 truncate font-medium">{f.name}</span>
                <Badge variant="outline">
                  {f.kind === 'unit' ? (
                    <Trans>Private to the unit</Trans>
                  ) : (
                    visibilityText(f.visibility)
                  )}
                </Badge>
              </Link>
            </li>
          ))}
        </ul>
      ) : null}

      {folderId ? (
        docs.isPending ? (
          <TableSkeleton rows={3} columns={3} />
        ) : docs.isError ? (
          <ErrorState error={docs.error} onRetry={() => void docs.refetch()} />
        ) : docs.items.length === 0 && subfolders.items.length === 0 ? (
          <EmptyState
            icon={FileTextIcon}
            title={<Trans>This folder is empty</Trans>}
            description={canUploadHere ? <Trans>Upload a file below.</Trans> : undefined}
          />
        ) : docs.items.length > 0 ? (
          <>
            <ul className="flex flex-col divide-y rounded-lg border" aria-label={t`Documents`}>
              {docs.items.map((d) => (
                <DocumentRow
                  key={d.id}
                  orgId={orgId}
                  doc={d}
                  folders={subfolders.items}
                  canManage={canManage}
                  timeZone={tz}
                  onPreview={() => setPreview(d)}
                />
              ))}
            </ul>
            <LoadMore query={docs} />
          </>
        ) : null
      ) : subfolders.items.length === 0 && !subfolders.isPending && !subfolders.isError ? (
        <EmptyState
          icon={FolderIcon}
          title={<Trans>No folders yet</Trans>}
          description={
            canManage ? <Trans>Create a folder to start filing documents.</Trans> : undefined
          }
        />
      ) : null}

      {canUploadHere ? (
        <div className="flex flex-col gap-2">
          <UploadField controller={uploads} orgId={orgId} />
          {uploads.files.length > 0 && !uploads.busy ? (
            <Button className="w-fit" disabled={filing} onClick={() => void fileUploads()}>
              <Trans>Add to this folder</Trans>
            </Button>
          ) : null}
        </div>
      ) : null}

      {dialog === 'new' && site ? (
        <NewFolderDialog
          orgId={orgId}
          siteId={site.id}
          parentId={folderId ?? null}
          onClose={() => setDialog(null)}
        />
      ) : null}
      {dialog === 'settings' && site && folder.data ? (
        <FolderSettingsDialog
          orgId={orgId}
          siteId={site.id}
          folder={folder.data}
          onClose={() => setDialog(null)}
        />
      ) : null}
      {preview ? (
        <PreviewDialog orgId={orgId} doc={preview} onClose={() => setPreview(null)} />
      ) : null}
    </div>
  )
}
