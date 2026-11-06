import { http, HttpResponse } from 'msw'
import type { DiscoveryDocument, Me } from '@/api/schema'

/** Hand-written MSW handlers. Generated handlers (openapi-msw) replace these when the spec lands. */

export const discoveryFixture: DiscoveryDocument = {
  instance_id: '0194a1f0-0000-7000-8000-000000000001',
  instance_name: 'Test Instance',
  api_base_url: 'http://localhost/v1',
  api_versions: ['v1'],
  server_version: '0.1.0',
  sdk_version: null,
  auth_methods: [
    { id: 'password' },
    { id: 'sms_otp' },
    { id: 'magic_link' },
    { id: 'oidc:google' },
  ],
  packs: ['tr'],
  branding: { primary_color: '#0f766e' },
}

export const meFixture: Me = {
  user: {
    id: 'u1',
    display_name: 'Ayşe Yılmaz',
    email: 'ayse@example.com',
    phone: '+905551112233',
    locale: 'tr',
  },
  organizations: [
    {
      id: 'org-1',
      name: 'Güneş Sitesi Yönetimi',
      default_locale: 'tr',
      roles: ['site_manager'],
      permissions: [
        { permission: 'site.read' },
        { permission: 'ledger.read', site_ids: ['site-a'] },
      ],
      sites: [{ id: 'site-a', name: 'Güneş Sitesi', timezone: 'Europe/Istanbul', currency: 'TRY' }],
    },
  ],
  occupancies: [
    {
      id: 'occ-1',
      organization_id: 'org-1',
      site_id: 'site-a',
      unit_id: 'unit-12',
      unit_label: 'A Blok 12',
      kind: 'owner',
      may_see_money: true,
    },
  ],
}

export const handlers = [
  http.get('*/.well-known/openbekci.json', () => HttpResponse.json(discoveryFixture)),
  http.get('*/v1/me', () => HttpResponse.json(meFixture)),
]
