import { useMemo, useState, type ReactNode } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { PlusIcon, Trash2Icon } from 'lucide-react'
import { AppError } from '@/api/errors'
import { FormField as Field, FormError, SubmitButton } from '@/components/forms/FormField'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { problemMessage } from '@/lib/labels'
import {
  buildValues,
  getAt,
  groupsOf,
  humanize,
  isObjectSchema,
  isSecret,
  MAX_ARRAY_ROWS,
  mapServerIssues,
  omit,
  setAt,
  typeOf,
  uiOf,
  withDefaults,
  type FieldDef,
  type Issue,
  type JsonSchema,
  type Values,
} from './schema'

export interface SchemaFormProps {
  /** An object schema (the settings of one pack scope). */
  schema: JsonSchema
  /** Current non-secret values; secrets are never passed in. */
  values?: Values
  /** Field paths of the secret fields that are set on the server ("set / not set"). */
  secretsSet?: readonly string[]
  /** Receives the settings document: typed values plus the secrets that were typed or cleared. */
  onSubmit: (values: Values) => unknown
  submitLabel: ReactNode
  busy?: boolean
  disabled?: boolean
  /** Failure of the last submit; a 422 is mapped onto the fields it names. */
  error?: unknown
  /** Resolves a label key (`x-ui.label`, `x-ui.help`, group titles) in the user's locale. */
  resolveText?: (key: string) => string | undefined
  /** Removed from property names when no title is given (`example_` of `example_api_key`). */
  stripPrefix?: string
  idPrefix?: string
  /** Rendered next to the submit button. */
  actions?: ReactNode
}

/**
 * Form rendered from a JSON Schema subset with `x-ui` and `x-secret` (web plan §12). The values
 * are the stored non-secret settings; secrets are write-only inputs next to a "set / not set"
 * badge. Client validation runs on submit; the server's field errors are mapped back.
 */
export function SchemaForm({
  schema,
  values,
  secretsSet = [],
  onSubmit,
  submitLabel,
  busy = false,
  disabled = false,
  error,
  resolveText,
  stripPrefix = '',
  idPrefix = 'sf',
  actions,
}: SchemaFormProps) {
  const { t, i18n } = useLingui()
  const [raw, setRaw] = useState<Values>(() => withDefaults(schema, values))
  const [secrets, setSecrets] = useState<Record<string, string | null>>({})
  const [issues, setIssues] = useState<Record<string, Issue>>({})
  const setSet = useMemo(() => new Set(secretsSet), [secretsSet])

  const server = useMemo(
    () =>
      error instanceof AppError && error.isValidation
        ? mapServerIssues(schema, error.fieldErrors)
        : null,
    [error, schema],
  )

  const issueMessage = (issue: Issue): string => {
    const p = issue.params ?? {}
    switch (issue.code) {
      case 'required':
        return t`This field is required`
      case 'invalid_number':
        return t`Enter a number`
      case 'not_integer':
        return t`Enter a whole number`
      case 'min':
        return t`Must be at least ${p.min}`
      case 'max':
        return t`Must be at most ${p.max}`
      case 'min_length':
        return t`Use at least ${p.min} characters`
      case 'max_length':
        return t`Use at most ${p.max} characters`
      case 'pattern':
        return t`This value does not have the expected format`
      case 'invalid_url':
        return t`Enter a full web address, for example https://example.org`
      case 'invalid_email':
        return t`Enter an email address`
      case 'min_items':
        return t`Add at least ${p.min} values`
      case 'max_items':
        return t`Add at most ${p.max} values`
      default:
        return t`This value is not valid`
    }
  }
  const serverMessage = (i: { code: string; message?: string }) => {
    if (i.message) return i.message
    return i.code === 'required'
      ? t`This field is required`
      : i.code === 'unknown_field'
        ? t`This setting is not known to the pack`
        : t`This value is not valid`
  }
  const errorFor = (path: string): string | undefined => {
    const local = issues[path]
    if (local) return issueMessage(local)
    const remote = server?.fields[path]
    return remote ? serverMessage(remote) : undefined
  }

  const rootMessage = (() => {
    if (!error) return undefined
    if (error instanceof AppError) {
      if (error.isValidation && server && server.general.length > 0) {
        return server.general.map(serverMessage).join(' ')
      }
      if (error.isValidation) {
        return Object.keys(server?.fields ?? {}).length > 0
          ? undefined
          : t`The settings were refused. Check the values and try again.`
      }
      if (error.isReauthRequired) return undefined
      if (error.isForbidden) return t`You do not have permission to do this.`
      const known = problemMessage(error.code, i18n)
      if (known) return known
      if (error.code === 'pack_error') return t`The pack could not apply these settings.`
    }
    return t`Something went wrong. Please try again.`
  })()

  const labelOf = (f: FieldDef): string => {
    const ui = uiOf(f.schema)
    return (ui.label && resolveText?.(ui.label)) || f.schema.title || humanize(f.name, stripPrefix)
  }
  const helpOf = (f: FieldDef): string | undefined => {
    const ui = uiOf(f.schema)
    return (ui.help && resolveText?.(ui.help)) || f.schema.description || undefined
  }

  const inputId = (path: string) => `${idPrefix}-${path.replace(/\./g, '-')}`
  const setValue = (path: string, value: unknown) => {
    setRaw((r) => setAt(r, path, value))
    setIssues((i) => (path in i ? omit(i, path) : i))
  }

  function submit(e: React.FormEvent) {
    e.preventDefault()
    const result = buildValues({ schema, raw, secrets, secretsSet: setSet })
    setIssues(result.issues)
    if (Object.keys(result.issues).length > 0) {
      const first = Object.keys(result.issues)[0]!
      document.getElementById(inputId(first))?.focus()
      return
    }
    void Promise.resolve(onSubmit(result.values)).catch(() => undefined)
  }

  /* ───────── controls ───────── */

  function describedBy(f: FieldDef) {
    const ids: string[] = []
    if (helpOf(f)) ids.push(`${inputId(f.path)}-hint`)
    if (errorFor(f.path)) ids.push(`${inputId(f.path)}-error`)
    return ids.join(' ') || undefined
  }

  function renderSecret(f: FieldDef) {
    const id = inputId(f.path)
    const isSet = setSet.has(f.path)
    const draft = secrets[f.path]
    const clearing = draft === null
    const ui = uiOf(f.schema)
    return (
      <Field
        key={f.path}
        id={id}
        label={
          <span className="flex flex-wrap items-center gap-2">
            {labelOf(f)}
            {f.required ? <RequiredMark /> : null}
            <Badge variant={isSet && !clearing ? 'success' : 'secondary'}>
              {clearing ? (
                <Trans>Will be cleared</Trans>
              ) : isSet ? (
                <Trans>Set</Trans>
              ) : (
                <Trans>Not set</Trans>
              )}
            </Badge>
          </span>
        }
        error={errorFor(f.path)}
        hint={helpOf(f)}
      >
        <div className="flex gap-2">
          <Input
            id={id}
            type="password"
            autoComplete="new-password"
            spellCheck={false}
            className="flex-1"
            disabled={disabled || clearing}
            placeholder={
              ui.placeholder ?? (isSet ? t`Leave empty to keep the current value` : undefined)
            }
            value={typeof draft === 'string' ? draft : ''}
            aria-invalid={!!errorFor(f.path) || undefined}
            aria-describedby={describedBy(f)}
            onChange={(e) => {
              setSecrets((s) => ({ ...s, [f.path]: e.target.value }))
              setIssues((i) => omit(i, f.path))
            }}
          />
          {isSet && !f.required ? (
            <Button
              type="button"
              variant="outline"
              disabled={disabled}
              onClick={() =>
                setSecrets((s) => {
                  const next = { ...s }
                  if (clearing) delete next[f.path]
                  else next[f.path] = null
                  return next
                })
              }
            >
              {clearing ? <Trans>Keep</Trans> : <Trans>Clear</Trans>}
            </Button>
          ) : null}
        </div>
      </Field>
    )
  }

  function renderEnum(f: FieldDef, value: unknown) {
    const id = inputId(f.path)
    const options = f.schema.enum ?? []
    const ui = uiOf(f.schema)
    const label = (o: unknown) => resolveText?.(`${f.name}.${String(o)}`) ?? String(o)
    if (ui.widget === 'radio') {
      return (
        <fieldset key={f.path} className="flex flex-col gap-2" aria-describedby={describedBy(f)}>
          <legend className="text-sm leading-none font-medium">
            {labelOf(f)} {f.required ? <RequiredMark /> : null}
          </legend>
          {options.map((o) => (
            <label key={String(o)} className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name={id}
                className="size-4 accent-primary"
                disabled={disabled}
                checked={String(value ?? '') === String(o)}
                onChange={() => setValue(f.path, o)}
              />
              {label(o)}
            </label>
          ))}
          <FieldHelp f={f} id={id} help={helpOf(f)} error={errorFor(f.path)} />
        </fieldset>
      )
    }
    return (
      <Field
        key={f.path}
        id={id}
        label={
          <>
            {labelOf(f)} {f.required ? <RequiredMark /> : null}
          </>
        }
        error={errorFor(f.path)}
        hint={helpOf(f)}
      >
        <Select
          id={id}
          disabled={disabled}
          value={value === undefined || value === null ? '' : String(value)}
          aria-invalid={!!errorFor(f.path) || undefined}
          aria-describedby={describedBy(f)}
          onChange={(e) => {
            const picked = options.find((o) => String(o) === e.target.value)
            setValue(f.path, picked)
          }}
        >
          <option value="">{f.required ? t`Choose…` : '—'}</option>
          {options.map((o) => (
            <option key={String(o)} value={String(o)}>
              {label(o)}
            </option>
          ))}
        </Select>
      </Field>
    )
  }

  function renderArray(f: FieldDef, value: unknown) {
    const id = inputId(f.path)
    const items = f.schema.items ?? {}
    const list = Array.isArray(value) ? (value as unknown[]) : []
    if (items.enum) {
      const label = (o: unknown) => resolveText?.(`${f.name}.${String(o)}`) ?? String(o)
      return (
        <fieldset key={f.path} className="flex flex-col gap-2" aria-describedby={describedBy(f)}>
          <legend className="text-sm leading-none font-medium">
            {labelOf(f)} {f.required ? <RequiredMark /> : null}
          </legend>
          {items.enum.map((o) => (
            <label key={String(o)} className="flex items-center gap-2 text-sm">
              <Checkbox
                disabled={disabled}
                checked={list.some((x) => String(x) === String(o))}
                onChange={(e) =>
                  setValue(
                    f.path,
                    e.target.checked ? [...list, o] : list.filter((x) => String(x) !== String(o)),
                  )
                }
              />
              {label(o)}
            </label>
          ))}
          <FieldHelp f={f} id={id} help={helpOf(f)} error={errorFor(f.path)} />
        </fieldset>
      )
    }
    const itemType = typeOf(items)
    const numeric = itemType === 'number' || itemType === 'integer'
    return (
      <fieldset key={f.path} className="flex flex-col gap-2" aria-describedby={describedBy(f)}>
        <legend className="text-sm leading-none font-medium">
          {labelOf(f)} {f.required ? <RequiredMark /> : null}
        </legend>
        {list.map((row, i) => {
          const rowPath = `${f.path}.${i}`
          const rowError = errorFor(rowPath)
          return (
            <div key={i} className="flex flex-col gap-1">
              <div className="flex gap-2">
                <Input
                  id={inputId(rowPath)}
                  aria-label={t`${labelOf(f)} ${i + 1}`}
                  inputMode={numeric ? 'decimal' : undefined}
                  disabled={disabled}
                  value={row === undefined || row === null ? '' : String(row)}
                  aria-invalid={!!rowError || undefined}
                  onChange={(e) =>
                    setValue(
                      f.path,
                      list.map((x, j) => (j === i ? e.target.value : x)),
                    )
                  }
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  disabled={disabled}
                  aria-label={t`Remove ${labelOf(f)} ${i + 1}`}
                  onClick={() =>
                    setValue(
                      f.path,
                      list.filter((_, j) => j !== i),
                    )
                  }
                >
                  <Trash2Icon aria-hidden="true" />
                </Button>
              </div>
              {rowError ? (
                <p role="alert" className="text-sm text-destructive">
                  {rowError}
                </p>
              ) : null}
            </div>
          )
        })}
        <div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={
              disabled ||
              list.length >= Math.min(f.schema.maxItems ?? MAX_ARRAY_ROWS, MAX_ARRAY_ROWS)
            }
            onClick={() => setValue(f.path, [...list, ''])}
          >
            <PlusIcon aria-hidden="true" />
            <Trans>Add value</Trans>
          </Button>
        </div>
        <FieldHelp f={f} id={id} help={helpOf(f)} error={errorFor(f.path)} />
      </fieldset>
    )
  }

  function renderField(f: FieldDef): ReactNode {
    if (isSecret(f.schema)) return renderSecret(f)
    if (isObjectSchema(f.schema)) {
      return (
        <fieldset key={f.path} className="flex flex-col gap-4 rounded-md border p-4">
          <legend className="px-1 text-sm font-medium">{labelOf(f)}</legend>
          {helpOf(f) ? <p className="text-xs text-muted-foreground">{helpOf(f)}</p> : null}
          {renderGroups(f.schema, f.path)}
        </fieldset>
      )
    }
    const value = getAt(raw, f.path)
    const type = typeOf(f.schema)
    const ui = uiOf(f.schema)
    const id = inputId(f.path)
    if (f.schema.enum) return renderEnum(f, value)
    if (type === 'array') return renderArray(f, value)
    if (type === 'boolean') {
      return (
        <div key={f.path} className="flex flex-col gap-1">
          <label className="flex items-start gap-3">
            <Checkbox
              id={id}
              disabled={disabled}
              className="mt-1"
              checked={value === true}
              aria-describedby={describedBy(f)}
              onChange={(e) => setValue(f.path, e.target.checked)}
            />
            <span className="flex flex-col">
              <span className="text-sm font-medium">{labelOf(f)}</span>
            </span>
          </label>
          <FieldHelp f={f} id={id} help={helpOf(f)} error={errorFor(f.path)} indent />
        </div>
      )
    }
    const numeric = type === 'number' || type === 'integer'
    const common = {
      id,
      disabled,
      placeholder: ui.placeholder,
      'aria-invalid': !!errorFor(f.path) || undefined,
      'aria-describedby': describedBy(f),
      value: value === undefined || value === null ? '' : String(value),
    }
    return (
      <Field
        key={f.path}
        id={id}
        label={
          <>
            {labelOf(f)} {f.required ? <RequiredMark /> : null}
          </>
        }
        error={errorFor(f.path)}
        hint={helpOf(f)}
      >
        {ui.widget === 'textarea' ? (
          <Textarea {...common} onChange={(e) => setValue(f.path, e.target.value)} />
        ) : (
          <Input
            {...common}
            type={
              ui.widget === 'password'
                ? 'password'
                : ui.widget === 'email' || f.schema.format === 'email'
                  ? 'email'
                  : ui.widget === 'url' || f.schema.format === 'uri'
                    ? 'url'
                    : 'text'
            }
            inputMode={numeric ? 'decimal' : undefined}
            autoComplete={ui.widget === 'password' ? 'off' : undefined}
            onChange={(e) => setValue(f.path, e.target.value)}
          />
        )}
      </Field>
    )
  }

  function renderGroups(s: JsonSchema, base: string) {
    return groupsOf(s, base).map((g) => {
      const title = g.id
        ? (g.titleKey && resolveText?.(g.titleKey)) || resolveText?.(g.id) || humanize(g.id)
        : null
      const body = <div className="flex flex-col gap-4">{g.fields.map(renderField)}</div>
      return g.id ? (
        <fieldset key={g.id} className="flex flex-col gap-4 rounded-md border p-4">
          <legend className="px-1 text-sm font-semibold">{title}</legend>
          {body}
        </fieldset>
      ) : (
        <div key="__ungrouped">{body}</div>
      )
    })
  }

  return (
    <form noValidate className="flex max-w-xl flex-col gap-5" onSubmit={submit}>
      {renderGroups(schema, '')}
      <FormError message={rootMessage} />
      <div className="flex flex-wrap items-center gap-2">
        <SubmitButton busy={busy} disabled={disabled}>
          {submitLabel}
        </SubmitButton>
        {actions}
      </div>
    </form>
  )
}

function RequiredMark() {
  const { t } = useLingui()
  return (
    <span aria-label={t`required`} className="text-destructive">
      *
    </span>
  )
}

function FieldHelp({
  id,
  help,
  error,
  indent = false,
}: {
  f: FieldDef
  id: string
  help?: string
  error?: string
  indent?: boolean
}) {
  return (
    <>
      {help ? (
        <p id={`${id}-hint`} className={`text-xs text-muted-foreground ${indent ? 'ps-7' : ''}`}>
          {help}
        </p>
      ) : null}
      {error ? (
        <p
          id={`${id}-error`}
          role="alert"
          className={`text-sm text-destructive ${indent ? 'ps-7' : ''}`}
        >
          {error}
        </p>
      ) : null}
    </>
  )
}
