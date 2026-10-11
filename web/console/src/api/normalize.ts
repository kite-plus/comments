// Maps server JSON onto the types in ./types.ts. api.md leaves many field
// names open, and the Go server is written in parallel, so each mapper
// accepts the names assumed here and the ones the server chose; the UI only
// ever sees the normalized shape. Drop the fallbacks once api.md pins them.
import type {
  AdminSite,
  Ban,
  BanTarget,
  ConsoleComment,
  Me,
  ModerationEvent,
  MyComment,
  Notification,
  NotificationPage,
  Page,
  Report,
  Site,
  SiteOrigin,
  SiteSession,
  SiteSummary,
  Thread,
  ThreadMoved,
  VerificationCheck,
} from './types'

// Server objects are untrusted shapes; fields are read defensively.
type Raw = Record<string, any> // eslint-disable-line @typescript-eslint/no-explicit-any

const str = (v: unknown, fallback = ''): string => (typeof v === 'string' ? v : fallback)
const strOrNull = (v: unknown): string | null => (typeof v === 'string' && v !== '' ? v : null)

export function page<T>(raw: Raw, map: (item: Raw) => T): Page<T> {
  const data = Array.isArray(raw?.data) ? raw.data : []
  return { data: data.map(map), next_cursor: strOrNull(raw?.next_cursor) }
}

export function me(raw: Raw): Me {
  return {
    ...(raw as Me),
    avatar_url: strOrNull(raw.avatar_url ?? raw.avatar),
    homepage_url: strOrNull(raw.homepage_url),
    email: strOrNull(raw.email),
    is_platform_admin: raw.is_platform_admin === true,
  }
}

/** Removes markup from server-rendered HTML for one-line previews. */
export function plainText(html: string, max = 200): string {
  const text = new DOMParser().parseFromString(html, 'text/html').body.textContent ?? ''
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat
}

export function myComment(raw: Raw): MyComment {
  return {
    id: str(raw.id),
    site: { id: str(raw.site?.id), name: str(raw.site?.name) },
    thread: { id: str(raw.thread?.id), title: str(raw.thread?.title), url: str(raw.thread?.url) },
    url: strOrNull(raw.url) ?? undefined,
    root_id: strOrNull(raw.root_id),
    parent_id: strOrNull(raw.parent_id),
    body_html: str(raw.body_html),
    status: raw.status,
    pinned: raw.pinned === true,
    created_at: str(raw.created_at),
    edited_at: strOrNull(raw.edited_at),
  }
}

export function notification(raw: Raw, muted: ReadonlySet<string>): Notification {
  const c: Raw = raw.comment ?? {}
  const thread: Raw = raw.thread ?? c.thread ?? {}
  const site: Raw = raw.site ?? c.site ?? {}
  const threadId = str(thread.id)
  return {
    id: str(raw.id),
    kind: 'reply',
    created_at: str(raw.created_at),
    read_at: strOrNull(raw.read_at),
    site: { id: str(site.id), name: str(site.name) },
    thread: {
      id: threadId,
      title: str(thread.title),
      url: str(thread.url),
      muted: typeof thread.muted === 'boolean' ? thread.muted : muted.has(threadId),
    },
    comment: {
      id: str(c.id),
      author: { name: str(c.author?.name), avatar: strOrNull(c.author?.avatar) },
      excerpt: typeof c.excerpt === 'string' ? c.excerpt : plainText(str(c.body_html)),
      url: strOrNull(c.url) ?? undefined,
    },
  }
}

export function notificationPage(raw: Raw, muted: ReadonlySet<string>): NotificationPage {
  const unread = raw.unread_count ?? raw.unread
  return {
    ...page(raw, (n) => notification(n, muted)),
    unread_count: typeof unread === 'number' ? unread : undefined,
  }
}

export function siteSession(raw: Raw): SiteSession {
  return {
    id: str(raw.id),
    site: { id: str(raw.site?.id), name: str(raw.site?.name), origin: strOrNull(raw.site?.origin) },
    client_label: str(raw.client_label ?? raw.client),
    created_via: raw.created_via === 'redirect' ? 'redirect' : 'popup',
    created_at: str(raw.created_at),
    last_used_at: str(raw.last_used_at),
    expires_at: str(raw.expires_at),
  }
}

function origin(raw: Raw): SiteOrigin {
  return {
    origin: str(raw.origin),
    is_primary: (raw.is_primary ?? raw.primary) === true,
    verified_at: strOrNull(raw.verified_at),
    last_checked_at: strOrNull(raw.last_checked_at),
    failing_since: strOrNull(raw.failing_since),
    created_at: str(raw.created_at),
  }
}

export function site(raw: Raw): Site {
  return {
    ...(raw as Site),
    status_note: strOrNull(raw.status_note),
    close_after_days: typeof raw.close_after_days === 'number' ? raw.close_after_days : null,
    query_keys: Array.isArray(raw.query_keys) ? raw.query_keys : [],
    blocked_words: Array.isArray(raw.blocked_words) ? raw.blocked_words : [],
    origins: Array.isArray(raw.origins) ? raw.origins.map(origin) : [],
    verified_at: strOrNull(raw.verified_at),
    approved_at: strOrNull(raw.approved_at),
    deleted_at: strOrNull(raw.deleted_at),
    purge_at: strOrNull(raw.purge_at),
    verification_expires_at: strOrNull(raw.verification_expires_at),
  }
}

export function siteSummary(raw: Raw): SiteSummary {
  const origins: SiteOrigin[] = Array.isArray(raw.origins) ? raw.origins.map(origin) : []
  return {
    id: str(raw.id),
    name: str(raw.name),
    status: raw.status,
    role: raw.role === 'moderator' ? 'moderator' : 'owner',
    primary_origin: str(raw.primary_origin, origins.find((o) => o.is_primary)?.origin ?? origins[0]?.origin ?? ''),
    pending_count: typeof raw.pending_count === 'number' ? raw.pending_count : undefined,
    open_reports: raw.open_reports ?? raw.reported_count,
    created_at: str(raw.created_at),
    deleted_at: strOrNull(raw.deleted_at),
    purge_at: strOrNull(raw.purge_at),
  }
}

export function verificationCheck(raw: Raw): VerificationCheck {
  const results: Raw[] = Array.isArray(raw.results) ? raw.results : []
  return {
    results: results.map((r) => ({
      origin: str(r.origin),
      verified: (r.verified ?? r.ok) === true,
      method: r.method === 'dns' || r.method === 'meta' || r.method === 'file' ? r.method : null,
      detail: strOrNull(r.detail),
    })),
    site: site(raw.site ?? {}),
  }
}

export function consoleComment(raw: Raw): ConsoleComment {
  const author: Raw = raw.author ?? { kind: 'external', name: '' }
  return {
    id: str(raw.id),
    root_id: strOrNull(raw.root_id),
    parent_id: strOrNull(raw.parent_id),
    reply_to: raw.reply_to ? { id: str(raw.reply_to.id), name: str(raw.reply_to.name) } : null,
    author: {
      kind: author.kind,
      id: strOrNull(author.id) ?? undefined,
      name: str(author.name),
      url: strOrNull(author.url),
      avatar: strOrNull(author.avatar),
      badge: author.badge ?? null,
      email: strOrNull(author.email ?? raw.guest_email),
    },
    body_html: str(raw.body_html),
    status: raw.status,
    pinned: raw.pinned === true,
    created_at: str(raw.created_at),
    edited_at: strOrNull(raw.edited_at),
    reply_count: typeof raw.reply_count === 'number' ? raw.reply_count : 0,
    url: strOrNull(raw.url) ?? undefined,
    thread: {
      id: str(raw.thread?.id),
      title: str(raw.thread?.title),
      url: str(raw.thread?.url),
      key: strOrNull(raw.thread?.key) ?? undefined,
    },
    report_count: typeof raw.report_count === 'number' ? raw.report_count : undefined,
  }
}

export function thread(raw: Raw): Thread {
  return {
    id: str(raw.id),
    key: str(raw.key),
    url: str(raw.url),
    title: str(raw.title),
    page_published_at: strOrNull(raw.page_published_at ?? raw.published_at),
    locked: raw.locked === true,
    auto_closed: raw.auto_closed === true,
    comment_count: typeof raw.comment_count === 'number' ? raw.comment_count : 0,
    last_comment_at: strOrNull(raw.last_comment_at),
    created_at: str(raw.created_at),
  }
}

export function threadMoved(raw: Raw): ThreadMoved {
  const moved = raw.thread ? thread(raw.thread) : thread(raw)
  return {
    thread: moved,
    moved: typeof raw.moved === 'number' ? raw.moved : null,
    merged: typeof raw.merged === 'boolean' ? raw.merged : null,
  }
}

export function ban(raw: Raw): Ban {
  const target = (raw.target ?? raw.kind) as BanTarget
  return {
    id: str(raw.id),
    target: target === 'email' || target === 'ip' ? target : 'user',
    label: str(raw.label ?? raw.name),
    reason: str(raw.reason),
    created_at: str(raw.created_at),
    expires_at: strOrNull(raw.expires_at),
    // Absent means a ban of this site; null marks a platform-wide one.
    site_id: raw.site_id === null ? null : str(raw.site_id),
  }
}

export function report(raw: Raw): Report {
  return {
    id: str(raw.id),
    reason: raw.reason,
    note: str(raw.note),
    status: raw.status,
    created_at: str(raw.created_at),
    resolved_at: strOrNull(raw.resolved_at),
    comment: consoleComment(raw.comment ?? {}),
  }
}

export function event(raw: Raw): ModerationEvent {
  const actor = raw.actor
  return {
    id: str(raw.id),
    action: str(raw.action),
    actor:
      typeof actor === 'string'
        ? actor
          ? { name: actor }
          : null
        : actor && typeof actor.name === 'string'
          ? { name: actor.name }
          : null,
    comment_id: strOrNull(raw.comment_id),
    detail: raw.detail && typeof raw.detail === 'object' ? raw.detail : {},
    created_at: str(raw.created_at),
  }
}

export function adminSite(raw: Raw): AdminSite {
  const s = site(raw)
  return {
    id: s.id,
    name: s.name,
    status: s.status,
    status_note: s.status_note,
    origins: s.origins,
    owner: raw.owner && typeof raw.owner.display_name === 'string'
      ? { id: str(raw.owner.id), display_name: raw.owner.display_name }
      : null,
    created_at: s.created_at,
    verified_at: s.verified_at,
  }
}
