import { beforeEach, describe, expect, it } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { discoveryFixture, meFixture } from '@/test/handlers'
import { server } from '@/test/setup'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { MoneyInput } from '@/components/forms/MoneyInput'
import { useLocale } from './locale'
import { Providers, createQueryClient } from './providers'
import { instanceQueryOptions, supportedAuthMethods } from './instance'
import { defaultLanding, loadSession } from './session'

beforeEach(() => localStorage.clear())

describe('discovery document', () => {
  it('loads from /.well-known/openbekci.json', async () => {
    const doc = await new QueryClient().fetchQuery(instanceQueryOptions)
    expect(doc.instance_name).toBe('Test Instance')
  })

  it('offers known methods only, phone (SMS) first', () => {
    expect(supportedAuthMethods(discoveryFixture)).toEqual(['sms_otp', 'password', 'magic_link'])
    expect(supportedAuthMethods(undefined)).toEqual([])
  })
})

describe('session', () => {
  it('loads /v1/me', async () => {
    const me = await loadSession(new QueryClient())
    expect(me?.user.display_name).toBe(meFixture.user.display_name)
  })

  it('returns null on 401 instead of throwing', async () => {
    server.use(
      http.get('*/v1/me', () =>
        HttpResponse.json({ status: 401, code: 'invalid_grant' }, { status: 401 }),
      ),
    )
    expect(await loadSession(new QueryClient())).toBeNull()
  })

  it('lands on the only organization', () => {
    expect(defaultLanding(meFixture)).toEqual({ to: '/orgs/$orgId', params: { orgId: 'org-1' } })
  })

  it('asks to choose when there are several and none is remembered', () => {
    const two = {
      ...meFixture,
      organizations: [...meFixture.organizations, { ...meFixture.organizations[0]!, id: 'org-2' }],
    }
    expect(defaultLanding(two)).toEqual({ to: '/select-org' })
    localStorage.setItem('ob.lastOrg', 'org-2')
    expect(defaultLanding(two)).toEqual({ to: '/orgs/$orgId', params: { orgId: 'org-2' } })
  })
})

function renderIn(ui: React.ReactElement, locale: 'en' | 'tr') {
  localStorage.setItem('ob.locale', locale) // explicit choice, so /v1/me cannot override it
  return render(
    <Providers queryClient={createQueryClient()} locale={locale}>
      {ui}
    </Providers>,
  )
}

describe('display components', () => {
  it('Money formats minor units per locale', async () => {
    renderIn(<Money minor={123456} currency="TRY" />, 'tr')
    await waitFor(() => expect(screen.getByText(/1\.234,56/)).toBeInTheDocument())
  })

  it('SiteDate uses the site timezone', async () => {
    renderIn(<SiteDate value="2026-12-31T22:30:00Z" timeZone="Europe/Istanbul" />, 'en')
    expect(await screen.findByText('Jan 1, 2027')).toBeInTheDocument()
  })

  it('MoneyInput reports minor units and normalises on blur', async () => {
    function Harness() {
      const [v, setV] = useState<bigint | null>(null)
      return (
        <>
          <MoneyInput aria-label="Amount" value={v} onChange={setV} currency="TRY" />
          <output data-testid="minor">{v === null ? 'null' : v.toString()}</output>
        </>
      )
    }
    renderIn(<Harness />, 'tr')
    const input = screen.getByLabelText('Amount')
    await userEvent.type(input, '1.234,5')
    expect(screen.getByTestId('minor')).toHaveTextContent('123450')
    await userEvent.tab()
    expect(input).toHaveValue('1234,50')
  })

  it('MoneyInput reformats the amount when the locale changes', async () => {
    function Harness() {
      const [v, setV] = useState<bigint | null>(123456n)
      const { locale, setLocale } = useLocale()
      return (
        <>
          <MoneyInput aria-label="Amount" value={v} onChange={setV} currency="TRY" />
          <button onClick={() => setLocale(locale === 'tr' ? 'en' : 'tr')}>toggle</button>
        </>
      )
    }
    renderIn(<Harness />, 'tr')
    const input = screen.getByLabelText('Amount')
    await waitFor(() => expect(input).toHaveValue('1234,56'))
    await userEvent.click(screen.getByText('toggle'))
    await waitFor(() => expect(input).toHaveValue('1234.56'))
  })
})
