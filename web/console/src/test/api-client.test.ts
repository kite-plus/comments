import { afterEach, describe, expect, it, vi } from 'vitest'
import * as api from '@/api'
import {
  ApiError,
  loginUrl,
  setCsrfToken,
  setRequestLocale,
  setTransport,
  setUnauthorizedHandler,
} from '@/api/client'
import { parseOrigin, purgeDate } from '@/lib/policy'

function capture(response: () => Response) {
  const requests: Request[] = []
  setTransport(async (request) => {
    requests.push(request)
    return response()
  })
  return requests
}

afterEach(() => {
  setCsrfToken(undefined)
  setUnauthorizedHandler(undefined)
  setRequestLocale('zh-CN')
})

describe('api client', () => {
  it('sends the CSRF token on writes only, and the reader language always', async () => {
    const requests = capture(() => new Response(null, { status: 204 }))
    setCsrfToken('tok')
    setRequestLocale('en')
    await api.me.markRead({ all: true })
    await api.me.notifications(null).catch(() => undefined)
    const [write, read] = requests
    expect(write.method).toBe('POST')
    expect(write.headers.get('X-CSRF-Token')).toBe('tok')
    expect(write.headers.get('Content-Type')).toBe('application/json')
    expect(await write.text()).toBe('{"all":true}')
    expect(read.method).toBe('GET')
    expect(read.headers.get('X-CSRF-Token')).toBeNull()
    expect(read.headers.get('Accept-Language')).toBe('en')
    expect(new URL(read.url).search).toBe('?limit=30')
  })

  it('turns the error envelope into an ApiError', async () => {
    capture(
      () =>
        new Response(JSON.stringify({ error: { code: 'rate_limited', message: 'slow down' } }), {
          status: 429,
          headers: { 'Retry-After': '12' },
        })
    )
    const error = await api.sites.checkVerification('s1').catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 429, code: 'rate_limited', message: 'slow down', retryAfter: 12 })
    expect(api.isApiError(error, 'rate_limited')).toBe(true)
  })

  it('reports 401 to the unauthorized handler', async () => {
    capture(() => new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'no' } }), { status: 401 }))
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    await expect(api.sites.list()).rejects.toMatchObject({ code: 'unauthorized' })
    expect(handler).toHaveBeenCalledOnce()
  })

  it('encodes path segments such as origins', async () => {
    const requests = capture(() => new Response(null, { status: 204 }))
    await api.sites.removeOrigin('abc', 'https://www.example.com')
    expect(new URL(requests[0].url).pathname).toBe(
      '/api/v1/sites/abc/origins/https%3A%2F%2Fwww.example.com'
    )
  })

  it('keeps sign-in returns on this origin', () => {
    expect(loginUrl('/me/sessions?x=1')).toBe('/auth/login?return=%2Fme%2Fsessions%3Fx%3D1')
    expect(loginUrl('//evil.example')).toBe('/auth/login?return=%2Fme')
    expect(loginUrl('https://evil.example')).toBe('/auth/login?return=%2Fme')
  })
})

describe('policy helpers', () => {
  it('parses origins the way sites.md describes', () => {
    expect(parseOrigin('https://Blog.Example.com/')).toEqual({ origin: 'https://blog.example.com' })
    expect(parseOrigin('https://blog.example.com:8443')).toEqual({ origin: 'https://blog.example.com:8443' })
    expect(parseOrigin('https://b\u00fccher.example')).toEqual({ origin: 'https://xn--bcher-kva.example' })
    expect(parseOrigin('http://blog.example.com')).toEqual({ error: 'https' })
    expect(parseOrigin('https://blog.example.com/posts')).toEqual({ error: 'format' })
    expect(parseOrigin('blog.example.com')).toEqual({ error: 'format' })
  })

  it('falls back to a 30-day recovery window', () => {
    expect(purgeDate('2026-10-01T00:00:00.000Z')).toBe('2026-10-31T00:00:00.000Z')
    expect(purgeDate('2026-10-01T00:00:00.000Z', '2026-10-05T00:00:00.000Z')).toBe('2026-10-05T00:00:00.000Z')
  })
})
