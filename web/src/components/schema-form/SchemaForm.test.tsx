import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { AppError } from '@/api/errors'
import { SchemaForm, type SchemaFormProps } from './SchemaForm'
import {
  buildValues,
  fieldsOf,
  groupsOf,
  mapServerIssues,
  withDefaults,
  type JsonSchema,
} from './schema'

const schema: JsonSchema = {
  type: 'object',
  required: ['ex_api_key', 'ex_sender'],
  properties: {
    ex_sender: {
      type: 'string',
      maxLength: 11,
      'x-ui': { order: 3, group: 'sms', label: 'ex.label.sender', help: 'ex.help.sender' },
    },
    ex_api_key: {
      type: 'string',
      minLength: 4,
      'x-secret': true,
      'x-ui': { order: 1, widget: 'password', help: 'ex.help.api_key' },
    },
    ex_retries: { type: 'integer', minimum: 0, maximum: 5, 'x-ui': { order: 2 } },
    ex_live: { type: 'boolean', default: true, title: 'Live mode' },
    ex_mode: { type: 'string', enum: ['a', 'b'], title: 'Mode' },
    ex_hosts: { type: 'array', items: { type: 'string' }, title: 'Hosts' },
    ex_channels: {
      type: 'array',
      items: { type: 'string', enum: ['sms', 'email'] },
      title: 'Channels',
    },
    ex_limits: {
      type: 'object',
      title: 'Limits',
      properties: { per_day: { type: 'number', minimum: 1, title: 'Per day' } },
    },
  },
}

const labels: Record<string, string> = {
  'ex.label.sender': 'Sender name',
  'ex.help.sender': 'Shown on the message',
  'ex.help.api_key': 'From the provider console',
  sms: 'SMS',
}

function renderForm(props: Partial<SchemaFormProps> = {}) {
  const onSubmit = vi.fn()
  const utils = render(
    <I18nProvider i18n={i18n}>
      <SchemaForm
        schema={schema}
        stripPrefix="ex_"
        resolveText={(k) => labels[k]}
        onSubmit={onSubmit}
        submitLabel="Save"
        {...props}
      />
    </I18nProvider>,
  )
  return { onSubmit, ...utils }
}

describe('SchemaForm rendering', () => {
  it('orders by x-ui.order, groups, resolves label and help keys, and humanizes the rest', () => {
    renderForm()
    const api = screen.getByLabelText(/Api key/)
    const retries = screen.getByLabelText(/Retries/)
    const sender = screen.getByLabelText(/Sender name/)
    expect(api.compareDocumentPosition(retries) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(retries.compareDocumentPosition(sender) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByText('Shown on the message')).toBeInTheDocument()
    expect(screen.getByText('From the provider console')).toBeInTheDocument()
    // group heading from the group id through the pack labels
    expect(screen.getByRole('group', { name: 'SMS' })).toContainElement(
      screen.getByLabelText(/Sender name/),
    )
    // humanized name without the pack prefix, and the schema title
    expect(screen.getByLabelText(/Retries/)).toBeInTheDocument()
    expect(screen.getByLabelText('Live mode')).toBeChecked() // schema default
    expect(screen.getByRole('group', { name: 'Limits' })).toContainElement(
      screen.getByLabelText('Per day'),
    )
  })

  it('renders enum as a select, array of enum as checkboxes and array of strings as rows', async () => {
    renderForm()
    expect(screen.getByRole('combobox', { name: 'Mode' })).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'sms' })).not.toBeChecked()
    await userEvent.click(screen.getByRole('button', { name: 'Add value' }))
    await userEvent.type(screen.getByLabelText('Hosts 1'), 'a.example.org')
    expect(screen.getByLabelText('Hosts 1')).toHaveValue('a.example.org')
    await userEvent.click(screen.getByRole('button', { name: 'Remove Hosts 1' }))
    expect(screen.queryByLabelText('Hosts 1')).not.toBeInTheDocument()
  })

  it('prefills the stored values', () => {
    renderForm({ values: { ex_sender: 'BEKCI', ex_retries: 2, ex_live: false, ex_mode: 'b' } })
    expect(screen.getByLabelText(/Sender name/)).toHaveValue('BEKCI')
    expect(screen.getByLabelText(/Retries/)).toHaveValue('2')
    expect(screen.getByLabelText('Live mode')).not.toBeChecked()
    expect(screen.getByRole('combobox', { name: 'Mode' })).toHaveValue('b')
  })
})

describe('SchemaForm secrets', () => {
  it('shows set / not set, never prefills and leaves an untouched secret out of the payload', async () => {
    const { onSubmit } = renderForm({
      values: { ex_sender: 'BEKCI' },
      secretsSet: ['ex_api_key'],
    })
    const key = screen.getByLabelText(/Api key/)
    expect(key).toHaveAttribute('type', 'password')
    expect(key).toHaveValue('')
    expect(key).toHaveAttribute('placeholder', 'Leave empty to keep the current value')
    expect(screen.getByText('Set')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSubmit).toHaveBeenCalledTimes(1)
    const sent = onSubmit.mock.calls[0]![0] as Record<string, unknown>
    expect(sent).not.toHaveProperty('ex_api_key')
    expect(sent.ex_sender).toBe('BEKCI')
  })

  it('requires an unset secret, sends a typed one and clears with null', async () => {
    const { onSubmit, rerender } = renderForm({ values: { ex_sender: 'BEKCI' } })
    expect(screen.getByText('Not set')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSubmit).not.toHaveBeenCalled()
    expect(await screen.findAllByText('This field is required')).not.toHaveLength(0)
    await userEvent.type(screen.getByLabelText(/Api key/), 'abc')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Use at least 4 characters')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText(/Api key/), 'd')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ ex_api_key: 'abcd' }))

    // an optional secret that is set can be cleared
    const optional: JsonSchema = {
      type: 'object',
      properties: { ex_token: { type: 'string', 'x-secret': true, title: 'Token' } },
    }
    rerender(
      <I18nProvider i18n={i18n}>
        <SchemaForm
          key="optional"
          schema={optional}
          secretsSet={['ex_token']}
          onSubmit={onSubmit}
          submitLabel="Save"
          idPrefix="opt"
        />
      </I18nProvider>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Clear' }))
    expect(screen.getByText('Will be cleared')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSubmit).toHaveBeenLastCalledWith({ ex_token: null })
  })
})

describe('SchemaForm validation', () => {
  it('validates numbers and coerces them in the payload', async () => {
    const { onSubmit } = renderForm({ secretsSet: ['ex_api_key'] })
    await userEvent.type(screen.getByLabelText(/Sender name/), 'BEKCI')
    await userEvent.type(screen.getByLabelText(/Retries/), '9')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Must be at most 5')).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText(/Retries/))
    await userEvent.type(screen.getByLabelText(/Retries/), '2.5')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Enter a whole number')).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText(/Retries/))
    await userEvent.type(screen.getByLabelText(/Retries/), '3')
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Mode' }), 'b')
    await userEvent.click(screen.getByRole('checkbox', { name: 'email' }))
    await userEvent.type(screen.getByLabelText('Per day'), '10')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSubmit).toHaveBeenCalledWith({
      ex_sender: 'BEKCI',
      ex_retries: 3,
      ex_live: true,
      ex_mode: 'b',
      ex_channels: ['email'],
      ex_limits: { per_day: 10 },
    })
  })

  it('maps the server field errors onto the fields and shows unmapped ones for the form', () => {
    const error = new AppError({
      status: 422,
      code: 'validation_failed',
      fieldErrors: {
        'settings.ex_sender': [{ code: 'invalid_value' }],
        'settings.ex_limits.per_day': [{ code: 'invalid_value', message: 'Too low for this pack' }],
        settings: [{ code: 'required' }],
      },
    })
    renderForm({ error, secretsSet: ['ex_api_key'] })
    const sender = screen.getByLabelText(/Sender name/)
    expect(sender).toHaveAttribute('aria-invalid', 'true')
    expect(sender).toHaveAccessibleDescription(/Shown on the message.*This value is not valid/)
    expect(screen.getByLabelText('Per day')).toHaveAccessibleDescription('Too low for this pack')
    // a required violation names the parent object: shown for the whole form
    expect(
      screen.getAllByRole('alert').some((a) => a.textContent === 'This field is required'),
    ).toBe(true)
  })
})

describe('schema helpers', () => {
  it('orders fields: ordered first by x-ui.order, then declaration order', () => {
    expect(fieldsOf(schema).map((f) => f.name)).toEqual([
      'ex_api_key',
      'ex_retries',
      'ex_sender',
      'ex_live',
      'ex_mode',
      'ex_hosts',
      'ex_channels',
      'ex_limits',
    ])
  })

  it('puts ungrouped fields first, then declared groups in order, then other group ids', () => {
    const s: JsonSchema = {
      type: 'object',
      'x-ui': {
        groups: [
          { id: 'b', order: 2 },
          { id: 'a', order: 1 },
        ],
      },
      properties: {
        p1: { type: 'string', 'x-ui': { group: 'b' } },
        p2: { type: 'string', 'x-ui': { group: 'a' } },
        p3: { type: 'string' },
        p4: { type: 'string', 'x-ui': { group: 'zz' } },
        p5: { type: 'string', 'x-ui': { hidden: true } },
      },
    }
    expect(groupsOf(s).map((g) => [g.id, g.fields.map((f) => f.name)])).toEqual([
      [null, ['p3']],
      ['a', ['p2']],
      ['b', ['p1']],
      ['zz', ['p4']],
    ])
  })

  it('buildValues drops blanks and passes unknown keys through', () => {
    const r = buildValues({
      schema,
      raw: { ex_sender: '  ', ex_retries: '', extra: 1 },
      secrets: {},
      secretsSet: new Set(['ex_api_key']),
    })
    expect(r.issues).toEqual({ ex_sender: { code: 'required' } })
    expect(r.values).toMatchObject({ extra: 1, ex_live: false })
  })

  it('mapServerIssues accepts bracket paths and the values prefix', () => {
    const out = mapServerIssues(schema, {
      'values.ex_hosts[1]': [{ code: 'invalid_value' }],
      'settings.ex_mode': [{ code: 'invalid_value' }],
      unknown: [{ code: 'invalid_value' }],
    })
    expect(Object.keys(out.fields).sort()).toEqual(['ex_hosts.1', 'ex_mode'])
    expect(out.general).toHaveLength(1)
  })
  it('does not test a server pattern against huge input, and survives an invalid pattern', () => {
    const sch: JsonSchema = {
      type: 'object',
      properties: {
        a: { type: 'string', pattern: '^(a+)+$' },
        b: { type: 'string', pattern: '([' },
      },
    }
    const t0 = Date.now()
    const r = buildValues({
      schema: sch,
      raw: { a: 'a'.repeat(100_000) + '!', b: 'x' },
      secrets: {},
      secretsSet: new Set(),
    })
    expect(Date.now() - t0).toBeLessThan(500)
    expect(r.issues).toEqual({})
    const short = buildValues({ schema: sch, raw: { a: 'ab' }, secrets: {}, secretsSet: new Set() })
    expect(short.issues).toEqual({ a: { code: 'pattern' } })
  })

  it('hidden fields are neither required, defaulted nor validated; stored values pass through', () => {
    const sch: JsonSchema = {
      type: 'object',
      required: ['h', 'v'],
      properties: {
        h: { type: 'string', default: 'stale', 'x-ui': { hidden: true } },
        k: { type: 'integer', minimum: 5, 'x-ui': { hidden: true } },
        v: { type: 'string' },
      },
    }
    const empty = withDefaults(sch, undefined)
    expect(empty).toEqual({})
    const r = buildValues({
      schema: sch,
      raw: { ...empty, v: 'x' },
      secrets: {},
      secretsSet: new Set(),
    })
    expect(r.issues).toEqual({})
    expect(r.values).toEqual({ v: 'x' })
    const stored = withDefaults(sch, { k: 1 })
    const r2 = buildValues({
      schema: sch,
      raw: { ...stored, v: 'x' },
      secrets: {},
      secretsSet: new Set(),
    })
    expect(r2.values).toEqual({ k: 1, v: 'x' })
  })

  it('parses decimal text only', () => {
    const sch: JsonSchema = { type: 'object', properties: { n: { type: 'number' } } }
    const run = (n: string) =>
      buildValues({ schema: sch, raw: { n }, secrets: {}, secretsSet: new Set() })
    expect(run('1,5').values).toEqual({ n: 1.5 })
    expect(run(' 2.25 ').values).toEqual({ n: 2.25 })
    for (const bad of ['0x10', '1e999', 'Infinity', '1,2,3', '--1'])
      expect(run(bad).issues).toEqual({ n: { code: 'invalid_number' } })
  })
})
