import { LanguagesIcon, MonitorIcon, MoonIcon, SunIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useLocale } from '@/app/locale'
import { useTheme, type Look, type ThemePreference } from '@/app/theme'
import { USER_LOCALES, isLocale } from '@/lib/i18n'

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
  const { preference, resolved, look, setPreference, setLook } = useTheme()
  const { t } = useLingui()
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
          <Trans>Look</Trans>
        </DropdownMenuLabel>
        <DropdownMenuRadioGroup value={look} onValueChange={(v) => setLook(v as Look)}>
          <DropdownMenuRadioItem value="kiremit">Kiremit</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="tas">Taş</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
