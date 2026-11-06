// Single switch point for the API types. When `clients/ts/index.d.ts` exists, change this to
//   export type { paths, components } from '../../../clients/ts'
// and delete schema.placeholder.d.ts.
export type { paths, components } from './schema.placeholder'

import type { components } from './schema.placeholder'

export type Me = components['schemas']['Me']
export type MeOrganization = components['schemas']['MeOrganization']
export type Occupancy = components['schemas']['Occupancy']
export type DiscoveryDocument = components['schemas']['DiscoveryDocument']
export type Permission = components['schemas']['Permission']
export type Site = components['schemas']['Site']
export type SessionRequest = components['schemas']['SessionRequest']
