import { useLingui } from '@lingui/react/macro'
import { SectionNav } from '@/components/layout/SectionNav'

export function PeopleNav() {
  const { t } = useLingui()
  return (
    <SectionNav
      label={t`People`}
      links={[
        { id: 'persons', label: t`Persons`, to: '/orgs/$orgId/people', permission: 'people.read' },
        {
          id: 'invitations',
          label: t`Invitations`,
          to: '/orgs/$orgId/people/invitations',
          permission: 'invitations.manage',
        },
        {
          id: 'claims',
          label: t`Unit claims`,
          to: '/orgs/$orgId/claims',
          permission: 'claims.decide',
        },
        {
          id: 'directory',
          label: t`Directory`,
          to: '/orgs/$orgId/directory',
          permission: 'directory.read',
        },
        {
          id: 'vendors',
          label: t`Vendors`,
          to: '/orgs/$orgId/vendors',
          permission: 'vendors.read',
        },
      ]}
    />
  )
}
