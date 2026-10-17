// Types for the console-session API, written by hand from
// docs/design/api.md (sections 1, 6, 7, 8 and 10) and data-model.md.
// Fields the docs do not pin down are marked "assumed" and listed in
// web/console/README.md under "API assumptions".

/** RFC 3339 timestamp in UTC. */
export type Timestamp = string
/** bigint IDs (comments, threads, reports, bans, events) travel as strings. */
export type BigId = string
/** Sites and sessions use uuid public IDs. */
export type Uuid = string

export type Locale = 'zh-CN' | 'en'

/** api.md section 1: every list. */
export type Page<T> = {
  data: T[]
  next_cursor: string | null
}

/** api.md section 10. */
export const ERROR_CODES = [
  'invalid_request',
  'comment_too_long',
  'too_many_links',
  'origin_not_allowed',
  'unauthorized',
  'forbidden',
  'site_inactive',
  'thread_locked',
  'edit_window_closed',
  'guest_comments_gone',
  'not_found',
  'conflict',
  'sole_owner',
  'login_expired',
  'verification_failed',
  'import_invalid',
  'rate_limited',
  'internal',
] as const

export type ErrorCode = (typeof ERROR_CODES)[number]

export type ErrorBody = {
  error: { code: ErrorCode | (string & {}); message: string }
}

// ---------------------------------------------------------------------------
// Reader center (api.md section 6)

export type AvatarKind = 'generated' | 'idp'
export type OnAccountDelete = 'remove' | 'anonymize'

/** GET /api/v1/me. */
export type Me = {
  /** users.public_id. */
  id: Uuid
  display_name: string
  avatar_kind: AvatarKind
  /** Assumed: a preview of the account picture, served by this service. */
  avatar_url: string | null
  homepage_url: string | null
  locale: Locale
  notify_replies: boolean
  on_account_delete: OnAccountDelete
  /** Assumed: the verified email from the identity service, shown only to its owner. */
  email: string | null
  is_platform_admin: boolean
  created_at: Timestamp
  /** Assumed to come with /me, as in Explore; api.md says CSRF works as in Explore. */
  csrf_token?: string
}

export type MePatch = Partial<
  Pick<
    Me,
    | 'display_name'
    | 'avatar_kind'
    | 'homepage_url'
    | 'locale'
    | 'notify_replies'
    | 'on_account_delete'
  >
>

export type CommentStatus = 'pending' | 'published' | 'spam' | 'hidden' | 'deleted'

export type SiteRef = { id: Uuid; name: string }

export type ThreadRef = {
  id: BigId
  title: string
  url: string
}

/** GET /api/v1/me/comments item. */
export type MyComment = {
  id: BigId
  site: SiteRef
  thread: ThreadRef
  /** The thread URL with #kc-<id>, when the server sends it. */
  url?: string
  root_id: BigId | null
  parent_id: BigId | null
  body_html: string
  status: CommentStatus
  pinned: boolean
  created_at: Timestamp
  edited_at: Timestamp | null
}

/** GET /api/v1/me/notifications item (data-model.md 3.6: kind is only "reply"). */
export type Notification = {
  id: BigId
  kind: 'reply'
  created_at: Timestamp
  read_at: Timestamp | null
  site: SiteRef
  /** Assumed: muted reflects PUT/DELETE /api/v1/me/mutes/{thread}. */
  thread: ThreadRef & { muted: boolean }
  /** The reply that triggered the notification. */
  comment: {
    id: BigId
    author: { name: string; avatar: string | null }
    /** Plain text, at most 200 characters (moderation.md 6.1). */
    excerpt: string
    url?: string
  }
}

/** Assumed: the list carries the unread total for the badge. */
export type NotificationPage = Page<Notification> & { unread_count?: number }

/** Assumed: POST /api/v1/me/claims answers 202 with the claim. */
export type ClaimStarted = {
  id?: BigId
  /** True when the address is the account's own verified one (sites.md 6, step 6). */
  verified?: boolean
}

/** GET /api/v1/me/site-sessions item. */
export type SiteSession = {
  id: Uuid
  site: SiteRef & { origin: string | null }
  /** A coarse browser and OS label, never the raw User-Agent. */
  client_label: string
  created_via: 'popup' | 'redirect'
  created_at: Timestamp
  last_used_at: Timestamp
  expires_at: Timestamp
}

/** POST /api/v1/me/claims/{id}/verify. */
export type ClaimPreview = {
  count: number
  sites: (SiteRef & { count: number })[]
}

export type ClaimResult = { claimed: number }

// ---------------------------------------------------------------------------
// Site console (api.md section 7, sites.md, moderation.md)

export type SiteStatus = 'unverified' | 'pending_review' | 'active' | 'suspended' | 'deleted'
export type SiteRole = 'owner' | 'moderator'
export type MemberPolicy = 'publish' | 'review'
export type SortOrder = 'oldest' | 'newest'
export type OwnerNotify = 'instant' | 'daily' | 'off'

export type SiteOrigin = {
  origin: string
  is_primary: boolean
  verified_at: Timestamp | null
  last_checked_at: Timestamp | null
  failing_since: Timestamp | null
  created_at: Timestamp
}

/** GET /api/v1/sites item. */
export type SiteSummary = {
  id: Uuid
  name: string
  status: SiteStatus
  role: SiteRole
  primary_origin: string
  /** Assumed: pending comments plus open reports, for the list and sidebar. */
  pending_count?: number
  open_reports?: number
  created_at: Timestamp
  deleted_at: Timestamp | null
  /** Assumed: end of the recovery window; falls back to deleted_at + 30 days. */
  purge_at?: Timestamp | null
}

/** GET /api/v1/sites/{site}. */
export type Site = {
  id: Uuid
  name: string
  status: SiteStatus
  status_note: string | null
  role: SiteRole
  member_policy: MemberPolicy
  sort_order: SortOrder
  close_after_days: number | null
  query_keys: string[]
  allow_localhost: boolean
  owner_notify: OwnerNotify
  blocked_words: string[]
  origins: SiteOrigin[]
  /** Assumed: when the newest unexpired verification token expires. */
  verification_expires_at?: Timestamp | null
  verified_at: Timestamp | null
  approved_at: Timestamp | null
  deleted_at: Timestamp | null
  purge_at?: Timestamp | null
  created_at: Timestamp
  updated_at: Timestamp
}

/** Assumed request shape for POST /api/v1/sites. */
export type SiteCreate = {
  name: string
  origin: string
  aliases: string[]
}

/** Assumed: the create response may carry the first token (sites.md 2 step 3). */
export type SiteCreated = Site & { verification?: VerificationToken }

export type SitePatch = Partial<
  Pick<
    Site,
    | 'name'
    | 'member_policy'
    | 'sort_order'
    | 'close_after_days'
    | 'query_keys'
    | 'allow_localhost'
    | 'owner_notify'
    | 'blocked_words'
  >
>

/** POST /api/v1/sites/{site}/verification: plaintext appears only here. */
export type VerificationToken = {
  token: string
  expires_at: Timestamp
}

export type VerificationMethod = 'dns' | 'meta' | 'file'

/** Assumed shape of POST /api/v1/sites/{site}/verification/check. */
export type VerificationCheck = {
  results: {
    origin: string
    verified: boolean
    method: VerificationMethod | null
    /** What the server found, as in verification_failed's message. */
    detail: string | null
  }[]
  site: Site
}

export type AuthorKind = 'user' | 'guest' | 'external' | 'former_user'

/** api.md section 1 comment object, as moderators see it. */
export type CommentAuthor = {
  kind: AuthorKind
  /** Per-site author ID; absent for guest and external. */
  id?: string
  name: string
  url: string | null
  avatar: string | null
  badge?: 'owner' | 'moderator' | null
  /** Console only: the email a guest left on this site. */
  email?: string | null
}

export type ConsoleComment = {
  id: BigId
  root_id: BigId | null
  parent_id: BigId | null
  reply_to: { id: BigId; name: string } | null
  author: CommentAuthor
  body_html: string
  status: CommentStatus
  pinned: boolean
  created_at: Timestamp
  edited_at: Timestamp | null
  reply_count: number
  url?: string
  /** Assumed: the queue needs to know where each comment lives. */
  thread: ThreadRef & { key?: string }
  /** Assumed: open reports on this comment. */
  report_count?: number
}

/** api.md 5 and moderation.md 4.1; there is no manager delete. */
export type ModerationAction = 'approve' | 'hide' | 'spam' | 'restore' | 'pin' | 'unpin'

/** "reported" lists comments with open reports (moderation.md 4.1). */
export type QueueStatus = CommentStatus | 'reported'

export type QueueFilter = {
  status?: QueueStatus
  thread?: BigId
}

export type Thread = {
  id: BigId
  key: string
  url: string
  title: string
  page_published_at: Timestamp | null
  locked: boolean
  /** Assumed: true once close_after_days has passed (sites.md 4). */
  auto_closed?: boolean
  comment_count: number
  last_comment_at: Timestamp | null
  created_at: Timestamp
}

/** Assumed field names; clearing a time needs its own flag in Go. */
export type ThreadPatch = Partial<{
  locked: boolean
  title: string
  url: string
  published_at: Timestamp
  clear_published_at: true
}>

export type ThreadMoved = {
  thread: Thread
  /** null when the server does not say. */
  moved: number | null
  merged: boolean | null
}

export type ReportReason = 'spam' | 'abuse' | 'illegal' | 'privacy' | 'other'
export type ReportStatus = 'open' | 'resolved' | 'dismissed'

export type Report = {
  id: BigId
  reason: ReportReason
  note: string
  status: ReportStatus
  created_at: Timestamp
  resolved_at: Timestamp | null
  comment: ConsoleComment
}

export type BanTarget = 'user' | 'email' | 'ip'

export type Ban = {
  id: BigId
  target: BanTarget
  /** Display name, the guest email, or "IP of comment #id". */
  label: string
  reason: string
  created_at: Timestamp
  expires_at: Timestamp | null
  /** null for platform-wide bans, which site moderators can see but not lift. */
  site_id: Uuid | null
}

/**
 * Assumed request shape for POST /api/v1/sites/{site}/bans: the author,
 * guest email or IP of a comment, or an email typed in. days 0 is forever.
 */
export type BanCreate =
  | { kind: BanTarget; comment_id: BigId; reason?: string; days?: number }
  | { kind: 'email'; email: string; reason?: string; days?: number }

/** moderation.md 4.4. */
export type EventAction =
  | 'approve'
  | 'hide'
  | 'spam'
  | 'restore'
  | 'pin'
  | 'unpin'
  | 'lock'
  | 'unlock'
  | 'move_thread'
  | 'ban'
  | 'unban'
  | 'auto_spam'
  | 'auto_pending'
  | 'auto_close'
  | 'report_threshold'
  | 'platform_hide'
  | 'platform_suspend_site'
  | 'platform_ban'
  | 'author_edit'
  | 'author_delete'
  | 'claim'

export type ModerationEvent = {
  id: BigId
  action: EventAction | (string & {})
  /** null when the system acted. */
  actor: { name: string } | null
  comment_id: BigId | null
  detail: Record<string, unknown>
  created_at: Timestamp
}

export type ExportFormat = 'json' | 'artrans'

// ---------------------------------------------------------------------------
// Platform admin (api.md section 8 names the area, not the routes)

/** Assumed: GET /api/v1/admin/sites item. */
export type AdminSite = {
  id: Uuid
  name: string
  status: SiteStatus
  status_note: string | null
  origins: SiteOrigin[]
  owner: { id: Uuid; display_name: string } | null
  created_at: Timestamp
  verified_at: Timestamp | null
}

export type AdminSiteAction = 'approve' | 'suspend' | 'unsuspend'

/** Assumed: GET /api/v1/embed-info, for the pinned embed.js with SRI (widget.md 10). */
export type EmbedInfo = {
  version: string
  script_url: string
  pinned_url: string
  integrity: string
}
