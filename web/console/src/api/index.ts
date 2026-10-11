// The console-session API (api.md sections 4, 6, 7 and 8). Every call the
// reader center and site console make goes through here.
import { download, request } from './client'
import type {
  AdminSite,
  AdminSitePatch,
  Ban,
  BanCreate,
  ClaimPreview,
  ClaimResult,
  ConsoleComment,
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
  SiteOrigin,
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

/** Default page sizes from api.md section 1. */
export const PAGE_SIZE = { comments: 20, other: 30 } as const

const enc = encodeURIComponent

function site(id: string) {
  return `/api/v1/sites/${enc(id)}`
}

export const auth = {
  logout: () => request<void>('/auth/logout', { method: 'POST' }),
}

export const me = {
  get: () => request<Me>('/api/v1/me'),
  update: (patch: MePatch) => request<Me>('/api/v1/me', { method: 'PATCH', body: patch }),
  /** 409 sole_owner while the reader is the only owner of a site. */
  deleteData: (comments: OnAccountDelete) =>
    request<void>('/api/v1/me', { method: 'DELETE', body: { comments } }),

  comments: (params: { site?: string; cursor?: string | null }) =>
    request<Page<MyComment>>('/api/v1/me/comments', {
      query: { site: params.site, cursor: params.cursor, limit: PAGE_SIZE.comments },
    }),

  notifications: (cursor?: string | null) =>
    request<NotificationPage>('/api/v1/me/notifications', {
      query: { cursor, limit: PAGE_SIZE.other },
    }),
  markRead: (target: { ids: string[] } | { all: true }) =>
    request<void>('/api/v1/me/notifications/read', { method: 'POST', body: target }),
  mute: (threadId: string) =>
    request<void>(`/api/v1/me/mutes/${enc(threadId)}`, { method: 'PUT' }),
  unmute: (threadId: string) =>
    request<void>(`/api/v1/me/mutes/${enc(threadId)}`, { method: 'DELETE' }),

  siteSessions: (cursor?: string | null) =>
    request<Page<SiteSession>>('/api/v1/me/site-sessions', {
      query: { cursor, limit: PAGE_SIZE.other },
    }),
  revokeSiteSession: (id: string) =>
    request<void>(`/api/v1/me/site-sessions/${enc(id)}`, { method: 'DELETE' }),
  revokeAllSiteSessions: () => request<void>('/api/v1/me/site-sessions', { method: 'DELETE' }),

  /** Always 202, whether or not the address has comments (sites.md 6). */
  startClaim: (email: string) =>
    request<void>('/api/v1/me/claims', { method: 'POST', body: { email } }),
  verifyClaim: (id: string, token: string) =>
    request<ClaimPreview>(`/api/v1/me/claims/${enc(id)}/verify`, {
      method: 'POST',
      body: { token },
    }),
  confirmClaim: (id: string) =>
    request<ClaimResult>(`/api/v1/me/claims/${enc(id)}/confirm`, { method: 'POST' }),

  export: () => download('/api/v1/me/export', {}, 'kite-comments-me.json'),
}

export const sites = {
  list: (cursor?: string | null) =>
    request<Page<SiteSummary>>('/api/v1/sites', { query: { cursor, limit: 100 } }),
  create: (input: SiteCreate) =>
    request<SiteCreated>('/api/v1/sites', { method: 'POST', body: input }),
  get: (id: string) => request<Site>(site(id)),
  update: (id: string, patch: SitePatch) =>
    request<Site>(site(id), { method: 'PATCH', body: patch }),
  remove: (id: string) => request<Site>(site(id), { method: 'DELETE' }),
  restore: (id: string) => request<Site>(`${site(id)}/restore`, { method: 'POST' }),

  addOrigin: (id: string, origin: string) =>
    request<SiteOrigin>(`${site(id)}/origins`, { method: 'POST', body: { origin } }),
  removeOrigin: (id: string, origin: string) =>
    request<void>(`${site(id)}/origins/${enc(origin)}`, { method: 'DELETE' }),

  newVerification: (id: string) =>
    request<VerificationToken>(`${site(id)}/verification`, { method: 'POST' }),
  checkVerification: (id: string) =>
    request<VerificationCheck>(`${site(id)}/verification/check`, { method: 'POST' }),

  comments: (id: string, filter: QueueFilter, cursor?: string | null) =>
    request<Page<ConsoleComment>>(`${site(id)}/comments`, {
      query: { status: filter.status, thread: filter.thread, cursor, limit: PAGE_SIZE.comments },
    }),
  moderate: (id: string, ids: string[], action: ModerationAction) =>
    request<void>(`${site(id)}/comments/moderation`, { method: 'POST', body: { ids, action } }),

  threads: (id: string, cursor?: string | null) =>
    request<Page<Thread>>(`${site(id)}/threads`, { query: { cursor, limit: PAGE_SIZE.other } }),
  updateThread: (id: string, threadId: string, patch: ThreadPatch) =>
    request<Thread>(`${site(id)}/threads/${enc(threadId)}`, { method: 'PATCH', body: patch }),
  moveThread: (id: string, threadId: string, toKey: string) =>
    request<ThreadMoved>(`${site(id)}/threads/${enc(threadId)}/move`, {
      method: 'POST',
      body: { to_key: toKey },
    }),

  bans: (id: string, cursor?: string | null) =>
    request<Page<Ban>>(`${site(id)}/bans`, { query: { cursor, limit: PAGE_SIZE.other } }),
  ban: (id: string, input: BanCreate) =>
    request<Ban>(`${site(id)}/bans`, { method: 'POST', body: input }),
  unban: (id: string, banId: string) =>
    request<void>(`${site(id)}/bans/${enc(banId)}`, { method: 'DELETE' }),

  reports: (id: string, status: ReportStatus, cursor?: string | null) =>
    request<Page<Report>>(`${site(id)}/reports`, {
      query: { status, cursor, limit: PAGE_SIZE.other },
    }),
  updateReport: (id: string, reportId: string, status: Exclude<ReportStatus, 'open'>) =>
    request<Report>(`${site(id)}/reports/${enc(reportId)}`, { method: 'PATCH', body: { status } }),

  events: (id: string, cursor?: string | null) =>
    request<Page<ModerationEvent>>(`${site(id)}/events`, {
      query: { cursor, limit: PAGE_SIZE.other },
    }),

  export: (id: string, format: ExportFormat) =>
    download(`${site(id)}/export`, { format }, `kite-comments-${id}.${format}.json`),
}

export const admin = {
  sites: (status: SiteStatus | undefined, cursor?: string | null) =>
    request<Page<AdminSite>>('/api/v1/admin/sites', {
      query: { status, cursor, limit: PAGE_SIZE.other },
    }),
  updateSite: (id: string, patch: AdminSitePatch) =>
    request<AdminSite>(`/api/v1/admin/sites/${enc(id)}`, { method: 'PATCH', body: patch }),
}
