import { createFileRoute } from '@tanstack/react-router'
import { DocumentsPage } from '@/features/documents/DocumentsPage'

export const Route = createFileRoute('/orgs/$orgId/documents/f/$folderId')({
  component: function FolderRoute() {
    const { folderId } = Route.useParams()
    return <DocumentsPage key={folderId} folderId={folderId} />
  },
})
