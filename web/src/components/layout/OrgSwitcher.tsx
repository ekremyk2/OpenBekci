import { useNavigate } from '@tanstack/react-router'
import { ChevronsUpDownIcon } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useSession } from '@/app/session'

export function OrgSwitcher() {
  const { organizations, org } = useSession()
  const navigate = useNavigate()
  const { t } = useLingui()
  if (!org) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          className="min-w-0 max-w-56 shrink justify-between"
          aria-label={t`Switch organization`}
        >
          <span className="truncate">{org.name}</span>
          <ChevronsUpDownIcon className="opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-56">
        <DropdownMenuRadioGroup
          value={org.id}
          onValueChange={(orgId) => void navigate({ to: '/orgs/$orgId', params: { orgId } })}
        >
          {organizations.map((o) => (
            <DropdownMenuRadioItem key={o.id} value={o.id}>
              {o.name}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
