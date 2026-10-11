import { render } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter } from 'react-router'
import { setCsrfToken, setTransport, setUnauthorizedHandler, type Transport } from '@/api/client'
import { App, createQueryClient } from '@/app'
import type { Lang } from '@/i18n'
import { createDb, type Db } from '@/mocks/db'
import { createMockTransport } from '@/mocks/server'
import { routes } from '@/routes'

export type Call = { method: string; path: string; body: unknown; headers: Headers }

/**
 * Renders the whole app at a path against a fresh in-memory backend, and
 * records every request so tests can assert on what was sent.
 */
export function renderApp(
  path: string,
  options: { lang?: Lang; db?: Db; setup?: (db: Db) => void } = {}
) {
  const db = options.db ?? createDb(Date.parse('2026-10-10T12:00:00Z'))
  options.setup?.(db)
  const calls: Call[] = []
  const mock = createMockTransport(db)
  const transport: Transport = async (request) => {
    const url = new URL(request.url)
    const text = request.method === 'GET' ? '' : await request.clone().text()
    calls.push({
      method: request.method,
      path: url.pathname + url.search,
      body: text ? JSON.parse(text) : undefined,
      headers: request.headers,
    })
    return mock(request)
  }
  setTransport(transport)
  setCsrfToken(undefined)
  setUnauthorizedHandler(undefined)
  // A saved choice wins over the account language, as it does in a browser.
  localStorage.setItem('kc-lang', options.lang ?? 'en')
  const router = createMemoryRouter(routes, { initialEntries: [path] })
  const queryClient = createQueryClient()
  queryClient.setDefaultOptions({ queries: { retry: false, staleTime: 0 } })
  const user = userEvent.setup()
  const view = render(<App router={router} queryClient={queryClient} lang={options.lang ?? 'en'} />)
  const writes = () => calls.filter((c) => c.method !== 'GET')
  return { ...view, db, calls, writes, router, user, queryClient }
}
