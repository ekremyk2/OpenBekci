import { LanguagesIcon, MonitorIcon, MoonIcon, SunIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useLocale } from '@/app/locale'
import { ACCENTS, useTheme, type Accent, type ThemePreference } from '@/app/theme'
import { USER_LOCALES, isLocale } from '@/lib/i18n'

// swatch = the light-mode primary of each palette in src/index.css (brand = the default blue)
const SWATCH: Record<Accent, string> = {
  brand: '#2563eb',
  slate: 'oklch(0.42 0.03 255)',
  forest: 'oklch(0.45 0.09 155)',
  terracotta: 'oklch(0.5 0.12 40)',
  navy: 'oklch(0.38 0.1 255)',
}

const LOCALE_NAMES: Record<string, string> = { en: 'English', tr: 'Türkçe' }

export function LocaleToggle() {
  const { locale, setLocale } = useLocale()
  const { t } = useLingui()
  // Developers can reach pseudo locales with ?locale=pseudo; keep the active one visible.
  const options = USER_LOCALES.includes(locale) ? USER_LOCALES : [...USER_LOCALES, locale]
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={t`Language`}>
          <LanguagesIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuRadioGroup
          value={locale}
          onValueChange={(v) => {
            if (isLocale(v)) setLocale(v)
          }}
        >
          {options.map((l) => (
            <DropdownMenuRadioItem key={l} value={l} lang={l.startsWith('pseudo') ? 'en' : l}>
              {LOCALE_NAMES[l] ?? l}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function ThemeToggle() {
  const { preference, resolved, accent, setPreference, setAccent } = useTheme()
  const { t } = useLingui()
  const accentNames: Record<Accent, string> = {
    brand: t`Organization color`,
    slate: t`Slate`,
    forest: t`Forest`,
    terracotta: t`Terracotta`,
    navy: t`Navy`,
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={t`Theme`}>
          {resolved === 'dark' ? <MoonIcon /> : <SunIcon />}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuRadioGroup
          value={preference}
          onValueChange={(v) => setPreference(v as ThemePreference)}
        >
          <DropdownMenuRadioItem value="system">
            <MonitorIcon className="size-4" /> <Trans>System</Trans>
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="light">
            <SunIcon className="size-4" /> <Trans>Light</Trans>
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark">
            <MoonIcon className="size-4" /> <Trans>Dark</Trans>
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>
          <Trans>Accent</Trans>
        </DropdownMenuLabel>
        <DropdownMenuRadioGroup value={accent} onValueChange={(v) => setAccent(v as Accent)}>
          {ACCENTS.map((a) => (
            <DropdownMenuRadioItem key={a} value={a}>
              <span
                className="size-4 rounded-full border"
                style={{ background: SWATCH[a] }}
                aria-hidden="true"
              />{' '}
              {accentNames[a]}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
