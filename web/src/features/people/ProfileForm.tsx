import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import type { Client } from 'openapi-fetch'
import { api } from '@/api/client'
import { AppError } from '@/api/errors'
import { useLocale } from '@/app/locale'
import { useSession } from '@/app/session'
import { Field, FormError, SubmitButton } from '@/components/forms/FormBits'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { problemMessage } from '@/lib/labels'
import { toast } from '@/lib/toast'
import type { paths } from '../../../../clients/ts'

// `PATCH /v1/me/profile` is in the served contract; the web client is still typed from the placeholder
const served = api as unknown as Client<paths>

/** My name and language (`me.updateProfile`); `If-Match` carries `user.version` from `/v1/me`. */
export function ProfileForm() {
  const { t } = useLingui()
  const { me } = useSession()
  const { setLocale } = useLocale()
  const qc = useQueryClient()
  const user = me!.user
  const [name, setName] = useState(user.display_name)
  const [locale, setLoc] = useState(user.locale === 'tr' ? 'tr' : 'en')
  const [error, setError] = useState<string>()
  const changed = name.trim() !== user.display_name || locale !== (user.locale ?? 'en')
  const save = useMutation({
    mutationFn: async () => {
      if (user.version === undefined) throw new Error('missing version')
      await served.PATCH('/v1/me/profile', {
        params: { header: { 'If-Match': `"${user.version}"` } },
        body: {
          ...(name.trim() !== user.display_name ? { display_name: name.trim() } : {}),
          ...(locale !== (user.locale ?? 'en') ? { locale } : {}),
        },
      })
    },
    onSuccess: async () => {
      setError(undefined)
      if (locale !== (user.locale ?? 'en')) setLocale(locale as 'en' | 'tr')
      await qc.invalidateQueries({ queryKey: ['me'] })
      toast.info(t`Profile saved.`)
    },
    onError: (e) =>
      setError(
        (e instanceof AppError ? problemMessage(e.code, { _: (d) => t(d) }) : undefined) ??
          t`Could not save your profile.`,
      ),
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Profile</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent>
        <form
          className="flex max-w-sm flex-col gap-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (changed) save.mutate()
          }}
        >
          <Field id="profile-name" label={<Trans>Display name</Trans>}>
            <Input id="profile-name" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field id="profile-locale" label={<Trans>Preferred language</Trans>}>
            <Select id="profile-locale" value={locale} onChange={(e) => setLoc(e.target.value)}>
              <option value="tr">Türkçe</option>
              <option value="en">English</option>
            </Select>
          </Field>
          <FormError message={error} />
          <SubmitButton busy={save.isPending} disabled={!changed} className="w-fit">
            <Trans>Save profile</Trans>
          </SubmitButton>
        </form>
      </CardContent>
    </Card>
  )
}
