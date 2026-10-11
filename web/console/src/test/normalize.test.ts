import { describe, expect, it } from 'vitest'
import * as n from '@/api/normalize'

// Payloads in the shapes the Go server on main produces today, which differ
// from api.md's open spots in field names. The UI must read both.
describe('normalizing server JSON', () => {
  it('reads /me with avatar instead of avatar_url', () => {
    const me = n.me({
      id: 'u1', display_name: 'A', avatar: '/avatars/generated/x.svg', avatar_kind: 'generated',
      homepage_url: '', locale: 'en', email: '', notify_replies: true, on_account_delete: 'remove',
      is_platform_admin: false, created_at: '2026-10-01T00:00:00Z', csrf_token: 't',
    })
    expect(me).toMatchObject({ avatar_url: '/avatars/generated/x.svg', homepage_url: null, email: null, csrf_token: 't' })
  })

  it('reads a notification that nests site and thread in the comment', () => {
    const page = n.notificationPage(
      {
        data: [
          {
            id: '7', kind: 'reply', created_at: '2026-10-01T00:00:00Z', read_at: null,
            comment: {
              id: '42', site: { id: 's1', name: 'Blog' }, thread: { id: '9', url: 'https://b.example/p', title: 'P' },
              url: 'https://b.example/p#kc-42', author: { kind: 'user', name: 'Lin' },
              body_html: '<p>Hello <strong>there</strong></p>',
            },
          },
        ],
        next_cursor: null,
        unread: 1,
      },
      new Set(['9'])
    )
    expect(page.unread_count).toBe(1)
    expect(page.data[0]).toMatchObject({
      site: { id: 's1', name: 'Blog' },
      thread: { id: '9', muted: true },
      comment: { id: '42', author: { name: 'Lin' }, excerpt: 'Hello there', url: 'https://b.example/p#kc-42' },
    })
  })

  it('reads a site list entry with primary and reported_count', () => {
    const summary = n.siteSummary({
      id: 's1', name: 'Blog', status: 'active', role: 'owner', created_at: '2026-10-01T00:00:00Z',
      origins: [
        { origin: 'https://www.b.example', primary: false },
        { origin: 'https://b.example', primary: true },
      ],
      pending_count: 2, reported_count: 1, deleted_at: null,
    })
    expect(summary).toMatchObject({ primary_origin: 'https://b.example', pending_count: 2, open_reports: 1 })
  })

  it('reads verification results with ok', () => {
    const check = n.verificationCheck({
      results: [{ origin: 'https://b.example', ok: true, method: 'meta' }, { origin: 'https://c.example', ok: false, detail: 'none' }],
      site: { id: 's1', origins: [{ origin: 'https://b.example', primary: true, verified_at: '2026-10-01T00:00:00Z' }] },
    })
    expect(check.results).toEqual([
      { origin: 'https://b.example', verified: true, method: 'meta', detail: null },
      { origin: 'https://c.example', verified: false, method: null, detail: 'none' },
    ])
    expect(check.site.origins[0].is_primary).toBe(true)
  })

  it('reads a queue comment with guest_email and a deleted placeholder', () => {
    const guest = n.consoleComment({
      id: '5', root_id: null, parent_id: null, status: 'pending', created_at: 'x',
      author: { kind: 'guest', name: 'G', avatar: '/a.svg' }, guest_email: 'g@example.com',
      body_html: '<p>x</p>', thread: { id: '9', url: 'https://b.example/p', title: 'P' },
    })
    expect(guest.author).toMatchObject({ kind: 'guest', email: 'g@example.com' })
    expect(guest.thread.key).toBeUndefined()
    const gone = n.consoleComment({ id: '6', status: 'deleted', placeholder: 'deleted', created_at: 'x', thread: {} })
    expect(gone).toMatchObject({ body_html: '', author: { kind: 'external', name: '' } })
  })

  it('reads threads, moves, bans and events in the server shapes', () => {
    expect(n.thread({ id: '1', key: 'p:/a', published_at: '2026-01-01T00:00:00Z', locked: true }).page_published_at).toBe(
      '2026-01-01T00:00:00Z'
    )
    expect(n.threadMoved({ id: '1', key: 'p:/b' })).toMatchObject({ thread: { key: 'p:/b' }, moved: null, merged: null })
    expect(n.ban({ id: '3', kind: 'email', name: 'x@example.com', reason: '' })).toMatchObject({
      target: 'email',
      label: 'x@example.com',
      site_id: '',
    })
    expect(n.event({ id: '1', actor: '', action: 'auto_spam', detail: null }).actor).toBeNull()
    expect(n.event({ id: '2', actor: 'Amigo', action: 'hide' }).actor).toEqual({ name: 'Amigo' })
  })

  it('reads site sessions with client and lists without next_cursor', () => {
    const page = n.page({ data: [{ id: 'x', site: { id: 's', name: 'S', origin: 'https://s.example' }, client: 'Safari / iOS' }] }, n.siteSession)
    expect(page.next_cursor).toBeNull()
    expect(page.data[0].client_label).toBe('Safari / iOS')
  })
})
