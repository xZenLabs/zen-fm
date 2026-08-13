import React from 'react'
import ReactDOM from 'react-dom/client'
import { CacheProvider } from '@emotion/react'
import { BrowserRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import './i18n'
import { AuthProvider } from './auth/AuthProvider'
import { ZenThemeProvider } from './theme'
import App from './App'
import './styles.css'
import { emotionCache } from './emotion'
import { installModalNavigationGuard } from './modalNavigation'

installModalNavigationGuard()

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 10_000, retry: 1, refetchOnWindowFocus: false },
    mutations: { retry: false },
  },
})

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <CacheProvider value={emotionCache}><QueryClientProvider client={queryClient}>
      <ZenThemeProvider>
        <BrowserRouter>
          <AuthProvider>
            <App />
          </AuthProvider>
        </BrowserRouter>
      </ZenThemeProvider>
    </QueryClientProvider></CacheProvider>
  </React.StrictMode>,
)
