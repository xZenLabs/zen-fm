import createCache from '@emotion/cache'

export const cspNonce = document.querySelector<HTMLMetaElement>('meta[name="csp-nonce"]')?.content || undefined

export const emotionCache = createCache({
  key: 'zenfm',
  nonce: cspNonce,
  prepend: true,
})
