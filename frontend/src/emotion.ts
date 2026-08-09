import createCache from '@emotion/cache'

const nonce = document.querySelector<HTMLMetaElement>('meta[name="csp-nonce"]')?.content || undefined

export const emotionCache = createCache({
  key: 'zenfm',
  nonce,
  prepend: true,
})
