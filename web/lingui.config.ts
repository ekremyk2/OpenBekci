import { defineConfig } from '@lingui/cli'
import { formatter } from '@lingui/format-po'

// `pseudo` is generated from the English source at compile time. `pseudo-rtl` reuses the
// `pseudo` catalog and only flips `dir` (see src/lib/i18n.ts).
export default defineConfig({
  sourceLocale: 'en',
  locales: ['en', 'tr', 'pseudo'],
  pseudoLocale: 'pseudo',
  fallbackLocales: { default: 'en', 'tr-TR': 'tr' },
  format: formatter({ origins: false, lineNumbers: false }),
  catalogs: [
    {
      path: '<rootDir>/src/locales/{locale}/messages',
      include: ['src'],
      exclude: ['**/*.test.*', '**/routeTree.gen.ts'],
    },
  ],
})
