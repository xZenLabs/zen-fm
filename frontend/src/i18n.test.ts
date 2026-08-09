import i18n, { detectLocale } from './i18n'

describe('locales', () => {
  it('reuses translated File Browser phrases and falls back for ZenFM-specific copy', () => {
    expect(i18n.t('common.cancel', { lng: 'de' })).toBe('Abbrechen')
    expect(i18n.t('files.preview', { lng: 'fr' })).toBe('Prévisualiser')
    expect(i18n.t('warning.advanced', { lng: 'de' })).toBe('Advanced root mode is active. System files, device paths, and ZenFM secrets are visible and may be changed or deleted.')
    expect(i18n.t('settings.showHidden', { lng: 'en' })).toBe('Show hidden files')
  })

  it('normalizes regional browser languages to the supported catalog', () => {
    expect(detectLocale('pt-BR')).toBe('pt-BR')
    expect(detectLocale('sv-FI')).toBe('sv-SE')
    expect(detectLocale('nb-NO')).toBe('no')
    expect(detectLocale('xx-ZZ')).toBe('en')
  })
})
