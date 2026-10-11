import type { ErrorBody, ErrorCode, Locale } from './types'

/** Swappable so the in-memory fake (src/mocks) can stand in for the server. */
export type Transport = (request: Request) => Promise<Response>

type ClientState = {
  transport: Transport
  csrfToken: string | undefined
  locale: Locale
  onUnauthorized: (() => void) | undefined
}

const state: ClientState = {
  transport: (request) => fetch(request),
  csrfToken: undefined,
  locale: 'zh-CN',
  onUnauthorized: undefined,
}

export function setTransport(transport: Transport) {
  state.transport = transport
}

export function setCsrfToken(token: string | undefined) {
  state.csrfToken = token
}

/** Server error messages follow Accept-Language (api.md section 1). */
export function setRequestLocale(locale: Locale) {
  state.locale = locale
}

export function setUnauthorizedHandler(handler: (() => void) | undefined) {
  state.onUnauthorized = handler
}

export class ApiError extends Error {
  readonly status: number
  readonly code: ErrorCode | (string & {})
  /** Seconds, from Retry-After on 429. */
  readonly retryAfter: number | null
  /** The whole error response, for errors that carry more than the envelope. */
  readonly body: unknown

  constructor(status: number, code: string, message: string, retryAfter: number | null = null, body?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
    this.body = body
  }
}

export function isApiError(error: unknown, code?: ErrorCode): error is ApiError {
  return error instanceof ApiError && (code === undefined || error.code === code)
}

export type Query = Record<string, string | number | boolean | null | undefined>

export function buildPath(path: string, query?: Query) {
  if (!query) return path
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue
    params.set(key, String(value))
  }
  const qs = params.toString()
  return qs ? `${path}?${qs}` : path
}

type RequestOptions = {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  query?: Query
  body?: unknown
}

function toRequest(path: string, { method = 'GET', query, body }: RequestOptions) {
  const headers = new Headers({
    Accept: 'application/json',
    'Accept-Language': state.locale,
  })
  if (body !== undefined) headers.set('Content-Type', 'application/json')
  // Only unsafe methods need the token; GETs never change state.
  if (method !== 'GET' && state.csrfToken) headers.set('X-CSRF-Token', state.csrfToken)
  return new Request(new URL(buildPath(path, query), window.location.origin), {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
}

async function toError(response: Response): Promise<ApiError> {
  const retryAfter = Number(response.headers.get('Retry-After')) || null
  let code = response.status === 401 ? 'unauthorized' : 'internal'
  let message = response.statusText || `HTTP ${response.status}`
  let body: Partial<ErrorBody> | undefined
  try {
    body = (await response.json()) as Partial<ErrorBody>
    if (body.error?.code) code = body.error.code
    if (body.error?.message) message = body.error.message
  } catch {
    // Not JSON, as from a proxy; the status decides.
  }
  return new ApiError(response.status, code, message, retryAfter, body)
}

async function send(path: string, options: RequestOptions): Promise<Response> {
  const response = await state.transport(toRequest(path, options))
  if (response.ok) return response
  const error = await toError(response)
  if (response.status === 401) state.onUnauthorized?.()
  throw error
}

/** A JSON request; resolves to undefined for 202 and 204 without a body. */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const response = await send(path, options)
  if (response.status === 204) return undefined as T
  const text = await response.text()
  return (text ? JSON.parse(text) : undefined) as T
}

export type Download = { blob: Blob; filename: string }

function filenameFrom(disposition: string | null, fallback: string) {
  if (!disposition) return fallback
  const star = /filename\*=UTF-8''([^;]+)/i.exec(disposition)
  if (star) return decodeURIComponent(star[1])
  const plain = /filename="?([^";]+)"?/i.exec(disposition)
  return plain ? plain[1] : fallback
}

/** A GET that returns a file, read as a Blob so the session cookie applies. */
export async function download(path: string, query: Query, fallbackName: string): Promise<Download> {
  const response = await send(path, { query })
  const blob = await response.blob()
  return { blob, filename: filenameFrom(response.headers.get('Content-Disposition'), fallbackName) }
}

/** The page the browser opens to sign in; return must be a relative path. */
export function loginUrl(returnTo: string) {
  const safe = returnTo.startsWith('/') && !returnTo.startsWith('//') ? returnTo : '/me'
  return buildPath('/auth/login', { return: safe })
}
