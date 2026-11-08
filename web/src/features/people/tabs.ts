export const PERSON_TABS = ['profile', 'units', 'roles', 'invitations'] as const
export type PersonTab = (typeof PERSON_TABS)[number]
