import { describe, expect, it } from 'vitest'
import { inAppPath } from './NotificationsPage'

describe('inAppPath (notification links)', () => {
  it('follows in-app paths, keeping query and fragment', () => {
    expect(inAppPath('/orgs/org-1/tickets/t-1')).toBe('/orgs/org-1/tickets/t-1')
    expect(inAppPath('/orgs/org-1/tickets?view=queue#top')).toBe(
      '/orgs/org-1/tickets?view=queue#top',
    )
  })

  it('rejects anything that can leave the site', () => {
    for (const bad of [
      '//evil.example/x', // protocol-relative
      '/\\evil.example/x', // browsers read the backslash as a slash
      '/\\\\evil.example',
      '/ok\\path',
      'https://evil.example/x',
      'http://localhost/x',
      'javascript:alert(1)',
      'orgs/org-1',
      '',
    ]) {
      expect(inAppPath(bad), bad).toBeNull()
    }
  })
})
