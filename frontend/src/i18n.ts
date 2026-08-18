import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import { fileBrowserTranslations } from './locales/translations'
import frontendEnglish from './locales/translations.en.json'
import settingsEnglish from './locales/settings.en.json'

export const zenUILocales = [
  'en', 'bg', 'cs', 'de', 'el', 'es', 'fr', 'it', 'ja', 'nl', 'pt-BR', 'pt-PT',
  'ro', 'ru', 'uk', 'vi', 'zh-CN', 'zh-HK', 'zh-MO', 'zh-TW',
] as const

export const supportedLocales = [
  'en', 'ar', 'bg', 'ca', 'cs', 'de', 'el', 'es', 'fa', 'fr', 'he', 'hr', 'hu',
  'is', 'it', 'ja', 'ko', 'lv', 'nl', 'nl-BE', 'no', 'pl', 'pt-BR', 'pt-PT',
  'ro', 'ru', 'sk', 'sv-SE', 'tr', 'uk', 'vi', 'zh-CN', 'zh-HK', 'zh-MO', 'zh-TW',
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

const english = { ...frontendEnglish, settings: settingsEnglish }

type TranslationTree = Record<string, unknown>
const fileBrowserResources = fileBrowserTranslations as unknown as Record<string, { translation: TranslationTree }>
const localizedResources = Object.fromEntries(supportedLocales.filter((locale) => locale !== 'en').map((locale) => {
  const translation = fileBrowserResources[locale]?.translation ?? {}
  return [locale, { translation }]
}))

void i18n.use(initReactI18next).init({
  resources: { en: { translation: english }, ...localizedResources },
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
