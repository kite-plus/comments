// The console-session API (api.md sections 4, 6, 7 and 8). Every call the
// reader center and site console make goes through here, and every
// response passes through ./normalize before the UI sees it.
import { download, isApiError, request } from './client'
import * as n from './normalize'
import type {
  AdminSite,
  AdminSiteAction,
  Ban,
  BanCreate,
  ClaimPreview,
  ClaimResult,
  ClaimStarted,
  ConsoleComment,
  EmbedInfo,
  ExportFormat,
  Me,
  MePatch,
  ModerationAction,
  ModerationEvent,
  MyComment,
  NotificationPage,
  OnAccountDelete,
  Page,
  QueueFilter,
  Report,
  ReportStatus,
  Site,
  SiteCreate,
  SiteCreated,
  SitePatch,
  SiteSession,
  SiteStatus,
  SiteSummary,
  Thread,
  ThreadMoved,
  ThreadPatch,
  VerificationCheck,
  VerificationToken,
} from './types'

export * from './client'
export type * from './types'
export { ERROR_CODES } from './types'
export { plainText } from './normalize'

/** Default page sizes from api.md section 1. */
export const PAGE_SIZE = { comments: 20, other: 30 } as const

const enc = encodeURIComponent

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Raw = any

function sitePath(id: string) {
  return `/api/v1/sites/${enc(id)}`
}

export const auth = {
  logout: () => request<void>('/auth/logout', { method: 'POST' }),
}

/** Thread IDs the reader muted; the list is not in api.md, so it may be absent. */
async function mutedThreads(): Promise<Set<string>> {
  try {
    const raw = await request<Raw>('/api/v1/me/mutes')
    return new Set(Array.isArray(raw?.data) ? raw.data.map(String) : [])
  } catch (error) {
    if (isApiError(error) && (error.status === 404 || error.status === 405)) return new Set()
    throw error
  }
}

export const me = {
  get: async (): Promise<Me> => n.me(await request<Raw>('/api/v1/me')),
  update: async (patch: MePatch): Promise<Me> =>
    n.me(await request<Raw>('/api/v1/me', { method: 'PATCH', body: patch })),
  /** 409 sole_owner while the reader is the only owner of a site. */
  deleteData: (comments: OnAccountDelete) =>
    request<void>('/api/v1/me', { method: 'DELETE', body: { comments } }),

  comments: async (params: { site?: string; cursor?: string | null }): Promise<Page<MyComment>> =>
    n.page(
      await request<Raw>('/api/v1/me/comments', {
        query: { site: params.site, cursor: params.cursor, limit: PAGE_SIZE.comments },
      }),
      n.myComment
    ),
  /** Works on any site that still exists, whatever its status. */
  deleteComment: (id: string) => request<void>(`/api/v1/me/comments/${enc(id)}`, { method: 'DELETE' }),

  /** withMutes: false skips the mute list, for the sidebar's unread count. */
  notifications: async (cursor?: string | null, { withMutes = true } = {}): Promise<NotificationPage> => {
    const [raw, muted] = await Promise.all([
      request<Raw>('/api/v1/me/notifications', { query: { cursor, limit: PAGE_SIZE.other } }),
      withMutes ? mutedThreads() : new Set<string>(),
    ])
    return n.notificationPage(raw, muted)
  },
  markRead: (target: { ids: string[] } | { all: true }) =>
    request<void>('/api/v1/me/notifications/read', { method: 'POST', body: target }),
  mute: (threadId: string) =>
    request<void>(`/api/v1/me/mutes/${enc(threadId)}`, { method: 'PUT' }),
  unmute: (threadId: string) =>
    request<void>(`/api/v1/me/mutes/${enc(threadId)}`, { method: 'DELETE' }),

  siteSessions: async (cursor?: string | null): Promise<Page<SiteSession>> =>
    n.page(
      await request<Raw>('/api/v1/me/site-sessions', { query: { cursor, limit: PAGE_SIZE.other } }),
      n.siteSession
    ),
  revokeSiteSession: (id: string) =>
    request<void>(`/api/v1/me/site-sessions/${enc(id)}`, { method: 'DELETE' }),
  revokeAllSiteSessions: () => request<void>('/api/v1/me/site-sessions', { method: 'DELETE' }),

  /** Always 202, whether or not the address has comments (sites.md 6). */
  startClaim: async (email: string): Promise<ClaimStarted> =>
    (await request<ClaimStarted | undefined>('/api/v1/me/claims', { method: 'POST', body: { email } })) ?? {},
  verifyClaim: (id: string, token: string) =>
    request<ClaimPreview>(`/api/v1/me/claims/${enc(id)}/verify`, {
      method: 'POST',
      body: { token },
    }),
  confirmClaim: (id: string) =>
    request<ClaimResult>(`/api/v1/me/claims/${enc(id)}/confirm`, { method: 'POST' }),
  /**
   * Guest comments left with the account's own verified address
   * (sites.md 6, step 6). Not in api.md; null when the server lacks it.
   */
  claimSuggestion: async (): Promise<ClaimPreview | null> => {
    try {
      return await request<ClaimPreview>('/api/v1/me/claims/suggestion')
    } catch (error) {
      if (isApiError(error) && (error.status === 404 || error.status === 405)) return null
      throw error
    }
  },

  export: () => download('/api/v1/me/export', {}, 'kite-comments-me.json'),
}

export const sites = {
  list: async (cursor?: string | null): Promise<Page<SiteSummary>> =>
    n.page(await request<Raw>('/api/v1/sites', { query: { cursor, limit: 100 } }), n.siteSummary),
  create: async (input: SiteCreate): Promise<SiteCreated> => {
    const raw = await request<Raw>('/api/v1/sites', { method: 'POST', body: input })
    return { ...n.site(raw), verification: raw?.verification }
  },
  get: async (id: string): Promise<Site> => n.site(await request<Raw>(sitePath(id))),
  update: async (id: string, patch: SitePatch): Promise<Site> => {
    // Go cannot tell an explicit null from a missing field, so turning
    // auto-close off also sends the server's clear flag.
    const body =
      'close_after_days' in patch && patch.close_after_days === null
        ? { ...patch, clear_close_after_days: true }
        : patch
    return n.site(await request<Raw>(sitePath(id), { method: 'PATCH', body }))
  },
  remove: (id: string) => request<void>(sitePath(id), { method: 'DELETE' }),
  restore: (id: string) => request<void>(`${sitePath(id)}/restore`, { method: 'POST' }),

  addOrigin: (id: string, origin: string) =>
    request<void>(`${sitePath(id)}/origins`, { method: 'POST', body: { origin } }),
  removeOrigin: (id: string, origin: string) =>
    request<void>(`${sitePath(id)}/origins/${enc(origin)}`, { method: 'DELETE' }),

  newVerification: (id: string) =>
    request<VerificationToken>(`${sitePath(id)}/verification`, { method: 'POST' }),
  /** Resolves with the results even on 422, which means none passed. */
  checkVerification: async (id: string): Promise<VerificationCheck & { failed: boolean }> => {
    try {
      const raw = await request<Raw>(`${sitePath(id)}/verification/check`, { method: 'POST' })
      return { ...n.verificationCheck(raw), failed: false }
    } catch (error) {
      const body = isApiError(error, 'verification_failed') ? (error.body as Raw) : null
      if (body && Array.isArray(body.results) && body.site) {
        return { ...n.verificationCheck(body), failed: true }
      }
      throw error
    }
  },

  comments: async (id: string, filter: QueueFilter, cursor?: string | null): Promise<Page<ConsoleComment>> =>
    n.page(
      await request<Raw>(`${sitePath(id)}/comments`, {
        query: { status: filter.status, thread: filter.thread, cursor, limit: PAGE_SIZE.comments },
      }),
      n.consoleComment
    ),
  moderate: (id: string, ids: string[], action: ModerationAction) =>
    request<void>(`${sitePath(id)}/comments/moderation`, { method: 'POST', body: { ids, action } }),

  threads: async (id: string, cursor?: string | null): Promise<Page<Thread>> =>
    n.page(
      await request<Raw>(`${sitePath(id)}/threads`, { query: { cursor, limit: PAGE_SIZE.other } }),
      n.thread
    ),
  updateThread: async (id: string, threadId: string, patch: ThreadPatch): Promise<Thread> =>
    n.thread(
      await request<Raw>(`${sitePath(id)}/threads/${enc(threadId)}`, { method: 'PATCH', body: patch })
    ),
  moveThread: async (id: string, threadId: string, toKey: string): Promise<ThreadMoved> =>
    n.threadMoved(
      await request<Raw>(`${sitePath(id)}/threads/${enc(threadId)}/move`, {
        method: 'POST',
        body: { to_key: toKey },
      })
    ),

  bans: async (id: string, cursor?: string | null): Promise<Page<Ban>> =>
    n.page(await request<Raw>(`${sitePath(id)}/bans`, { query: { cursor, limit: PAGE_SIZE.other } }), n.ban),
  ban: (id: string, input: BanCreate) =>
    request<void>(`${sitePath(id)}/bans`, { method: 'POST', body: input }),
  unban: (id: string, banId: string) =>
    request<void>(`${sitePath(id)}/bans/${enc(banId)}`, { method: 'DELETE' }),

  reports: async (id: string, status: ReportStatus, cursor?: string | null): Promise<Page<Report>> =>
    n.page(
      await request<Raw>(`${sitePath(id)}/reports`, { query: { status, cursor, limit: PAGE_SIZE.other } }),
      n.report
    ),
  /** Resolve or dismiss; action also hides the comment or marks it spam. */
  updateReport: (
    id: string,
    reportId: string,
    status: Exclude<ReportStatus, 'open'>,
    action?: 'hide' | 'spam'
  ) =>
    request<void>(`${sitePath(id)}/reports/${enc(reportId)}`, {
      method: 'PATCH',
      body: action ? { status, action } : { status },
    }),

  events: async (id: string, cursor?: string | null): Promise<Page<ModerationEvent>> =>
    n.page(await request<Raw>(`${sitePath(id)}/events`, { query: { cursor, limit: PAGE_SIZE.other } }), n.event),
  /** Undoes a thread move (sites.md 4); the route is assumed. */
  undoEvent: (id: string, eventId: string) =>
    request<void>(`${sitePath(id)}/events/${enc(eventId)}/undo`, { method: 'POST' }),

  export: (id: string, format: ExportFormat) =>
    download(`${sitePath(id)}/export`, { format }, `kite-comments-${id}.${format}.json`),
}

export const admin = {
  sites: async (status: SiteStatus | undefined, cursor?: string | null): Promise<Page<AdminSite>> =>
    n.page(
      await request<Raw>('/api/v1/admin/sites', { query: { status, cursor, limit: PAGE_SIZE.other } }),
      n.adminSite
    ),
  siteAction: (id: string, action: AdminSiteAction, note?: string) =>
    request<void>(`/api/v1/admin/sites/${enc(id)}/${action}`, {
      method: 'POST',
      body: note ? { note } : undefined,
    }),
}

/** The pinned embed.js and its SRI hash, or null when the server has none. */
export async function embedInfo(): Promise<EmbedInfo | null> {
  try {
    return await request<EmbedInfo>('/api/v1/embed-info')
  } catch (error) {
    if (isApiError(error) && error.status !== 401) return null
    throw error
  }
}
