import type { ReactNode } from 'react'
import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider } from '../auth/AuthProvider'
import { ZenThemeProvider } from '../theme'
import App from '../App'

export function TestProviders({ children, initialPath = '/' }: { children: ReactNode; initialPath?: string }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return <QueryClientProvider client={client}><ZenThemeProvider><MemoryRouter initialEntries={[initialPath]}><AuthProvider>{children}</AuthProvider></MemoryRouter></ZenThemeProvider></QueryClientProvider>
}

export function renderApp(initialPath = '/') {
  return render(<TestProviders initialPath={initialPath}><App /></TestProviders>)
}
