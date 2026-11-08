import js from '@eslint/js'
import globals from 'globals'
import tseslint from 'typescript-eslint'
import reactHooks from 'eslint-plugin-react-hooks'
import jsxA11y from 'eslint-plugin-jsx-a11y'
import openbekci from './eslint-rules/logical-properties.js'

export default tseslint.config(
  {
    ignores: [
      'dist',
      'node_modules',
      'playwright-report',
      'test-results',
      'src/routeTree.gen.ts',
      'src/api/clients-ts-generated',
      'public',
      'src/mocks/sw',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  jsxA11y.flatConfigs.recommended,
  {
    files: ['src/**/*.{ts,tsx}'],
    languageOptions: { globals: globals.browser },
    plugins: { 'react-hooks': reactHooks, openbekci },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'openbekci/logical-properties': 'error',
      'no-restricted-syntax': [
        'error',
        {
          selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']",
          message: 'dangerouslySetInnerHTML is forbidden (CSP and XSS policy).',
        },
      ],
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
    },
  },
  {
    // our own form primitives wrap native controls: count them as controls for label association
    files: ['src/**/*.tsx'],
    rules: {
      'jsx-a11y/label-has-associated-control': [
        'error',
        {
          controlComponents: ['Input', 'Select', 'Checkbox', 'Textarea'],
          assert: 'either',
          depth: 3,
        },
      ],
    },
  },
  {
    // generated-style primitives export variants next to components
    files: ['src/components/ui/**/*.tsx'],
    rules: { 'jsx-a11y/heading-has-content': 'off' },
  },
  {
    files: ['*.config.{js,ts}', '.size-limit.mjs', 'eslint-rules/**', 'scripts/**', 'e2e/**'],
    languageOptions: { globals: globals.node },
  },
)
