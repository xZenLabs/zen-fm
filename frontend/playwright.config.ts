import { defineConfig, devices } from '@playwright/test'

const unavailable = process.env.ZENFM_E2E_UNAVAILABLE === '1'
const normalPort = Number(process.env.ZENFM_E2E_PORT ?? 18_780)
const baseURL = `http://127.0.0.1:${normalPort}`

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  workers: process.env.CI ? 1 : undefined,
  timeout: 30_000,
  expect: { timeout: 7_500 },
  reporter: process.env.CI ? [['line'], ['html', { open: 'never' }]] : 'list',
  use: {
    ...devices['Desktop Chrome'],
    baseURL,
    ignoreHTTPSErrors: true,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  webServer: unavailable ? undefined : {
    command: 'node ./e2e/serve-real-binary.mjs',
    url: `${baseURL}/healthz`,
    timeout: 120_000,
    reuseExistingServer: false,
    stdout: 'pipe',
    stderr: 'pipe',
  },
})
