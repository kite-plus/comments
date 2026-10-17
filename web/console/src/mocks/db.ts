// Seed data for the in-memory fake backend. Plain JSON so the dev mock can
// keep it in sessionStorage across reloads.
import type {
  Ban,
  CommentAuthor,
  CommentStatus,
  EventAction,
  Me,
  Notification,
  ReportReason,
  ReportStatus,
  Site,
  SiteSession,
  Thread,
} from '@/api/types'

export type DbSite = Site & {
  mine: boolean
  owner_name: string
}

export type DbThread = Thread & { site_id: string }

export type DbComment = {
  id: string
  site_id: string
  thread_id: string
  root_id: string | null
  parent_id: string | null
  author: CommentAuthor
  body_html: string
  status: CommentStatus
  pinned: boolean
  created_at: string
  edited_at: string | null
  mine: boolean
}

export type DbReport = {
  id: string
  site_id: string
  comment_id: string
  reason: ReportReason
  note: string
  status: ReportStatus
  created_at: string
  resolved_at: string | null
}

export type DbEvent = {
  id: string
  site_id: string
  action: EventAction
  actor: { name: string } | null
  comment_id: string | null
  detail: Record<string, unknown>
  created_at: string
}

export type DbClaim = { id: string; token: string; email: string; verified: boolean; done: boolean }

export type Db = {
  signedIn: boolean
  /** Set after DELETE /api/v1/me; the next sign-in starts a fresh profile. */
  deleted: boolean
  me: Me
  sites: DbSite[]
  threads: DbThread[]
  comments: DbComment[]
  reports: DbReport[]
  bans: Ban[]
  events: DbEvent[]
  notifications: Notification[]
  sessions: SiteSession[]
  claims: DbClaim[]
  tokens: Record<string, { token: string; expires_at: string }>
  /** Monotonic counter for new IDs. */
  seq: number
}

const HOUR = 3600 * 1000
const DAY = 24 * HOUR

export const SITE_IDS = {
  notes: '7f3c2a1e-5b8d-4c6f-9a2e-1d4b6c8e0f12',
  demo: '2b9e4d7a-1c3f-4e8b-a6d2-5f7c9e1b3d45',
  fresh: 'c4d6e8f0-2a4b-4c6d-8e0f-1a3b5c7d9e21',
  old: '9a8b7c6d-5e4f-4a3b-2c1d-0e9f8a7b6c54',
  lin: '5e6f7a8b-9c0d-4e1f-a2b3-c4d5e6f7a8b9',
  rust: '1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9',
  spam: '0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d',
  diary: '6c5b4a39-2817-4f6e-9d8c-7b6a59483726',
} as const

export function createDb(now = Date.now()): Db {
  const at = (msAgo: number) => new Date(now - msAgo).toISOString()

  const baseSite = {
    status_note: null,
    role: 'owner' as const,
    member_policy: 'publish' as const,
    sort_order: 'oldest' as const,
    close_after_days: null,
    query_keys: [],
    allow_localhost: true,
    owner_notify: 'instant' as const,
    blocked_words: [],
    verification_expires_at: null,
    approved_at: null,
    deleted_at: null,
    purge_at: null,
  }

  const origin = (url: string, primary: boolean, verifiedAgo: number | null, createdAgo: number) => ({
    origin: url,
    is_primary: primary,
    verified_at: verifiedAgo === null ? null : at(verifiedAgo),
    last_checked_at: verifiedAgo === null ? null : at(verifiedAgo),
    failing_since: null,
    created_at: at(createdAgo),
  })

  const sites: DbSite[] = [
    {
      ...baseSite,
      id: SITE_IDS.notes,
      name: 'Amigo 的笔记',
      status: 'active',
      blocked_words: ['casino', '代开发票'],
      close_after_days: 365,
      origins: [
        origin('https://amigoer.com', true, 60 * DAY, 61 * DAY),
        origin('https://www.amigoer.com', false, 60 * DAY, 61 * DAY),
      ],
      verified_at: at(60 * DAY),
      approved_at: at(59 * DAY),
      created_at: at(61 * DAY),
      updated_at: at(3 * DAY),
      mine: true,
      owner_name: 'Amigo',
    },
    {
      ...baseSite,
      id: SITE_IDS.demo,
      name: 'Kite 演示博客',
      status: 'pending_review',
      origins: [origin('https://demo.kite.plus', true, 2 * DAY, 3 * DAY)],
      verified_at: at(2 * DAY),
      created_at: at(3 * DAY),
      updated_at: at(2 * DAY),
      mine: true,
      owner_name: 'Amigo',
    },
    {
      ...baseSite,
      id: SITE_IDS.fresh,
      name: 'Field Notes',
      status: 'unverified',
      origins: [
        origin('https://blog.example.org', true, null, 2 * HOUR),
        origin('https://www.blog.example.org', false, null, 2 * HOUR),
      ],
      verification_expires_at: new Date(now + 7 * DAY - 2 * HOUR).toISOString(),
      verified_at: null,
      created_at: at(2 * HOUR),
      updated_at: at(2 * HOUR),
      mine: true,
      owner_name: 'Amigo',
    },
    {
      ...baseSite,
      id: SITE_IDS.old,
      name: '旧博客',
      status: 'deleted',
      origins: [origin('https://old.amigoer.com', true, 400 * DAY, 401 * DAY)],
      verified_at: at(400 * DAY),
      approved_at: at(399 * DAY),
      deleted_at: at(5 * DAY),
      purge_at: new Date(now + 25 * DAY).toISOString(),
      created_at: at(401 * DAY),
      updated_at: at(5 * DAY),
      mine: true,
      owner_name: 'Amigo',
    },
    {
      ...baseSite,
      id: SITE_IDS.lin,
      name: "Lin's Tech Notes",
      status: 'active',
      origins: [origin('https://lin.dev', true, 30 * DAY, 31 * DAY)],
      verified_at: at(30 * DAY),
      approved_at: at(29 * DAY),
      created_at: at(31 * DAY),
      updated_at: at(29 * DAY),
      mine: false,
      owner_name: 'Lin',
    },
    {
      ...baseSite,
      id: SITE_IDS.rust,
      name: 'Rust 周刊',
      status: 'pending_review',
      origins: [
        origin('https://rustweekly.cn', true, 1 * DAY, 1 * DAY),
        origin('https://www.rustweekly.cn', false, null, 1 * DAY),
      ],
      verified_at: at(1 * DAY),
      created_at: at(1 * DAY),
      updated_at: at(1 * DAY),
      mine: false,
      owner_name: '周刊编辑部',
    },
    {
      ...baseSite,
      id: SITE_IDS.spam,
      name: 'Best Deals Daily',
      status: 'suspended',
      status_note: 'Comment section used for link spam.',
      origins: [origin('https://deals-daily.example.net', true, 20 * DAY, 20 * DAY)],
      verified_at: at(20 * DAY),
      approved_at: at(19 * DAY),
      created_at: at(20 * DAY),
      updated_at: at(4 * DAY),
      mine: false,
      owner_name: 'deals',
    },
  ]

  const thread = (
    id: string,
    siteId: string,
    key: string,
    url: string,
    title: string,
    createdAgo: number,
    extra: Partial<Thread> = {}
  ): DbThread => ({
    id,
    site_id: siteId,
    key,
    url,
    title,
    page_published_at: at(createdAgo + DAY),
    locked: false,
    auto_closed: false,
    comment_count: 0,
    last_comment_at: null,
    created_at: at(createdAgo),
    ...extra,
  })

  const threads: DbThread[] = [
    thread('901', SITE_IDS.notes, 'p:/posts/go-comments', 'https://amigoer.com/posts/go-comments', '用 Go 写一个评论服务', 20 * DAY),
    thread('902', SITE_IDS.notes, 'k:01J9Z3K6V4R8Q2X5N7M1B0C3D4', 'https://amigoer.com/posts/hello-kite', 'Hello, Kite', 50 * DAY),
    thread('903', SITE_IDS.notes, 'p:/posts/reading-2026', 'https://amigoer.com/posts/reading-2026', '2026 年读书笔记', 40 * DAY, { locked: true }),
    thread('904', SITE_IDS.notes, 'p:/about', 'https://amigoer.com/about', '关于', 58 * DAY),
    thread('905', SITE_IDS.notes, 'p:/2021/hello', 'https://amigoer.com/2021/hello', 'Hello World (2021)', 400 * DAY, {
      auto_closed: true,
    }),
    thread('951', SITE_IDS.lin, 'p:/posts/sqlite-wal', 'https://lin.dev/posts/sqlite-wal', 'SQLite WAL 模式详解', 10 * DAY),
    thread('952', SITE_IDS.lin, 'p:/posts/zig-build', 'https://lin.dev/posts/zig-build', 'Notes on the Zig build system', 6 * DAY),
    thread('971', SITE_IDS.diary, 'p:/2026/09/keyboard', 'https://diary.example.com/2026/09/keyboard', 'My new keyboard', 15 * DAY),
  ]

  const reader = (name: string, id: string, url: string | null = null): CommentAuthor => ({
    kind: 'user',
    id,
    name,
    url,
    avatar: null,
    badge: null,
  })
  const owner: CommentAuthor = { ...reader('Amigo', 'a-owner', 'https://amigoer.com'), badge: 'owner' }
  const meOnLin: CommentAuthor = reader('Amigo', 'a-lin', 'https://amigoer.com')
  const meOnDiary: CommentAuthor = reader('Amigo', 'a-diary', 'https://amigoer.com')

  const people = [
    reader('小林', 'r-1', 'https://lin.dev'),
    reader('Mira', 'r-2'),
    reader('陈一', 'r-3', 'https://chenyi.me'),
    reader('Tomás', 'r-4'),
    reader('风吹麦浪', 'r-5'),
  ]
  const guest: CommentAuthor = {
    kind: 'guest',
    name: '路过的访客',
    url: null,
    avatar: null,
    email: 'visitor@example.com',
  }
  const external: CommentAuthor = { kind: 'external', name: 'old_reader', url: null, avatar: null }
  const spammer = reader('Cheap Pills', 'r-9', 'https://pills.example.net')

  const comments: DbComment[] = []
  let cid = 18300
  const add = (
    threadId: string,
    author: CommentAuthor,
    body: string,
    status: CommentStatus,
    ago: number,
    extra: Partial<DbComment> = {}
  ) => {
    const t = threads.find((x) => x.id === threadId)!
    const c: DbComment = {
      id: String(++cid),
      site_id: t.site_id,
      thread_id: threadId,
      root_id: null,
      parent_id: null,
      author,
      body_html: body,
      status,
      pinned: false,
      created_at: at(ago),
      edited_at: null,
      mine: false,
      ...extra,
    }
    comments.push(c)
    return c
  }

  const lines = [
    '<p>写得很清楚，谢谢分享！</p>',
    '<p>请问 <code>frame-ancestors</code> 在 Safari 上的表现和 Chrome 一样吗？</p>',
    '<p>Great write-up. The part about partitioned cookies finally made it click for me.</p>',
    '<p>我也在用 Kite，迁移评论的时候遇到过同样的问题。</p>',
    '<blockquote><p>读取评论不建任何记录</p></blockquote><p>这一点非常赞同。</p>',
    '<p>Typo in the second paragraph: <em>recieve</em> should be <em>receive</em>.</p>',
    '<p>期待下一篇！</p>',
    '<p>Could you share the Caddyfile you used? <a href="https://example.org/caddy" rel="ugc nofollow noopener noreferrer" target="_blank">This one</a> did not work for me.</p>',
  ]

  // A long published thread so the queue shows "load more".
  for (let i = 0; i < 14; i++) {
    add('901', people[i % people.length], lines[i % lines.length], 'published', (20 - i) * DAY - i * HOUR)
  }
  const root = add('901', people[1], '<p>评论服务打算支持访客评论吗？</p>', 'published', 3 * DAY)
  add('901', owner, '<p>C3 会支持，C2 内测期间先只开放登录读者。</p>', 'published', 3 * DAY - 2 * HOUR, {
    root_id: root.id,
    parent_id: root.id,
  })
  add('901', people[2], '<p>链接里有个新站点 <a href="https://new.example.com" rel="ugc nofollow noopener noreferrer" target="_blank">new.example.com</a>，欢迎来看看。</p>', 'pending', 5 * HOUR)
  add('901', people[3], '<p>I think the second diagram is out of date since the redirect fallback changed.</p>', 'pending', 3 * HOUR)
  add('902', people[4], '<p>请问 Kite 插件里填的站点 ID 在哪里找？</p>', 'pending', 50 * 60 * 1000)
  const spam1 = add('902', spammer, '<p>Buy cheap pills at <a href="https://pills.example.net" rel="ugc nofollow noopener noreferrer" target="_blank">pills.example.net</a></p>', 'spam', 6 * HOUR)
  add('902', spammer, '<p>casino bonus 100% <a href="https://casino.example.net" rel="ugc nofollow noopener noreferrer" target="_blank">click</a></p>', 'spam', 7 * HOUR)
  const pinned = add('902', owner, '<p>欢迎在这里留言，问题和建议都可以。</p>', 'published', 49 * DAY)
  pinned.pinned = true
  add('902', guest, '<p>从 Waline 导入的旧评论。</p>', 'published', 300 * DAY)
  add('902', external, '<p>Imported from Disqus.</p>', 'published', 500 * DAY)
  const rude = add('903', people[0], '<p>这篇书单也太水了吧。</p>', 'hidden', 9 * DAY)
  add('903', people[2], '<p>《置身事内》确实值得一读。</p>', 'published', 35 * DAY)
  add('904', people[1], '<p>Hi! Found you through Explore.</p>', 'published', 57 * DAY)
  const reported = add('904', people[3], '<p>你这个博客主题是抄的吧，<a href="https://example.com/x" rel="ugc nofollow noopener noreferrer" target="_blank">看这里</a>。</p>', 'published', 2 * DAY)
  add('905', people[0], '<p>Old post, still useful.</p>', 'published', 390 * DAY)

  // The reader's own comments on other sites.
  const linRoot = add('951', meOnLin, '<p>WAL 模式下 checkpoint 的时机能再讲讲吗？</p>', 'published', 9 * DAY, { mine: true })
  add('951', people[0], '<p>好问题，下一篇会写。</p>', 'published', 8 * DAY, {
    root_id: linRoot.id,
    parent_id: linRoot.id,
  })
  add('952', meOnLin, '<p>The <code>zig build --watch</code> trick saved me a lot of time, thanks.</p>', 'pending', 2 * HOUR, { mine: true })
  add('971', meOnDiary, '<p>Which switches did you go with?</p>', 'hidden', 14 * DAY, { mine: true })
  const ownReply = add('901', owner, '<p>谢谢大家的反馈，第二部分写好了。</p>', 'published', 1 * DAY, { mine: true })

  for (const t of threads) {
    const published = comments.filter((c) => c.thread_id === t.id && c.status === 'published')
    t.comment_count = published.length
    t.last_comment_at = published.reduce<string | null>(
      (latest, c) => (!latest || c.created_at > latest ? c.created_at : latest),
      null
    )
  }

  const reports: DbReport[] = [
    {
      id: '61',
      site_id: SITE_IDS.notes,
      comment_id: reported.id,
      reason: 'abuse',
      note: '人身攻击',
      status: 'open',
      created_at: at(1 * DAY),
      resolved_at: null,
    },
    {
      id: '62',
      site_id: SITE_IDS.notes,
      comment_id: reported.id,
      reason: 'spam',
      note: '',
      status: 'open',
      created_at: at(20 * HOUR),
      resolved_at: null,
    },
    {
      id: '58',
      site_id: SITE_IDS.notes,
      comment_id: rude.id,
      reason: 'other',
      note: 'Rude',
      status: 'resolved',
      created_at: at(9 * DAY),
      resolved_at: at(8 * DAY),
    },
  ]

  const bans: Ban[] = [
    {
      id: '31',
      target: 'user',
      label: 'Cheap Pills',
      reason: 'Link spam',
      created_at: at(6 * HOUR),
      expires_at: null,
      site_id: SITE_IDS.notes,
    },
    {
      id: '32',
      target: 'email',
      label: 'spam@example.net',
      reason: '',
      created_at: at(3 * DAY),
      expires_at: new Date(now + 27 * DAY).toISOString(),
      site_id: SITE_IDS.notes,
    },
    {
      id: '33',
      target: 'user',
      label: 'Cheap Pills',
      reason: 'Cross-site spam',
      created_at: at(5 * DAY),
      expires_at: null,
      site_id: null,
    },
  ]

  const events: DbEvent[] = [
    { id: '7001', site_id: SITE_IDS.notes, action: 'auto_spam', actor: null, comment_id: spam1.id, detail: { rule: 'spam_domain' }, created_at: at(6 * HOUR) },
    { id: '7002', site_id: SITE_IDS.notes, action: 'ban', actor: { name: 'Amigo' }, comment_id: null, detail: { target: 'user', ban_id: '31' }, created_at: at(6 * HOUR) },
    { id: '7003', site_id: SITE_IDS.notes, action: 'hide', actor: { name: 'Amigo' }, comment_id: rude.id, detail: {}, created_at: at(8 * DAY) },
    { id: '7004', site_id: SITE_IDS.notes, action: 'lock', actor: { name: 'Amigo' }, comment_id: null, detail: { thread_id: '903' }, created_at: at(30 * DAY) },
    { id: '7005', site_id: SITE_IDS.notes, action: 'auto_close', actor: null, comment_id: null, detail: { thread_id: '905' }, created_at: at(35 * DAY) },
    { id: '7006', site_id: SITE_IDS.notes, action: 'pin', actor: { name: 'Amigo' }, comment_id: pinned.id, detail: {}, created_at: at(49 * DAY) },
  ]

  const notifications: Notification[] = [
    {
      id: '501',
      kind: 'reply',
      created_at: at(8 * DAY),
      read_at: null,
      site: { id: SITE_IDS.lin, name: "Lin's Tech Notes" },
      thread: { id: '951', title: 'SQLite WAL 模式详解', url: 'https://lin.dev/posts/sqlite-wal', muted: false },
      comment: { id: String(Number(linRoot.id) + 1), author: { name: '小林', avatar: null }, excerpt: '好问题，下一篇会写。' },
    },
    {
      id: '500',
      kind: 'reply',
      created_at: at(3 * DAY),
      read_at: null,
      site: { id: SITE_IDS.notes, name: 'Amigo 的笔记' },
      thread: { id: '901', title: '用 Go 写一个评论服务', url: 'https://amigoer.com/posts/go-comments', muted: false },
      comment: { id: ownReply.id, author: { name: 'Mira', avatar: null }, excerpt: 'Looking forward to part two.' },
    },
    {
      id: '499',
      kind: 'reply',
      created_at: at(12 * DAY),
      read_at: at(11 * DAY),
      site: { id: SITE_IDS.diary, name: 'Hacker Diary' },
      thread: { id: '971', title: 'My new keyboard', url: 'https://diary.example.com/2026/09/keyboard', muted: true },
      comment: { id: '18190', author: { name: 'Sam', avatar: null }, excerpt: 'Gateron browns, and I regret nothing.' },
    },
  ]

  const sessions: SiteSession[] = [
    {
      id: 'b1a2c3d4-0001-4000-8000-000000000001',
      site: { id: SITE_IDS.notes, name: 'Amigo 的笔记', origin: 'https://amigoer.com' },
      client_label: 'Chrome · macOS',
      created_via: 'popup',
      created_at: at(40 * DAY),
      last_used_at: at(1 * DAY),
      expires_at: new Date(now + 89 * DAY).toISOString(),
    },
    {
      id: 'b1a2c3d4-0002-4000-8000-000000000002',
      site: { id: SITE_IDS.lin, name: "Lin's Tech Notes", origin: 'https://lin.dev' },
      client_label: 'Safari · iOS',
      created_via: 'redirect',
      created_at: at(9 * DAY),
      last_used_at: at(2 * HOUR),
      expires_at: new Date(now + 90 * DAY).toISOString(),
    },
    {
      id: 'b1a2c3d4-0003-4000-8000-000000000003',
      site: { id: SITE_IDS.diary, name: 'Hacker Diary', origin: 'https://diary.example.com' },
      client_label: 'Firefox · Windows',
      created_via: 'popup',
      created_at: at(15 * DAY),
      last_used_at: at(14 * DAY),
      expires_at: new Date(now + 76 * DAY).toISOString(),
    },
  ]

  return {
    signedIn: true,
    deleted: false,
    me: {
      id: '3e1f5a7c-9b2d-4f6e-8a1c-5d7e9f1b3c50',
      display_name: 'Amigo',
      avatar_kind: 'generated',
      avatar_url: null,
      homepage_url: 'https://amigoer.com',
      locale: 'zh-CN',
      notify_replies: true,
      on_account_delete: 'remove',
      email: 'amigo@example.com',
      is_platform_admin: true,
      created_at: at(90 * DAY),
      csrf_token: 'mock-csrf-token',
    },
    sites,
    threads,
    comments,
    reports,
    bans,
    events,
    notifications,
    sessions,
    claims: [{ id: '41', token: 'demo-token', email: 'amigo@qq.example', verified: false, done: false }],
    tokens: {},
    seq: 100000,
  }
}
