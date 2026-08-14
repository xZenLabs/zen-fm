import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import { fileBrowserTranslations } from './locales/filebrowser'

export const supportedLocales = [
  'en', 'ar', 'bg', 'ca', 'cs', 'de', 'el', 'es', 'fa', 'fr', 'he', 'hr', 'hu',
  'is', 'it', 'ja', 'ko', 'lv', 'nl', 'nl-BE', 'no', 'pl', 'pt-BR', 'pt-PT',
  'ro', 'ru', 'sk', 'sv-SE', 'tr', 'uk', 'vi', 'zh-CN', 'zh-TW',
] as const

export type SupportedLocale = typeof supportedLocales[number]

const localeAliases: Record<string, SupportedLocale> = {
  nb: 'no', no: 'no', pt: 'pt-PT', sv: 'sv-SE', zh: 'zh-CN',
}

export function detectLocale(language = typeof navigator === 'undefined' ? 'en' : navigator.language): SupportedLocale {
  const normalized = language.replace('_', '-').toLowerCase()
  const exact = supportedLocales.find((locale) => locale.toLowerCase() === normalized)
  if (exact) return exact
  const base = normalized.split('-')[0] ?? ''
  return localeAliases[normalized] ?? localeAliases[base] ?? supportedLocales.find((locale) => locale.toLowerCase() === base) ?? 'en'
}

const english = {
  appName: 'ZenFM',
  nav: {
    files: 'Files', shares: 'Shares', settings: 'Settings', logout: 'Sign out',
    useLightMode: 'Switch to light mode', useDarkMode: 'Switch to dark mode',
  },
  auth: {
    welcome: 'Your files, easily accessible.', username: 'Username', password: 'Password',
    signIn: 'Sign in', signingIn: 'Signing in…', failed: 'The username or password is incorrect.',
    setupTitle: 'Choose a private password', setupBody: 'Replace the temporary password before accessing your files.',
    currentPassword: 'Temporary password', newPassword: 'New password', confirmPassword: 'Confirm password',
    passwordHint: 'Use at least 12 characters.', showPassword: 'Show password', hidePassword: 'Hide password', completeSetup: 'Finish setup',
  },
  files: {
    search: 'Search this folder', upload: 'Upload', newFile: 'New file', newFolder: 'New folder', empty: 'Nothing here yet',
    emptyHint: 'Upload a file or create one.', name: 'Name', fileName: 'File name', size: 'Size', modified: 'Date modified',
    grid: 'Grid view', list: 'List view', hidden: 'Show hidden files', refresh: 'Refresh',
    preview: 'Open', edit: 'Edit', rename: 'Rename', move: 'Move', copy: 'Copy', paste: 'Paste', delete: 'Delete', download: 'Download',
    share: 'Share', checksum: 'Checksum', folderName: 'Folder name', destination: 'Destination path', destinationFolder: 'Destination folder',
    save: 'Save changes', editor: 'Text editor', editing: 'Editing {{name}}', editorUnavailable: 'This file is too large or unsupported for safe editing.', noPreview: 'Preview is unavailable for this file.',
    unsavedTitle: 'Save changes before closing?', unsavedBody: 'This file has unsaved changes.', keepEditing: 'Keep editing', discardChanges: 'Discard', saveAndClose: 'Save and close',
    uploading: 'Uploading {{name}} — {{progress}}%', uploadingBatch: 'Uploading · {{completed}} of {{count}} files complete · {{name}}', uploadingProgress: '{{uploaded}} of {{total}} — {{progress}}%', uploadProgress: 'Total upload progress', uploadEta: 'About {{eta}} remaining', searchResults: 'Search results', clearSearch: 'Clear search',
    calculatingCopy: 'Calculating total size…', copyProgress: 'Total copy progress', copyingProgress: 'Copying {{copied}} of {{total}} — {{progress}}%', copyEta: 'About {{eta}} remaining',
    conflictTitle: 'File already exists', conflictBody: '{{name}} already exists. Apply this choice to all conflicts in this upload.',
    skipAll: 'Skip all', replace: 'Replace', replaceAll: 'Replace all',
    itemsSelected: '{{count}} items selected', actionItems: '{{action}} {{count}} items', deleteItems: 'Delete {{count}} items?',
    confirmMove: 'Are you sure you want to move <filename>{{name}}</filename> to <path>{{destination}}</path>?',
  },
  shares: {
    title: 'Shared links', intro: 'Create temporary links without exposing your owner session.', empty: 'No shared links',
    create: 'Create share', path: 'File or folder path', name: 'Label', password: 'Optional password',
    expiry: 'Expires after', oneHour: '1 hour', oneDay: '1 day', oneWeek: '1 week', never: 'Never',
    copy: 'Copy link', revoke: 'Revoke', protected: 'Password protected', publicTitle: 'Shared with ZenFM', root: 'Shared files',
    unlock: 'Unlock', download: 'Download', expired: 'This link is unavailable or has expired.',
  },
  settings: {
    title: 'Settings', general: 'General', theme: 'Theme', language: 'Language',
    version: 'Version', root: 'Root',
    light: 'Light', dark: 'Dark', system: 'System', showHidden: 'Show hidden files',
    timeout: 'Client timeout', timeoutHint: 'Stop waiting for ordinary server requests after this many seconds.',
    account: 'Account', tokens: 'Personal API tokens', tokenHint: 'Tokens are shown once and never stored by this browser.',
    tokenName: 'Token name', createToken: 'Create token', revokeToken: 'Revoke', save: 'Save settings',
    password: 'Change password', currentPassword: 'Current password', newPassword: 'New password', changePassword: 'Change password',
  },
  common: { cancel: 'Cancel', create: 'Create', close: 'Close', confirm: 'Confirm', loading: 'Loading…', copied: 'Copied', error: 'Something went wrong' },
  warning: {
    http: 'This connection is using HTTP. Credentials and file contents may be visible on the network.',
    advanced: 'Advanced root mode is active. System files, device paths, and ZenFM secrets are visible and may be changed or deleted.',
  },
}

void i18n.use(initReactI18next).init({
  resources: { en: { translation: english }, ...fileBrowserTranslations },
  lng: detectLocale(),
  fallbackLng: 'en',
  supportedLngs: [...supportedLocales],
  load: 'currentOnly',
  interpolation: { escapeValue: false },
  returnNull: false,
})

function setDocumentLocale(locale: string) {
  if (typeof document === 'undefined') return
  document.documentElement.lang = locale
  document.documentElement.dir = locale === 'ar' || locale === 'he' || locale === 'fa' ? 'rtl' : 'ltr'
}

setDocumentLocale(i18n.resolvedLanguage ?? i18n.language)
i18n.on('languageChanged', setDocumentLocale)

export default i18n
