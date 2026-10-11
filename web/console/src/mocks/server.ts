// An in-memory stand-in for the Go server's console API. It follows
// api.md closely enough that the real client, headers and error handling
// are exercised in dev and in tests.
import type {
  AdminSite,
  Ban,
  BanCreate,
  ConsoleComment,
  ErrorCode,
  ModerationAction,
  MyComment,
  Report,
  Site,
  SiteSummary,
  Thread,
} from '@/api/types'
import type { Transport } from '@/api/client'
import { SITE_IDS, type Db, type DbComment, type DbSite } from './db'

type Ctx = {
  db: Db
  req: Request
  url: URL
  params: Record<string, string>
  body: unknown
  zh: boolean
}

type Handler = (ctx: Ctx) => Response | Promise<Response>

class HttpError extends Error {
  constructor(
    readonly status: number,
    readonly code: ErrorCode,
    readonly zh: string,
    readonly en: string
  ) {
    super(en)
  }
}

const fail = (status: number, code: ErrorCode, zh: string, en: string) => {
  throw new HttpError(status, code, zh, en)
}

const json = (data: unknown, status = 200, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json', 'Cache-Control': 'private, no-store', ...headers },
  })

const empty = (status = 204) => new Response(null, { status })

function paginate<T>(items: T[], url: URL, defaultLimit: number, maxLimit: number) {
  const limit = Math.min(Number(url.searchParams.get('limit')) || defaultLimit, maxLimit)
  const cursor = url.searchParams.get('cursor')
  const offset = cursor ? Number(atob(cursor.replace(/-/g, '+').replace(/_/g, '/'))) || 0 : 0
  const data = items.slice(offset, offset + limit)
  const next = offset + limit < items.length ? btoa(String(offset + limit)).replace(/=+$/, '') : null
  return { data, next_cursor: next }
}

const newer = <T extends { created_at: string }>(a: T, b: T) => b.created_at.localeCompare(a.created_at)

function nextId(db: Db) {
  db.seq += 1
  return String(db.seq)
}

function isoNow() {
  return new Date().toISOString()
}

// --- auth helpers ----------------------------------------------------------

function requireUser(ctx: Ctx) {
  if (!ctx.db.signedIn) fail(401, 'unauthorized', '请先登录。', 'Please sign in.')
  const method = ctx.req.method
  if (method !== 'GET' && method !== 'HEAD') {
    if (ctx.req.headers.get('X-CSRF-Token') !== ctx.db.me.csrf_token)
      fail(403, 'forbidden', '请求校验失败，请刷新页面。', 'Request check failed. Reload the page.')
  }
}

function ownSite(ctx: Ctx, opts: { allowDeleted?: boolean } = {}): DbSite {
  requireUser(ctx)
  const site = ctx.db.sites.find((s) => s.id === ctx.params.site && s.mine)
  if (!site) fail(404, 'not_found', '站点不存在。', 'Site not found.')
  if (site!.status === 'deleted' && !opts.allowDeleted)
    fail(403, 'site_inactive', '站点已删除。', 'This site has been deleted.')
  return site!
}

// --- shapes ----------------------------------------------------------------

function publicSite(site: DbSite): Site {
  const { mine: _mine, owner_name: _owner, ...rest } = site
  return structuredClone(rest)
}

function summary(db: Db, site: DbSite): SiteSummary {
  const primary = site.origins.find((o) => o.is_primary) ?? site.origins[0]
  return {
    id: site.id,
    name: site.name,
    status: site.status,
    role: site.role,
    primary_origin: primary?.origin ?? '',
    pending_count: db.comments.filter((c) => c.site_id === site.id && c.status === 'pending').length,
    open_reports: db.reports.filter((r) => r.site_id === site.id && r.status === 'open').length,
    created_at: site.created_at,
    deleted_at: site.deleted_at,
    purge_at: site.purge_at,
  }
}

function consoleComment(db: Db, c: DbComment): ConsoleComment {
  const thread = db.threads.find((t) => t.id === c.thread_id)!
  const parent = c.parent_id ? db.comments.find((p) => p.id === c.parent_id) : undefined
  return {
    id: c.id,
    root_id: c.root_id,
    parent_id: c.parent_id,
    reply_to: parent && parent.id !== c.root_id ? { id: parent.id, name: parent.author.name } : null,
    author: structuredClone(c.author),
    body_html: c.body_html,
    status: c.status,
    pinned: c.pinned,
    created_at: c.created_at,
    edited_at: c.edited_at,
    reply_count: db.comments.filter((r) => r.root_id === c.id && r.status === 'published').length,
    thread: { id: thread.id, key: thread.key, title: thread.title, url: thread.url },
    report_count: db.reports.filter((r) => r.comment_id === c.id && r.status === 'open').length,
  }
}

function myComment(db: Db, c: DbComment): MyComment {
  const thread = db.threads.find((t) => t.id === c.thread_id)!
  const site = db.sites.find((s) => s.id === c.site_id)
  return {
    id: c.id,
    site: { id: c.site_id, name: site?.name ?? 'Hacker Diary' },
    thread: { id: thread.id, title: thread.title, url: thread.url },
    root_id: c.root_id,
    parent_id: c.parent_id,
    body_html: c.body_html,
    status: c.status,
    pinned: c.pinned,
    created_at: c.created_at,
    edited_at: c.edited_at,
  }
}

function threadOut(t: Db['threads'][number]): Thread {
  const { site_id: _site, ...rest } = t
  return structuredClone(rest)
}

function recount(db: Db, threadId: string) {
  const t = db.threads.find((x) => x.id === threadId)
  if (!t) return
  const published = db.comments.filter((c) => c.thread_id === t.id && c.status === 'published')
  t.comment_count = published.length
  t.last_comment_at = published.reduce<string | null>(
    (latest, c) => (!latest || c.created_at > latest ? c.created_at : latest),
    null
  )
}

function normalizeOrigin(raw: unknown, ctx: Ctx): string {
  if (typeof raw !== 'string' || !raw.trim()) fail(400, 'invalid_request', '请填写 origin。', 'Enter an origin.')
  let url: URL
  try {
    url = new URL(String(raw).trim())
  } catch {
    return fail(400, 'invalid_request', 'origin 格式不对。', 'That origin is not a valid URL.')
  }
  if (url.protocol !== 'https:')
    fail(400, 'origin_not_allowed', '只接受 https 的 origin。', 'Only https origins are accepted.')
  if (url.pathname !== '/' || url.search || url.hash)
    fail(400, 'invalid_request', 'origin 只包含协议、主机和端口。', 'An origin is only scheme, host and port.')
  const origin = url.origin.toLowerCase()
  // Only a verified origin belongs to a site (sites.md 2).
  if (ctx.db.sites.some((s) => s.origins.some((o) => o.origin === origin && o.verified_at)))
    fail(409, 'conflict', '这个 origin 已属于另一个站点。', 'That origin already belongs to another site.')
  return origin
}

const MODERATION: Record<ModerationAction, (c: DbComment) => void> = {
  approve: (c) => c.status === 'pending' && (c.status = 'published'),
  hide: (c) => c.status !== 'deleted' && (c.status = 'hidden'),
  spam: (c) => c.status !== 'deleted' && (c.status = 'spam'),
  restore: (c) => (c.status === 'hidden' || c.status === 'spam') && (c.status = 'published'),
  pin: (c) => !c.root_id && (c.pinned = true),
  unpin: (c) => (c.pinned = false),
}

// --- routes ----------------------------------------------------------------

const routes: [string, string, Handler][] = [
  ['POST', '/auth/logout', ({ db }) => {
    db.signedIn = false
    return empty()
  }],

  ['GET', '/api/v1/me', (ctx) => {
    requireUser(ctx)
    return json(ctx.db.me)
  }],
  ['PATCH', '/api/v1/me', (ctx) => {
    requireUser(ctx)
    const patch = (ctx.body ?? {}) as Record<string, unknown>
    if ('display_name' in patch) {
      const name = String(patch.display_name ?? '').trim()
      if (name.length < 1 || name.length > 40)
        fail(400, 'invalid_request', '显示名需要 1 到 40 个字符。', 'Display names are 1 to 40 characters.')
      patch.display_name = name
    }
    if ('homepage_url' in patch && patch.homepage_url) {
      const value = String(patch.homepage_url)
      if (!/^https?:\/\//.test(value) || value.length > 300)
        fail(400, 'invalid_request', '个人主页要以 http:// 或 https:// 开头。', 'Homepages start with http:// or https://.')
    }
    Object.assign(ctx.db.me, patch)
    return json(ctx.db.me)
  }],
  ['DELETE', '/api/v1/me', (ctx) => {
    requireUser(ctx)
    const choice = (ctx.body as { comments?: string } | undefined)?.comments
    if (choice !== 'remove' && choice !== 'anonymize')
      fail(400, 'invalid_request', '请选择评论的处理方式。', 'Choose what happens to your comments.')
    if (ctx.db.sites.some((s) => s.mine && s.status !== 'deleted'))
      fail(409, 'sole_owner', '你是某些站点唯一的所有者，请先删除这些站点。', 'You are the only owner of some sites. Delete them first.')
    for (const c of ctx.db.comments.filter((x) => x.mine)) {
      c.author = { kind: 'former_user', name: ctx.zh ? '已注销用户' : 'Deleted user', url: null, avatar: null }
      if (choice === 'remove') c.status = 'deleted'
      c.mine = false
    }
    ctx.db.notifications = []
    ctx.db.sessions = []
    ctx.db.signedIn = false
    ctx.db.deleted = true
    return empty()
  }],

  ['GET', '/api/v1/me/comments', (ctx) => {
    requireUser(ctx)
    const site = ctx.url.searchParams.get('site')
    const mine = ctx.db.comments
      .filter((c) => c.mine && (!site || c.site_id === site))
      .sort(newer)
      .map((c) => myComment(ctx.db, c))
    return json(paginate(mine, ctx.url, 20, 50))
  }],

  ['DELETE', '/api/v1/me/comments/:id', (ctx) => {
    requireUser(ctx)
    const c = ctx.db.comments.find((x) => x.mine && x.id === ctx.params.id && x.status !== 'deleted')
    if (!c) fail(404, 'not_found', '没有找到。', 'Not found.')
    c!.status = 'deleted'
    return empty()
  }],

  ['GET', '/api/v1/me/notifications', (ctx) => {
    requireUser(ctx)
    const list = [...ctx.db.notifications].sort(newer)
    return json({
      ...paginate(list, ctx.url, 30, 100),
      unread_count: list.filter((n) => !n.read_at).length,
    })
  }],
  ['POST', '/api/v1/me/notifications/read', (ctx) => {
    requireUser(ctx)
    const body = (ctx.body ?? {}) as { ids?: string[]; all?: boolean }
    const now = isoNow()
    for (const n of ctx.db.notifications) {
      if (!n.read_at && (body.all || body.ids?.includes(n.id))) n.read_at = now
    }
    return empty()
  }],
  ['PUT', '/api/v1/me/mutes/:thread', (ctx) => {
    requireUser(ctx)
    for (const n of ctx.db.notifications) if (n.thread.id === ctx.params.thread) n.thread.muted = true
    return empty()
  }],
  ['DELETE', '/api/v1/me/mutes/:thread', (ctx) => {
    requireUser(ctx)
    for (const n of ctx.db.notifications) if (n.thread.id === ctx.params.thread) n.thread.muted = false
    return empty()
  }],

  ['GET', '/api/v1/me/site-sessions', (ctx) => {
    requireUser(ctx)
    const list = [...ctx.db.sessions].sort((a, b) => b.last_used_at.localeCompare(a.last_used_at))
    return json(paginate(list, ctx.url, 30, 100))
  }],
  ['DELETE', '/api/v1/me/site-sessions/:id', (ctx) => {
    requireUser(ctx)
    const before = ctx.db.sessions.length
    ctx.db.sessions = ctx.db.sessions.filter((s) => s.id !== ctx.params.id)
    if (ctx.db.sessions.length === before) fail(404, 'not_found', '会话不存在。', 'Session not found.')
    return empty()
  }],
  ['DELETE', '/api/v1/me/site-sessions', (ctx) => {
    requireUser(ctx)
    ctx.db.sessions = []
    return empty()
  }],

  ['POST', '/api/v1/me/claims', (ctx) => {
    requireUser(ctx)
    const email = String((ctx.body as { email?: string })?.email ?? '').trim()
    if (!/^[^@\s]+@[^@\s]+$/.test(email))
      fail(400, 'invalid_request', '邮箱格式不对。', 'That email address is not valid.')
    const own = email.toLowerCase() === ctx.db.me.email?.toLowerCase()
    const id = nextId(ctx.db)
    ctx.db.claims.push({ id, token: 'demo-token', email, verified: own, done: false })
    return json({ id, verified: own }, 202)
  }],
  ['POST', '/api/v1/me/claims/:id/verify', (ctx) => {
    requireUser(ctx)
    const claim = ctx.db.claims.find((c) => c.id === ctx.params.id)
    const token = (ctx.body as { token?: string })?.token
    if (!claim || claim.token !== token)
      fail(404, 'not_found', '链接无效或已过期。', 'This link is invalid or has expired.')
    claim!.verified = true
    return json({
      count: 3,
      sites: [
        { id: '8d7c6b5a-4f3e-4d2c-9b1a-0f9e8d7c6b5a', name: '某某的博客', count: 2 },
        { id: '1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d', name: 'Waline Demo', count: 1 },
      ],
    })
  }],
  ['POST', '/api/v1/me/claims/:id/confirm', (ctx) => {
    requireUser(ctx)
    const claim = ctx.db.claims.find((c) => c.id === ctx.params.id && c.verified)
    if (!claim) fail(404, 'not_found', '认领请求不存在。', 'Claim not found.')
    claim!.done = true
    return json({ claimed: claim!.email === ctx.db.me.email ? 2 : 3 })
  }],

  ['GET', '/api/v1/me/export', (ctx) => {
    requireUser(ctx)
    const body = JSON.stringify(
      {
        version: 1,
        exported_at: isoNow(),
        profile: { ...ctx.db.me, csrf_token: undefined },
        comments: ctx.db.comments.filter((c) => c.mine).map((c) => myComment(ctx.db, c)),
      },
      null,
      2
    )
    return new Response(body, {
      headers: {
        'Content-Type': 'application/json',
        'Content-Disposition': 'attachment; filename="kite-comments-me.json"',
      },
    })
  }],

  ['GET', '/api/v1/sites', (ctx) => {
    requireUser(ctx)
    const list = ctx.db.sites.filter((s) => s.mine).map((s) => summary(ctx.db, s))
    return json(paginate(list, ctx.url, 30, 100))
  }],
  ['POST', '/api/v1/sites', (ctx) => {
    requireUser(ctx)
    const body = (ctx.body ?? {}) as { name?: string; origin?: string; aliases?: string[] }
    const name = String(body.name ?? '').trim()
    if (name.length < 1 || name.length > 100)
      fail(400, 'invalid_request', '站点名称需要 1 到 100 个字符。', 'Site names are 1 to 100 characters.')
    const primary = normalizeOrigin(body.origin, ctx)
    const aliases = (body.aliases ?? []).filter(Boolean).map((a) => normalizeOrigin(a, ctx))
    if (aliases.length > 5) fail(400, 'invalid_request', '别名最多 5 个。', 'At most 5 aliases.')
    const now = isoNow()
    const expires = new Date(Date.now() + 7 * 24 * 3600 * 1000).toISOString()
    const id = crypto.randomUUID()
    const site: DbSite = {
      id,
      name,
      status: 'unverified',
      status_note: null,
      role: 'owner',
      guest_policy: 'off',
      member_policy: 'publish',
      sort_order: 'oldest',
      close_after_days: null,
      query_keys: [],
      allow_localhost: true,
      owner_notify: 'instant',
      blocked_words: [],
      origins: [primary, ...new Set(aliases)].map((origin, i) => ({
        origin,
        is_primary: i === 0,
        verified_at: null,
        last_checked_at: null,
        failing_since: null,
        created_at: now,
      })),
      verification_expires_at: expires,
      verified_at: null,
      approved_at: null,
      deleted_at: null,
      purge_at: null,
      created_at: now,
      updated_at: now,
      mine: true,
      owner_name: ctx.db.me.display_name,
    }
    ctx.db.sites.push(site)
    const token = `kc_${id.slice(0, 8)}${Math.random().toString(36).slice(2, 10)}`
    ctx.db.tokens[id] = { token, expires_at: expires }
    return json({ ...publicSite(site), verification: { token, expires_at: expires } }, 201)
  }],
  ['GET', '/api/v1/sites/:site', (ctx) => json(publicSite(ownSite(ctx, { allowDeleted: true })))],
  ['PATCH', '/api/v1/sites/:site', (ctx) => {
    const site = ownSite(ctx)
    const patch = (ctx.body ?? {}) as Partial<Site> & { clear_close_after_days?: boolean }
    if (patch.clear_close_after_days) patch.close_after_days = null
    if (patch.name !== undefined && (!patch.name.trim() || patch.name.length > 100))
      fail(400, 'invalid_request', '站点名称需要 1 到 100 个字符。', 'Site names are 1 to 100 characters.')
    if (patch.guest_policy && patch.guest_policy !== 'off')
      fail(400, 'invalid_request', '内测期间不接受访客评论。', 'Guest comments are off during the beta.')
    if (patch.close_after_days !== undefined && patch.close_after_days !== null &&
      (patch.close_after_days < 1 || patch.close_after_days > 3650))
      fail(400, 'invalid_request', '自动关闭的天数要在 1 到 3650 之间。', 'Auto-close days must be between 1 and 3650.')
    if (patch.blocked_words && patch.blocked_words.length > 200)
      fail(400, 'invalid_request', '屏蔽词最多 200 个。', 'At most 200 blocked words.')
    if (patch.query_keys && patch.query_keys.length > 5)
      fail(400, 'invalid_request', '查询参数最多 5 个。', 'At most 5 query parameters.')
    const allowed = ['name', 'guest_policy', 'member_policy', 'sort_order', 'close_after_days', 'query_keys', 'allow_localhost', 'owner_notify', 'blocked_words'] as const
    for (const key of allowed) if (key in patch) (site as Record<string, unknown>)[key] = patch[key]
    site.updated_at = isoNow()
    return json(publicSite(site))
  }],
  ['DELETE', '/api/v1/sites/:site', (ctx) => {
    const site = ownSite(ctx)
    site.status = 'deleted'
    site.deleted_at = isoNow()
    site.purge_at = new Date(Date.now() + 30 * 24 * 3600 * 1000).toISOString()
    ctx.db.sessions = ctx.db.sessions.filter((s) => s.site.id !== site.id)
    return empty()
  }],
  ['POST', '/api/v1/sites/:site/restore', (ctx) => {
    const site = ownSite(ctx, { allowDeleted: true })
    if (site.status !== 'deleted') fail(409, 'conflict', '站点没有被删除。', 'This site is not deleted.')
    site.status = site.approved_at ? 'active' : site.verified_at ? 'pending_review' : 'unverified'
    site.deleted_at = null
    site.purge_at = null
    return json(publicSite(site))
  }],

  ['POST', '/api/v1/sites/:site/origins', (ctx) => {
    const site = ownSite(ctx)
    const origin = normalizeOrigin((ctx.body as { origin?: string })?.origin, ctx)
    if (site.origins.filter((o) => !o.is_primary).length >= 5)
      fail(400, 'invalid_request', '别名最多 5 个。', 'At most 5 aliases.')
    const entry = { origin, is_primary: false, verified_at: null, last_checked_at: null, failing_since: null, created_at: isoNow() }
    site.origins.push(entry)
    return json(entry, 201)
  }],
  ['DELETE', '/api/v1/sites/:site/origins/:origin', (ctx) => {
    const site = ownSite(ctx)
    const target = site.origins.find((o) => o.origin === ctx.params.origin)
    if (!target) fail(404, 'not_found', 'origin 不存在。', 'Origin not found.')
    if (target!.is_primary)
      fail(409, 'conflict', '不能删除主 origin。', 'The primary origin cannot be removed.')
    site.origins = site.origins.filter((o) => o !== target)
    return empty()
  }],

  ['POST', '/api/v1/sites/:site/verification', (ctx) => {
    const site = ownSite(ctx)
    const token = `kc_${site.id.slice(0, 8)}${Math.random().toString(36).slice(2, 10)}`
    const expires = new Date(Date.now() + 7 * 24 * 3600 * 1000).toISOString()
    ctx.db.tokens[site.id] = { token, expires_at: expires }
    site.verification_expires_at = expires
    return json({ token, expires_at: expires }, 201)
  }],
  ['POST', '/api/v1/sites/:site/verification/check', (ctx) => {
    const site = ownSite(ctx)
    const pending = site.origins.filter((o) => !o.verified_at)
    if (!site.verification_expires_at || site.verification_expires_at < isoNow())
      fail(422, 'verification_failed', '没有有效的验证令牌，请先生成一个。', 'No valid verification token. Generate one first.')
    const now = isoNow()
    // In the fake, hosts containing "fail" never verify; the rest verify by DNS.
    const results = pending.map((o) => {
      o.last_checked_at = now
      const taken = ctx.db.sites.some((s) => s !== site && s.origins.some((x) => x.origin === o.origin && x.verified_at))
      if (taken)
        return {
          origin: o.origin,
          verified: false,
          method: null,
          detail: ctx.zh ? '这个 origin 已被另一个站点验证。' : 'Another site has verified this origin.',
        }
      const ok = !new URL(o.origin).hostname.includes('fail')
      if (ok) o.verified_at = now
      return {
        origin: o.origin,
        verified: ok,
        method: ok ? ('dns' as const) : null,
        detail: ok
          ? null
          : ctx.zh
            ? `_kite-comments.${new URL(o.origin).hostname} 没有 TXT 记录；首页没有找到 meta 标签；/.well-known/kite-comments.txt 返回 404。`
            : `No TXT record at _kite-comments.${new URL(o.origin).hostname}; no meta tag on the home page; /.well-known/kite-comments.txt returned 404.`,
      }
    })
    if (site.status === 'unverified' && site.origins.some((o) => o.verified_at)) {
      site.status = 'pending_review'
      site.verified_at = now
    }
    if (results.length && !results.some((r) => r.verified)) {
      return json({
        results,
        site: publicSite(site),
        error: { code: 'verification_failed', message: ctx.zh ? '没有找到验证记录。' : 'The verification record was not found.' },
      }, 422)
    }
    return json({ results, site: publicSite(site) })
  }],

  ['GET', '/api/v1/sites/:site/comments', (ctx) => {
    const site = ownSite(ctx)
    const status = ctx.url.searchParams.get('status')
    const thread = ctx.url.searchParams.get('thread')
    const reported = new Set(ctx.db.reports.filter((r) => r.status === 'open').map((r) => r.comment_id))
    const matches = (c: DbComment) =>
      !status || (status === 'reported' ? reported.has(c.id) : c.status === status)
    const list = ctx.db.comments
      .filter((c) => c.site_id === site.id && matches(c) && (!thread || c.thread_id === thread))
      .sort(newer)
      .map((c) => consoleComment(ctx.db, c))
    return json(paginate(list, ctx.url, 20, 50))
  }],
  ['POST', '/api/v1/sites/:site/comments/moderation', (ctx) => {
    const site = ownSite(ctx)
    const { ids, action } = (ctx.body ?? {}) as { ids?: string[]; action?: ModerationAction }
    if (!Array.isArray(ids) || !ids.length || !action || !(action in MODERATION))
      fail(400, 'invalid_request', '请求参数不对。', 'Invalid request.')
    for (const id of ids!) {
      const c = ctx.db.comments.find((x) => x.id === id && x.site_id === site.id)
      if (!c) continue
      MODERATION[action!](c)
      recount(ctx.db, c.thread_id)
      ctx.db.events.push({ id: nextId(ctx.db), site_id: site.id, action: action!, actor: { name: ctx.db.me.display_name }, comment_id: c.id, detail: {}, created_at: isoNow() })
    }
    return empty()
  }],

  ['GET', '/api/v1/sites/:site/threads', (ctx) => {
    const site = ownSite(ctx)
    const list = ctx.db.threads
      .filter((t) => t.site_id === site.id)
      .sort((a, b) => (b.last_comment_at ?? b.created_at).localeCompare(a.last_comment_at ?? a.created_at))
      .map(threadOut)
    return json(paginate(list, ctx.url, 30, 100))
  }],
  ['PATCH', '/api/v1/sites/:site/threads/:thread', (ctx) => {
    const site = ownSite(ctx)
    const t = ctx.db.threads.find((x) => x.id === ctx.params.thread && x.site_id === site.id)
    if (!t) fail(404, 'not_found', '讨论页不存在。', 'Thread not found.')
    const patch = (ctx.body ?? {}) as Partial<Thread> & { published_at?: string; clear_published_at?: boolean }
    if (patch.url !== undefined && !site.origins.some((o) => o.verified_at && patch.url!.startsWith(`${o.origin}/`)))
      fail(400, 'origin_not_allowed', '地址不属于这个站点。', 'That address does not belong to this site.')
    if (patch.clear_published_at) t!.page_published_at = null
    else if (patch.published_at) t!.page_published_at = new Date(patch.published_at).toISOString()
    if (patch.locked !== undefined && patch.locked !== t!.locked) {
      ctx.db.events.push({ id: nextId(ctx.db), site_id: site.id, action: patch.locked ? 'lock' : 'unlock', actor: { name: ctx.db.me.display_name }, comment_id: null, detail: { thread_id: t!.id }, created_at: isoNow() })
    }
    for (const key of ['locked', 'title', 'url'] as const) {
      if (key in patch) (t as Record<string, unknown>)[key] = patch[key]
    }
    return json(threadOut(t!))
  }],
  ['POST', '/api/v1/sites/:site/threads/:thread/move', (ctx) => {
    const site = ownSite(ctx)
    const t = ctx.db.threads.find((x) => x.id === ctx.params.thread && x.site_id === site.id)
    if (!t) fail(404, 'not_found', '讨论页不存在。', 'Thread not found.')
    const toKey = String((ctx.body as { to_key?: string })?.to_key ?? '').trim()
    if (!/^[kph]:.+/.test(toKey))
      fail(400, 'invalid_request', '标识要以 k:、p: 或 h: 开头。', 'Keys start with k:, p: or h:.')
    if (toKey === t!.key) fail(400, 'invalid_request', '目标标识和当前相同。', 'That is the current key.')
    const fromKey = t!.key
    const target = ctx.db.threads.find((x) => x.site_id === site.id && x.key === toKey)
    const moving = ctx.db.comments.filter((c) => c.thread_id === t!.id)
    let result = t!
    if (target) {
      for (const c of moving) c.thread_id = target.id
      ctx.db.threads = ctx.db.threads.filter((x) => x !== t)
      recount(ctx.db, target.id)
      result = target
    } else {
      t!.key = toKey
    }
    ctx.db.events.push({ id: nextId(ctx.db), site_id: site.id, action: 'move_thread', actor: { name: ctx.db.me.display_name }, comment_id: null, detail: { from: fromKey, to: toKey, comments: moving.map((c) => c.id) }, created_at: isoNow() })
    return json({ thread: threadOut(result), moved: moving.length, merged: !!target })
  }],

  ['GET', '/api/v1/sites/:site/bans', (ctx) => {
    const site = ownSite(ctx)
    const list = ctx.db.bans.filter((b) => b.site_id === site.id || b.site_id === null).sort(newer)
    return json(paginate(list, ctx.url, 30, 100))
  }],
  ['POST', '/api/v1/sites/:site/bans', (ctx) => {
    const site = ownSite(ctx)
    const input = (ctx.body ?? {}) as BanCreate
    let label = ''
    if ('comment_id' in input) {
      const c = ctx.db.comments.find((x) => x.id === input.comment_id && x.site_id === site.id)
      if (!c) fail(404, 'not_found', '评论不存在。', 'Comment not found.')
      if (input.kind === 'email' && !c!.author.email)
        fail(400, 'invalid_request', '这条评论没有访客邮箱。', 'This comment has no guest email.')
      if (input.kind === 'user' && c!.author.kind !== 'user')
        fail(400, 'invalid_request', '这条评论不是登录读者发的。', 'This comment is not from a signed-in reader.')
      label = input.kind === 'ip' ? (ctx.zh ? `评论 #${c!.id} 的 IP` : `IP of comment #${c!.id}`) : input.kind === 'email' ? c!.author.email! : c!.author.name
    } else if (input.kind === 'email' && 'email' in input) {
      if (!/^[^@\s]+@[^@\s]+$/.test(input.email))
        fail(400, 'invalid_request', '邮箱格式不对。', 'That email address is not valid.')
      label = input.email.trim().toLowerCase()
    } else {
      fail(400, 'invalid_request', '请求参数不对。', 'Invalid request.')
    }
    const days = input.days ?? 0
    if (!Number.isInteger(days) || days < 0 || days > 3650) fail(400, 'invalid_request', '期限不对。', 'Invalid duration.')
    const ban: Ban = {
      id: nextId(ctx.db),
      target: input.kind,
      label,
      reason: input.reason ?? '',
      created_at: isoNow(),
      expires_at: days ? new Date(Date.now() + days * 86_400_000).toISOString() : null,
      site_id: site.id,
    }
    ctx.db.bans.push(ban)
    ctx.db.events.push({ id: nextId(ctx.db), site_id: site.id, action: 'ban', actor: { name: ctx.db.me.display_name }, comment_id: 'comment_id' in input ? input.comment_id : null, detail: { target: ban.target, ban_id: ban.id }, created_at: ban.created_at })
    return json({ id: ban.id }, 201)
  }],
  ['DELETE', '/api/v1/sites/:site/bans/:id', (ctx) => {
    const site = ownSite(ctx)
    const ban = ctx.db.bans.find((b) => b.id === ctx.params.id)
    if (!ban || ban.site_id !== site.id) fail(404, 'not_found', '封禁不存在。', 'Ban not found.')
    ctx.db.bans = ctx.db.bans.filter((b) => b !== ban)
    ctx.db.events.push({ id: nextId(ctx.db), site_id: site.id, action: 'unban', actor: { name: ctx.db.me.display_name }, comment_id: null, detail: { ban_id: ban!.id }, created_at: isoNow() })
    return empty()
  }],

  ['GET', '/api/v1/sites/:site/reports', (ctx) => {
    const site = ownSite(ctx)
    const status = ctx.url.searchParams.get('status') ?? 'open'
    const list: Report[] = ctx.db.reports
      .filter((r) => r.site_id === site.id && r.status === status)
      .sort(newer)
      .map((r) => ({
        id: r.id,
        reason: r.reason,
        note: r.note,
        status: r.status,
        created_at: r.created_at,
        resolved_at: r.resolved_at,
        comment: consoleComment(ctx.db, ctx.db.comments.find((c) => c.id === r.comment_id)!),
      }))
    return json(paginate(list, ctx.url, 30, 100))
  }],
  ['PATCH', '/api/v1/sites/:site/reports/:id', (ctx) => {
    const site = ownSite(ctx)
    const r = ctx.db.reports.find((x) => x.id === ctx.params.id && x.site_id === site.id)
    if (!r) fail(404, 'not_found', '举报不存在。', 'Report not found.')
    const { status, action } = (ctx.body ?? {}) as { status?: string; action?: string }
    if ((status !== 'resolved' && status !== 'dismissed') || (action && action !== 'hide' && action !== 'spam'))
      fail(400, 'invalid_request', '请求参数不对。', 'Invalid request.')
    r!.status = status as 'resolved' | 'dismissed'
    r!.resolved_at = isoNow()
    if (action) {
      const c = ctx.db.comments.find((x) => x.id === r!.comment_id)!
      MODERATION[action as 'hide' | 'spam'](c)
      recount(ctx.db, c.thread_id)
      ctx.db.events.push({ id: nextId(ctx.db), site_id: site.id, action: action as 'hide' | 'spam', actor: { name: ctx.db.me.display_name }, comment_id: c.id, detail: { report_id: r!.id }, created_at: isoNow() })
    }
    return empty()
  }],

  ['GET', '/api/v1/sites/:site/events', (ctx) => {
    const site = ownSite(ctx)
    const list = ctx.db.events
      .filter((e) => e.site_id === site.id)
      .sort(newer)
      .map(({ site_id: _site, ...e }) => e)
    return json(paginate(list, ctx.url, 30, 100))
  }],

  ['GET', '/api/v1/sites/:site/export', (ctx) => {
    const site = ownSite(ctx, { allowDeleted: true })
    const format = ctx.url.searchParams.get('format')
    if (format !== 'json' && format !== 'artrans')
      fail(400, 'invalid_request', '不支持的格式。', 'Unsupported format.')
    const comments = ctx.db.comments.filter((c) => c.site_id === site.id)
    const body =
      format === 'json'
        ? { version: 1, site: publicSite(site), threads: ctx.db.threads.filter((t) => t.site_id === site.id).map(threadOut), comments }
        : comments.map((c) => ({ id: c.id, content: c.body_html, nick: c.author.name, created_at: c.created_at }))
    return new Response(JSON.stringify(body, null, 2), {
      headers: {
        'Content-Type': 'application/json',
        'Content-Disposition': `attachment; filename="${site.id}.${format}.json"`,
      },
    })
  }],

  ['GET', '/api/v1/admin/sites', (ctx) => {
    requireUser(ctx)
    if (!ctx.db.me.is_platform_admin) fail(403, 'forbidden', '没有权限。', 'Not allowed.')
    const status = ctx.url.searchParams.get('status')
    const list: AdminSite[] = ctx.db.sites
      .filter((s) => !status || s.status === status)
      .sort(newer)
      .map((s) => ({
        id: s.id,
        name: s.name,
        status: s.status,
        status_note: s.status_note,
        origins: structuredClone(s.origins),
        owner: { id: s.mine ? ctx.db.me.id : `owner-${s.id.slice(0, 4)}`, display_name: s.owner_name },
        created_at: s.created_at,
        verified_at: s.verified_at,
      }))
    return json(paginate(list, ctx.url, 30, 100))
  }],
  ['POST', '/api/v1/admin/sites/:site/:action', (ctx) => {
    requireUser(ctx)
    if (!ctx.db.me.is_platform_admin) fail(403, 'forbidden', '没有权限。', 'Not allowed.')
    const site = ctx.db.sites.find((s) => s.id === ctx.params.site)
    if (!site) fail(404, 'not_found', '站点不存在。', 'Site not found.')
    const note = String((ctx.body as { note?: string } | undefined)?.note ?? '').trim()
    switch (ctx.params.action) {
      case 'approve':
        if (!site!.verified_at) fail(409, 'conflict', '站点还没有通过验证。', 'The site has no verified origin yet.')
        site!.status = 'active'
        site!.approved_at ??= isoNow()
        break
      case 'suspend':
        site!.status = 'suspended'
        site!.status_note = note || null
        ctx.db.events.push({ id: nextId(ctx.db), site_id: site!.id, action: 'platform_suspend_site', actor: { name: ctx.db.me.display_name }, comment_id: null, detail: { note }, created_at: isoNow() })
        break
      case 'unsuspend':
        site!.status = site!.approved_at ? 'active' : 'pending_review'
        site!.status_note = null
        break
      default:
        fail(404, 'not_found', '接口不存在。', 'No such endpoint.')
    }
    return json(publicSite(site!))
  }],

  ['POST', '/api/v1/sites/:site/events/:id/undo', (ctx) => {
    const site = ownSite(ctx)
    const event = ctx.db.events.find((e) => e.id === ctx.params.id && e.site_id === site.id)
    if (!event || event.action !== 'move_thread') fail(404, 'not_found', '记录不存在。', 'Event not found.')
    const detail = event!.detail as { from: string; to: string; comments: string[]; thread_id: string }
    const target = ctx.db.threads.find((t) => t.site_id === site.id && t.key === detail.to)!
    let original = ctx.db.threads.find((t) => t.site_id === site.id && t.key === detail.from)
    if (!original) {
      original = { ...target, id: nextId(ctx.db), key: detail.from }
      ctx.db.threads.push(original)
    }
    for (const c of ctx.db.comments) if (detail.comments.includes(c.id)) c.thread_id = original.id
    recount(ctx.db, original.id)
    recount(ctx.db, target.id)
    ctx.db.events = ctx.db.events.filter((e) => e !== event)
    return empty()
  }],

  ['GET', '/api/v1/me/mutes', (ctx) => {
    requireUser(ctx)
    const muted = new Set(ctx.db.notifications.filter((n) => n.thread.muted).map((n) => n.thread.id))
    return json({ data: [...muted] })
  }],

  ['GET', '/api/v1/me/claims/suggestion', (ctx) => {
    requireUser(ctx)
    if (ctx.db.claims.some((c) => c.done && c.email === ctx.db.me.email)) return json({ count: 0, sites: [] })
    return json({ count: 2, sites: [{ id: SITE_IDS.lin, name: "Lin's Tech Notes", count: 2 }] })
  }],

  ['GET', '/api/v1/embed-info', () =>
    json({
      version: '1.0.0',
      script_url: `${location.origin}/embed.js`,
      pinned_url: `${location.origin}/embed/v1.0.0.js`,
      integrity: 'sha384-mockmockmockmockmockmockmockmockmockmockmockmockmockmockmockmock',
    })],
]

const compiled = routes.map(([method, pattern, handler]) => {
  const keys: string[] = []
  const source = pattern.replace(/:(\w+)/g, (_, key: string) => {
    keys.push(key)
    return '([^/]+)'
  })
  return { method, regex: new RegExp(`^${source}$`), keys, handler }
})

export type MockOptions = {
  /** Milliseconds added to every response, to make loading states visible. */
  latency?: number
  /** Called after any request that may have changed the data. */
  onWrite?: (db: Db) => void
}

export function createMockTransport(db: Db, options: MockOptions = {}): Transport {
  return async (req) => {
    const url = new URL(req.url)
    const zh = (req.headers.get('Accept-Language') ?? 'zh-CN').startsWith('zh')
    if (options.latency) await new Promise((r) => setTimeout(r, options.latency))
    let matched = false
    for (const route of compiled) {
      const m = route.regex.exec(url.pathname)
      if (!m) continue
      matched = true
      if (route.method !== req.method) continue
      const params = Object.fromEntries(route.keys.map((k, i) => [k, decodeURIComponent(m[i + 1])]))
      const text = req.method === 'GET' ? '' : await req.text()
      try {
        const body = text ? JSON.parse(text) : undefined
        const response = await route.handler({ db, req, url, params, body, zh })
        if (req.method !== 'GET') options.onWrite?.(db)
        return response
      } catch (error) {
        if (error instanceof HttpError) {
          return json({ error: { code: error.code, message: zh ? error.zh : error.en } }, error.status)
        }
        console.error(error)
        return json({ error: { code: 'internal', message: String(error) } }, 500)
      }
    }
    return matched
      ? json({ error: { code: 'invalid_request', message: 'Method not allowed' } }, 405)
      : json({ error: { code: 'not_found', message: zh ? '接口不存在。' : 'No such endpoint.' } }, 404)
  }
}
