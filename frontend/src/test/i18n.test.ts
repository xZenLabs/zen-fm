import i18n, { detectLocale, supportedLocales, zenUILocales } from '../i18n'

describe('locales', () => {
  it('translates File Browser and ZenFM-specific copy', () => {
    expect(i18n.t('common.cancel', { lng: 'de' })).toBe('Abbrechen')
    expect(i18n.t('files.preview', { lng: 'fr' })).toBe('Prévisualiser')
    expect(i18n.t('warning.advanced', { lng: 'de' })).toBe('Der erweiterte Root-Modus ist aktiv. Systemdateien, Gerätepfade und ZenFM-Geheimnisse sind sichtbar und können geändert oder gelöscht werden.')
    expect(i18n.t('settings.showHidden', { lng: 'en' })).toBe('Show hidden files')
  })

  it('normalizes regional browser languages to the supported catalog', () => {
    expect(detectLocale('pt-BR')).toBe('pt-BR')
    expect(detectLocale('sv-FI')).toBe('sv-SE')
    expect(detectLocale('nb-NO')).toBe('no')
    expect(detectLocale('xx-ZZ')).toBe('en')
  })

  it('supports every ZenUI locale, including the regional Chinese catalogs', () => {
    expect(zenUILocales.every((locale) => supportedLocales.includes(locale))).toBe(true)
    expect(detectLocale('zh_HK')).toBe('zh-HK')
    expect(detectLocale('zh-MO')).toBe('zh-MO')
  })

  it('translates ZenFM-specific Settings copy from the shared plugin catalogs', () => {
    expect(i18n.t('settings.saved', { lng: 'de' })).toBe('Einstellungen gespeichert')
    expect(i18n.t('settings.lifetime', { lng: 'ja' })).toBe('有効期間')
    expect(i18n.t('settings.tokenDialogTitle', { lng: 'zh-HK' })).toBe('個人 API 權杖')
  })
})
