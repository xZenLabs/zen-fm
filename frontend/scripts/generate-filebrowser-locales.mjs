#!/usr/bin/env node

// Generates ZenFM's checked-in partial translations from File Browser's
// Apache-2.0 locale catalog. ZenFM-specific copy deliberately falls back to
// English instead of being machine translated.

import { readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const frontendDirectory = path.resolve(scriptDirectory, '..')
const defaultSource = path.resolve(frontendDirectory, '..', '..', 'filebrowser', 'frontend', 'src', 'i18n')
const sourceDirectory = path.resolve(process.argv[2] ?? defaultSource)
const outputFile = path.join(frontendDirectory, 'src', 'locales', 'translations.ts')

const locales = {
  ar: 'ar', bg: 'bg', ca: 'ca', cs: 'cs', de: 'de', el: 'el', es: 'es', fa: 'fa', fr: 'fr',
  he: 'he', hr: 'hr', hu: 'hu', is: 'is', it: 'it', ja: 'ja', ko: 'ko', lv: 'lv', nl: 'nl',
  'nl-BE': 'nl-be', no: 'no', pl: 'pl', 'pt-BR': 'pt-br', 'pt-PT': 'pt-pt', ro: 'ro', ru: 'ru',
  sk: 'sk', 'sv-SE': 'sv-se', tr: 'tr', uk: 'uk', vi: 'vi', 'zh-CN': 'zh-cn', 'zh-TW': 'zh-tw',
}

// Only phrases with the same meaning in both applications are reused.
const mappings = {
  'nav.files': 'files.files',
  'nav.settings': 'sidebar.settings',
  'nav.logout': 'sidebar.logout',
  'auth.username': 'login.username',
  'auth.password': 'login.password',
  'auth.signIn': 'login.submit',
  'auth.failed': 'login.wrongCredentials',
  'auth.newPassword': 'settings.newPassword',
  'auth.confirmPassword': 'settings.newPasswordConfirm',
  'files.search': 'search.search',
  'files.upload': 'buttons.upload',
  'files.newFolder': 'sidebar.newFolder',
  'files.empty': 'files.lonely',
  'files.name': 'files.name',
  'files.size': 'files.size',
  'files.modified': 'files.lastModified',
  'files.preview': 'buttons.preview',
  'files.edit': 'buttons.editAsText',
  'files.rename': 'buttons.rename',
  'files.move': 'buttons.move',
  'files.copy': 'buttons.copy',
  'files.delete': 'buttons.delete',
  'files.download': 'buttons.download',
  'files.share': 'buttons.share',
  'files.save': 'buttons.saveChanges',
  'files.editor': 'buttons.editAsText',
  'files.noPreview': 'files.noPreview',
  'files.clearSearch': 'buttons.clear',
  'shares.title': 'settings.shareManagement',
  'shares.path': 'settings.path',
  'shares.password': 'prompts.optionalPassword',
  'shares.copy': 'buttons.copyDownloadLinkToClipboard',
  'shares.download': 'buttons.download',
  'settings.title': 'sidebar.settings',
  'settings.theme': 'settings.themes.title',
  'settings.language': 'settings.language',
  'settings.light': 'settings.themes.light',
  'settings.dark': 'settings.themes.dark',
  'settings.system': 'settings.themes.default',
  'settings.password': 'settings.changePassword',
  'settings.currentPassword': 'settings.currentPassword',
  'settings.newPassword': 'settings.newPassword',
  'settings.changePassword': 'settings.changePassword',
  'settings.save': 'buttons.save',
  'common.cancel': 'buttons.cancel',
  'common.create': 'buttons.create',
  'common.close': 'buttons.close',
  'common.confirm': 'buttons.ok',
  'common.loading': 'files.loading',
  'common.copied': 'success.linkCopied',
  'common.error': 'prompts.error',
}

const englishCatalog = JSON.parse(await readFile(path.join(sourceDirectory, 'en.json'), 'utf8'))

function get(object, dottedPath) {
  return dottedPath.split('.').reduce((value, key) => value?.[key], object)
}

function set(object, dottedPath, value) {
  const parts = dottedPath.split('.')
  let cursor = object
  for (const part of parts.slice(0, -1)) cursor = cursor[part] ??= {}
  cursor[parts.at(-1)] = value
}

const resources = {}
for (const [locale, fileName] of Object.entries(locales)) {
  const catalog = JSON.parse(await readFile(path.join(sourceDirectory, `${fileName}.json`), 'utf8'))
  const translation = {}
  for (const [zenKey, fileBrowserKey] of Object.entries(mappings)) {
    const value = get(catalog, fileBrowserKey)
    const englishValue = get(englishCatalog, fileBrowserKey)
    if (typeof value === 'string' && value.trim() !== '' && value !== englishValue) set(translation, zenKey, value)
  }
  resources[locale] = { translation }
}

const banner = `// Generated from File Browser by scripts/generate-filebrowser-locales.mjs\n// commit 833d908884d5c801f30f5c098d7977177eb3a36b (Apache-2.0).\n\n`
await writeFile(outputFile, `${banner}export const fileBrowserTranslations = ${JSON.stringify(resources, null, 2)} as const\n`)
console.log(`Wrote ${path.relative(frontendDirectory, outputFile)} from ${sourceDirectory}`)
