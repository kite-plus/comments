# 数据模型

> 状态：已落地为 `migrations/` 下的迁移（`00001_init.sql`，2026-10 安全审查后的修正在 `00002_review_fixes.sql`），表结构以迁移文件为准，本文同步更新 · 最近更新：2026-10-11
> 原则见 [architecture.md §0](architecture.md#0-两个核心判断)。本文把它落到 PostgreSQL 上。迁移文件另有一些只为查询加的索引，这里不一一列出。

---

## 1. 数据分类

| 数据 | 性质 | 能不能清空 | 备份 |
|---|---|---|---|
| 评论、讨论页、站点、站点成员 | 真相源：博主和读者交给我们的内容 | 不能 | 要备份，异地保存 |
| 账号资料（显示名、头像、通知设置） | 真相源；读者随时可以删除 | 不能 | 要备份 |
| 会话、登录交易 | 凭据，过期即删 | 能，清空后所有人重新登录 | 不备份 |
| 通知、发信队列 | 流程记录，按保留期清理 | 能 | 不备份 |
| 审核记录、举报 | 流程记录，按保留期清理 | 不建议 | 要备份 |

数据库里**永远不出现**的东西：读者的浏览记录（谁看了哪一页）、明文 IP、明文邮箱、第三方社交平台的令牌、文章正文。读取评论不建任何记录，讨论页在第一条评论写入时才创建。§5 的 schema 守护会在有人加列时让 CI 失败。

---

## 2. 约定

沿用 Explore 的约定（[Explore data-model.md §2](https://github.com/kite-plus/explore/blob/main/docs/design/data-model.md#2-表结构-设计中)）：

- 主键用 `bigint GENERATED ALWAYS AS IDENTITY`；对外暴露的站点、会话另有 `public_id uuid`。评论 ID 直接用 `bigint`，接口里以字符串传递，页面锚点是 `#kc-<id>`。
- **读者对外的 ID 按站点区分**：组件接口、头像地址和博主导出里出现的是 `site_authors.public_id`（§3.1），不是账号本身的 ID。
- 时间一律 `timestamptz`；枚举用 `text` 加 `CHECK`；应用负责维护 `updated_at`。表名用复数蛇形。
- **DDL 里的默认值和上限**：`DEFAULT` 是 [sites.md](sites.md)、[moderation.md](moderation.md) 所定默认值的落地；长度、数量类的 `CHECK` 是硬上限，不低于 [moderation.md §3.4](moderation.md#34-限额-待定) 的政策值，调整政策值不需要改 schema。

另有三条隐私约定：

- **邮箱只存密文和 HMAC**。`*_enc` 是 AES-256-GCM 密文，首字节是密钥版本，可以轮换；`*_hmac` 是对规范化邮箱（去掉首尾空白、转小写）做的 HMAC-SHA256，只用来匹配（认领、封禁、合并访客通知），不能反推。两把密钥分开配置（[architecture.md §8](architecture.md#8-工程结构)）。HMAC 密钥不能轮换，轮换意味着重算所有 HMAC，要走专门的迁移。
- **IP 只存按月轮换的 HMAC**。`ip_hmac` 的密钥由主密钥按「年-月」派生，跨月无法关联；按 §4 的保留期置空。它只用于限流和封禁。
- **头像不外链**。账号头像从身份服务给的地址抓一次（和站点验证一样经过内网地址防护），缩到 96×96 存进 `avatars`，由 `comments.kite.plus` 提供。没有头像的读者和访客用生成的图形：读者按站点作者 ID 生成，访客按 `(站点, 邮箱 HMAC)` 生成，各站不同。不用 Gravatar：邮箱的 MD5 能被字典反推，Gravatar 在国内也不稳定。

---

## 3. 表结构 `[设计中]`

### 3.1 账号与身份

```sql
CREATE TABLE users (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id          uuid        NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    display_name       text        NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 40),
    avatar_kind        text        NOT NULL DEFAULT 'generated'
                                   CHECK (avatar_kind IN ('generated', 'idp')),
    homepage_url       text                 CHECK (char_length(homepage_url) <= 300),
    locale             text        NOT NULL DEFAULT 'zh-CN' CHECK (locale IN ('zh-CN', 'en')),
    email_enc          bytea,
    email_hmac         bytea,
    notify_replies     boolean     NOT NULL DEFAULT true,
    on_account_delete  text        NOT NULL DEFAULT 'remove'
                                   CHECK (on_account_delete IN ('remove', 'anonymize')),
    is_platform_admin  boolean     NOT NULL DEFAULT false,
    suspended_at       timestamptz,
    suspend_reason     text                 CHECK (char_length(suspend_reason) <= 500),
    idp_suspended_at   timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    last_login_at      timestamptz
);

CREATE INDEX users_email ON users (email_hmac);

CREATE TABLE user_identities (
    issuer     text        NOT NULL,
    subject    text        NOT NULL,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    linked_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issuer, subject)
);

CREATE TABLE site_authors (
    site_id     bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    user_id     bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    public_id   uuid        NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, user_id)
);

CREATE TABLE avatars (
    user_id       bigint      PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    content_type  text        NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg', 'image/webp')),
    bytes         bytea       NOT NULL CHECK (octet_length(bytes) <= 65536),
    etag          text        NOT NULL,
    fetched_at    timestamptz NOT NULL DEFAULT now()
);
```

| 字段 | 说明 |
|---|---|
| `users.public_id` | 只在读者中心和控制台里用，是读者对自己和对平台的 ID，不出现在任何站点的组件接口里 |
| `user_identities` | 与 Explore 同构：用户键是 `(iss, sub)`，不按邮箱合并（[identity-and-comments.md §3](https://github.com/kite-plus/explore/blob/main/docs/design/identity-and-comments.md#3-协议与数据)） |
| `display_name` | 首次登录时取身份服务的 `name`，没有时取 `preferred_username`，超过 40 字截断；两者都没有时用「读者」加 4 位随机字符。之后读者在读者中心自己改，不再随身份服务同步。不要求唯一 |
| `email_enc`、`email_hmac` | 只保存身份服务标记为已验证的邮箱，每次登录按身份服务刷新：身份服务给的邮箱变了就换成新的，没有或没验证就清空，认领和通知不会建立在旧邮箱上；用于回复通知、「认领访客评论」的自动提示（[sites.md §6](sites.md#6-认领历史评论)），以及 C4 转让站点时按邮箱找人 |
| `on_account_delete` | 删除账号时评论怎么处理，读者在读者中心设置；在身份服务上直接删账号时按这里的值执行（[login.md §7](login.md#7-账号删除与停用)） |
| `suspended_at`、`suspend_reason`、`idp_suspended_at` | 两种暂停分开记：前两个是平台维护者的暂停，`idp_suspended_at` 是身份服务停用账号造成的暂停。身份服务重新启用账号只清 `idp_suspended_at`，平台的暂停只能由平台解除（[login.md §7](login.md#7-账号删除与停用)）。任一个有值，账号就算暂停 |
| `is_platform_admin` | 平台维护者，只能由服务器操作者用命令授予（`comments users promote-admin`），和 Explore 一样 |
| `site_authors` | 读者在每个站点的公开 ID，在读者第一次登录这个站点时建立。不同站点之间对不上，任何人（包括博主）都不能凭 ID 把一个读者在各站的评论串起来；读者本人在读者中心看得到全部。显示名和头像是读者自己选的公开信息，同名的人仍可能被猜出是同一个 |

### 3.2 会话与登录交易

```sql
CREATE TABLE console_sessions (
    token_hash  bytea       PRIMARY KEY,
    user_id     bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    idp_sid     text        NOT NULL CHECK (char_length(idp_sid) <= 200),
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);

CREATE INDEX console_sessions_sid ON console_sessions (idp_sid);

CREATE TABLE site_sessions (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id     uuid        NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    token_hash    bytea       NOT NULL UNIQUE,
    user_id       bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    site_id       bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    idp_sid       text        NOT NULL CHECK (char_length(idp_sid) <= 200),
    created_via   text        NOT NULL CHECK (created_via IN ('popup', 'redirect')),
    client_label  text        NOT NULL DEFAULT '' CHECK (char_length(client_label) <= 60),
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL
);

CREATE INDEX site_sessions_user ON site_sessions (user_id, last_used_at DESC);
CREATE INDEX site_sessions_sid  ON site_sessions (idp_sid);

CREATE TABLE login_transactions (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    mode              text        NOT NULL CHECK (mode IN ('popup', 'redirect', 'console')),
    site_id           bigint               REFERENCES sites (id) ON DELETE CASCADE,
    challenge         bytea                UNIQUE,
    return_to         text                 CHECK (char_length(return_to) <= 2000),
    state_hash        bytea       NOT NULL UNIQUE,
    binder_hash       bytea       NOT NULL,
    oidc_nonce        text        NOT NULL,
    pkce_verifier     text        NOT NULL,
    user_id           bigint               REFERENCES users (id) ON DELETE CASCADE,
    idp_sid           text                 CHECK (char_length(idp_sid) <= 200),
    status            text        NOT NULL DEFAULT 'pending'
                                  CHECK (status IN ('pending', 'authenticated', 'confirmed', 'redeemed')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    authenticated_at  timestamptz,
    confirmed_at      timestamptz,
    redeemed_at       timestamptz,
    expires_at        timestamptz NOT NULL,

    CHECK ((mode = 'console') = (site_id IS NULL)),
    CHECK ((mode = 'console') = (challenge IS NULL)),
    CHECK ((mode = 'popup') = (return_to IS NULL)),
    CHECK (status = 'pending' OR (user_id IS NOT NULL AND idp_sid IS NOT NULL)),
    CHECK (status <> 'confirmed' OR mode = 'redirect')
);
```

| 字段 | 说明 |
|---|---|
| `console_sessions` | `comments.kite.plus` 自己页面（读者中心、站点控制台）的第一方会话，结构同 Explore 的 `sessions` |
| `site_sessions` | 组件在某个站点上的会话，一个 `(读者, 站点, 浏览器)` 一条，只对这个站点有效。令牌只存 SHA-256。`client_label` 是从 User-Agent 粗略归纳的「Safari · macOS」，给读者在读者中心辨认，不存原始 UA |
| `idp_sid` | ID Token 里的 `sid`，即身份服务的会话 ID。回调时先记在交易上，兑换或建立会话时抄过去；读者在 `id.kite.plus` 退出时，back-channel logout 按它删除会话（[login.md §6](login.md#6-退出)） |
| `login_transactions.mode` | `popup`、`redirect` 是组件登录（[login.md §4](login.md#4-组件登录协议)）；`console` 是读者中心和控制台的第一方登录，没有站点和 challenge |
| `return_to` | 整页跳转模式是回到哪篇文章（绝对地址，属于站点的已验证 origin）；`console` 模式是登录后回到本站的哪个相对路径 |
| `binder_hash` | 发起登录的那个浏览器的 binder Cookie 的哈希，回调和确认都要对得上（[login.md §4.5](login.md#45-参数)） |
| `status` | `pending` → `authenticated`（回调通过）→ 整页跳转模式还要 `confirmed`（读者在确认页确认）→ `redeemed`（组件兑换，或第一方登录建立了会话） |

### 3.3 站点

```sql
CREATE TABLE sites (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id        uuid        NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    name             text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    status           text        NOT NULL DEFAULT 'unverified'
                                 CHECK (status IN ('unverified', 'pending_review', 'active', 'suspended', 'deleted')),
    status_note      text                 CHECK (char_length(status_note) <= 500),
    guest_policy     text        NOT NULL DEFAULT 'off'
                                 CHECK (guest_policy IN ('off', 'review', 'publish')),
    member_policy    text        NOT NULL DEFAULT 'publish'
                                 CHECK (member_policy IN ('publish', 'review')),
    sort_order       text        NOT NULL DEFAULT 'oldest' CHECK (sort_order IN ('oldest', 'newest')),
    close_after_days integer              CHECK (close_after_days BETWEEN 1 AND 3650),
    query_keys       text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(query_keys) <= 20),
    allow_localhost  boolean     NOT NULL DEFAULT true,
    owner_notify     text        NOT NULL DEFAULT 'instant'
                                 CHECK (owner_notify IN ('instant', 'daily', 'off')),
    blocked_words    text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(blocked_words) <= 1000),
    created_by       bigint               REFERENCES users (id) ON DELETE SET NULL,
    verified_at      timestamptz,
    approved_at      timestamptz,
    suspended_at     timestamptz,
    deleted_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    CHECK ((status = 'deleted') = (deleted_at IS NOT NULL))
);

CREATE TABLE site_origins (
    origin           text        NOT NULL
                                 CHECK (origin ~ '^https?://' AND char_length(origin) <= 300),
    site_id          bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    is_primary       boolean     NOT NULL DEFAULT false,
    verified_at      timestamptz,
    proof_hash       bytea,
    last_checked_at  timestamptz,
    failing_since    timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, origin)
);

CREATE UNIQUE INDEX site_origins_primary  ON site_origins (site_id) WHERE is_primary;
CREATE UNIQUE INDEX site_origins_verified ON site_origins (origin) WHERE verified_at IS NOT NULL;

CREATE TABLE site_members (
    site_id           bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    user_id           bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role              text        NOT NULL CHECK (role IN ('owner', 'moderator')),
    last_notified_at  timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, user_id)
);

CREATE TABLE site_verifications (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id      bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    token_hash   bytea       NOT NULL,
    created_by   bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_error   text                 CHECK (char_length(last_error) <= 500)
);
```

| 字段 | 说明 |
|---|---|
| `status` | `unverified` 没有任何已验证的 origin；`pending_review` 已验证、等平台批准（只在 C2 内测期存在）；`active` 至少有一个已验证的 origin，可以嵌入；`suspended` 被平台暂停，或所有者已不在；`deleted` 所有者删除后的恢复窗口（§4）。各状态下组件显示什么见 [widget.md §4](widget.md#4-iframe-地址) |
| `suspended_at` | 平台暂停的标记，和 `status` 分开存：所有者删除再恢复一个被暂停的站点，它恢复成 `suspended`，`status_note` 保留，暂停不会因此解除（[sites.md §4](sites.md#4-站点设置与讨论页管理)）。只有平台解除暂停时清空 |
| `guest_policy`、`member_policy` | 含义见 [moderation.md §2](moderation.md#2-发布规则)。访客评论的默认值 `off` 只是兜底：C3 起新建站点时由博主明确选择（[sites.md §4](sites.md#4-站点设置与讨论页管理)） |
| `query_keys` | 计入讨论页标识的查询参数，比如 WordPress 朴素链接的 `p`（[widget.md §5](widget.md#5-讨论页标识)） |
| `allow_localhost` | 允许在 `http://localhost:*`、`http://127.0.0.1:*` 上显示只读的组件，方便博主本地预览主题（[widget.md §4](widget.md#4-iframe-地址)） |
| `site_origins` | 一个 origin（`https://host[:port]`）只能被一个站点**验证**，**每个都要单独验证**。没验证的登记不独占：几个站点可以同时登记同一个 origin，谁先通过验证谁得到它，其他站点再验证时失败、也不能再登记它；否则别人可以先登记你的域名占着不验证，让你注册不了。从未验证的登记在验证令牌的有效期（7 天）后由清理任务删除（§4）。只接受 https：约束允许 `http://`，只是为了本地开发服务器（`COMMENTS_DEV_HTTP_ORIGINS`），生产服务在应用层拒绝 http（[sites.md §2](sites.md#2-注册与验证)）。`last_checked_at`、`failing_since` 用于定期复查 |
| `site_origins.proof_hash` | 证明这个 origin 的那个验证令牌的哈希。令牌本身 7 天后过期删除，定期复查认这个哈希，所以博主不用为了复查重新生成令牌 |
| `site_members.last_notified_at` | 上次给这位管理者发站长通知的时间，用于合并通知和每日汇总（[moderation.md §6.2](moderation.md#62-博主)） |
| `site_verifications` | 一个站点的验证令牌，可以同时用来验证它的多个 origin，过期后重新生成。有效期和验证方式见 [sites.md §2](sites.md#2-注册与验证) |

### 3.4 讨论页与评论

```sql
CREATE TABLE threads (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id            bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    page_key           text        NOT NULL CHECK (char_length(page_key) <= 512),
    url                text        NOT NULL CHECK (char_length(url) <= 2000),
    title              text        NOT NULL DEFAULT '' CHECK (char_length(title) <= 300),
    page_published_at  timestamptz,
    locked_at          timestamptz,
    comment_count      integer     NOT NULL DEFAULT 0,
    last_comment_at    timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (site_id, page_key)
);

CREATE TABLE comments (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id            bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    thread_id          bigint      NOT NULL REFERENCES threads (id) ON DELETE CASCADE,
    root_id            bigint               REFERENCES comments (id) ON DELETE CASCADE,
    parent_id          bigint               REFERENCES comments (id) ON DELETE CASCADE,
    author_kind        text        NOT NULL
                                   CHECK (author_kind IN ('user', 'guest', 'external', 'former_user')),
    author_user_id     bigint               REFERENCES users (id) ON DELETE SET NULL,
    author_name        text                 CHECK (char_length(author_name) BETWEEN 1 AND 40),
    author_email_enc   bytea,
    author_email_hmac  bytea,
    author_url         text                 CHECK (char_length(author_url) <= 300),
    author_ref         text                 CHECK (char_length(author_ref) <= 100),
    notify_email       boolean     NOT NULL DEFAULT false,
    body_md            text        NOT NULL CHECK (char_length(body_md) <= 10000),
    body_html          text        NOT NULL,
    render_version     smallint    NOT NULL,
    status             text        NOT NULL
                                   CHECK (status IN ('pending', 'published', 'spam', 'hidden', 'deleted')),
    pinned_at          timestamptz,
    ip_hmac            bytea,
    source             text        NOT NULL DEFAULT 'native'
                                   CHECK (source IN ('native', 'json', 'waline', 'twikoo', 'artrans', 'wxr', 'disqus', 'giscus')),
    source_id          text                 CHECK (char_length(source_id) <= 200),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    edited_at          timestamptz,
    published_at       timestamptz,

    CHECK ((root_id IS NULL) = (parent_id IS NULL)),
    CHECK (pinned_at IS NULL OR root_id IS NULL),
    CHECK ((author_kind = 'user') = (author_user_id IS NOT NULL)),
    CHECK (author_kind NOT IN ('guest', 'external') OR author_name IS NOT NULL),
    CHECK ((source = 'native') = (source_id IS NULL))
);

CREATE INDEX comments_thread  ON comments (thread_id, root_id, created_at, id);
CREATE INDEX comments_queue   ON comments (site_id, status, created_at DESC);
CREATE INDEX comments_author  ON comments (author_user_id, created_at DESC) WHERE author_user_id IS NOT NULL;
CREATE INDEX comments_claim   ON comments (author_email_hmac) WHERE author_kind = 'guest';
CREATE UNIQUE INDEX comments_source ON comments (site_id, source, source_id) WHERE source <> 'native';
CREATE INDEX comments_body_recent ON comments (md5(body_md), created_at);

CREATE TABLE comment_revisions (
    comment_id  bigint      NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
    revised_at  timestamptz NOT NULL,
    body_md     text        NOT NULL,
    PRIMARY KEY (comment_id, revised_at)
);
```

| 字段 | 说明 |
|---|---|
| `threads.page_key` | 讨论页标识，规则只写在 [widget.md §5](widget.md#5-讨论页标识) |
| `threads.url`、`title` | 第一条评论创建讨论页时写入，之后只有管理者能改；一律当纯文本处理。用于通知邮件和控制台，不当标识用。这样评论者不能借后续评论改写别人收到的邮件里的标题和链接 |
| `page_published_at` | 嵌入代码的 `page-published` 属性给的文章发布时间，用于自动关闭；没有时按讨论页的创建时间算（[sites.md §4](sites.md#4-站点设置与讨论页管理)） |
| `comment_count` | 已发布评论数（含回复），和评论写入在同一事务里维护 |
| `root_id`、`parent_id` | 两层结构：顶层评论两者都为空；回复的 `root_id` 指向所在顶层评论，`parent_id` 指向被回复的那条（用于显示「回复 @某人」）。更深的回复也挂在同一个顶层下，不再嵌套 |
| `author_kind` | `user` 登录读者；`guest` 访客，或导入的带邮箱的评论，可以按邮箱认领；`external` 导入的、没有可认领邮箱的作者：本服务导出里的账号评论、Disqus、Giscus 的评论，只保留显示名，Giscus 的另在 `author_ref` 记下 GitHub 用户名，C4 可以用 GitHub 登录认领；`former_user` 作者已注销 |
| `author_*` | 访客和导入作者的资料。登录读者的显示名、头像不存在这里，按 `author_user_id` 取当前值 |
| `(author_kind = 'user') = (author_user_id IS NOT NULL)` | `author_user_id` 在删除账号时会被置空，这条约束让「没先把评论改成 `former_user` 就删账号」直接失败，保证 [login.md §7](login.md#7-账号删除与停用) 的顺序 |
| `body_md`、`body_html` | 读者写的 Markdown 原文，和服务端渲染、净化后的 HTML。渲染规则升级时 `render_version` 加一，由 worker 重渲染旧评论（[moderation.md §5](moderation.md#5-内容格式)） |
| `status` | `pending` 待审；`published` 公开；`spam`、`hidden` 被判为垃圾或被管理者隐藏；`deleted` 作者删除。状态流转见 [moderation.md §2](moderation.md#2-发布规则) |
| `source`、`source_id` | 导入来源和原系统里的 ID，保证重复导入不产生重复评论。`json` 是本服务自己的导出格式 |
| `comment_revisions` | 编辑前的版本，供审核追查，保留期见 §4 |
| `comments_body_recent` | 「相同正文 1 小时内出现在多个站点」这条反垃圾规则用（[moderation.md §3.1](moderation.md#31-通用规则)），是表达式索引，不加列 |

### 3.5 审核、举报与封禁

```sql
CREATE TABLE reports (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    comment_id        bigint      NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
    site_id           bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    reporter_user_id  bigint               REFERENCES users (id) ON DELETE SET NULL,
    reason            text        NOT NULL
                                  CHECK (reason IN ('spam', 'abuse', 'illegal', 'privacy', 'other')),
    note              text        NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    status            text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    resolved_at       timestamptz,
    resolved_by       bigint               REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE bans (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id     bigint               REFERENCES sites (id) ON DELETE CASCADE,
    issuer      text                 CHECK (char_length(issuer) <= 300),
    subject     text                 CHECK (char_length(subject) <= 300),
    email_hmac  bytea,
    ip_hmac     bytea,
    reason      text        NOT NULL DEFAULT '' CHECK (char_length(reason) <= 500),
    created_by  bigint               REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz,
    CHECK (num_nonnulls(subject, email_hmac, ip_hmac) = 1),
    CHECK ((issuer IS NULL) = (subject IS NULL))
);

CREATE TABLE spam_domains (
    domain      text        PRIMARY KEY CHECK (char_length(domain) BETWEEN 1 AND 253),
    created_by  bigint               REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE moderation_events (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id        bigint               REFERENCES sites (id) ON DELETE CASCADE,
    comment_id     bigint               REFERENCES comments (id) ON DELETE SET NULL,
    actor_user_id  bigint               REFERENCES users (id) ON DELETE SET NULL,
    action         text        NOT NULL CHECK (char_length(action) <= 40),
    detail         jsonb       NOT NULL DEFAULT '{}',
    created_at     timestamptz NOT NULL DEFAULT now()
);
```

- `spam_domains` 是平台维护的垃圾域名清单：评论里的链接指向清单里的域名或它的子域名时判为垃圾（[moderation.md §3.1](moderation.md#31-通用规则)）。只有平台维护者能改。
- `bans.site_id` 为空表示全网封禁，只有平台维护者能建。IP 封禁怎么匹配见 [moderation.md §4.1](moderation.md#41-站点管理者)。
- 封禁读者时记的是身份 `(issuer, subject)`，不是账号 ID：读者删除账号数据后封禁还在，用同一个身份重新登录、建出新账号时照样生效。这是账号删除后唯一留下的、能认出这个人的东西，只用来执行封禁，保存到封禁被解除或过期为止（§4）；隐私政策里写明。
- `moderation_events.actor_user_id` 为空表示系统自动处理（反垃圾规则、过期关闭）。`action` 的取值和 `detail` 的内容见 [moderation.md §4.4](moderation.md#44-处置记录)。迁移讨论页时，被移动的评论 ID 记在 `detail` 里，撤销时按它移回。

### 3.6 通知、访客邮箱与认领

```sql
CREATE TABLE notifications (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        text        NOT NULL CHECK (kind IN ('reply')),
    comment_id  bigint      NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    read_at     timestamptz,
    emailed_at  timestamptz
);

CREATE INDEX notifications_inbox ON notifications (user_id, created_at DESC);

CREATE TABLE thread_mutes (
    user_id     bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    thread_id   bigint      NOT NULL REFERENCES threads (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, thread_id)
);

CREATE TABLE guest_emails (
    email_hmac          bytea       PRIMARY KEY,
    email_enc           bytea       NOT NULL,
    confirm_sent_at     timestamptz,
    confirmed_at        timestamptz,
    opted_out_at        timestamptz,
    locale              text        NOT NULL DEFAULT 'zh-CN' CHECK (locale IN ('zh-CN', 'en')),
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE guest_consents (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email_hmac          bytea       NOT NULL,
    browser_hash        bytea,
    comment_id          bigint               REFERENCES comments (id) ON DELETE CASCADE,
    confirm_token_hash  bytea       NOT NULL UNIQUE,
    created_at          timestamptz NOT NULL DEFAULT now(),
    confirmed_at        timestamptz
);

CREATE TABLE email_claims (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id        bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email_hmac     bytea       NOT NULL,
    token_hash     bytea       NOT NULL UNIQUE,
    created_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,
    verified_at    timestamptz,
    confirmed_at   timestamptz,
    claimed_count  integer     NOT NULL DEFAULT 0
);

CREATE TABLE outbox (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind             text        NOT NULL CHECK (char_length(kind) <= 40),
    to_enc           bytea       NOT NULL,
    coalesce_key     text                 CHECK (char_length(coalesce_key) <= 200),
    locale           text        NOT NULL CHECK (locale IN ('zh-CN', 'en')),
    payload          jsonb       NOT NULL,
    status           text        NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sent', 'failed')),
    attempts         smallint    NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    last_error       text                 CHECK (char_length(last_error) <= 500),
    created_at       timestamptz NOT NULL DEFAULT now(),
    sent_at          timestamptz
);

CREATE INDEX outbox_due      ON outbox (next_attempt_at) WHERE status = 'queued';
CREATE INDEX outbox_coalesce ON outbox (coalesce_key) WHERE status = 'queued';
```

- `notifications` 只给登录读者，是读者中心里的通知。访客没有读者中心，回复通知只有邮件。
- `thread_mutes` 是登录读者在某个讨论页关闭的通知；访客只能按邮箱整体退订（`guest_emails.opted_out_at`）。
- `guest_emails.locale` 是这个访客最近一次留言时组件的语言，确认信和回复通知按它发（[moderation.md §6.1](moderation.md#61-读者)）。
- 访客的回复通知要按**浏览器**确认（[moderation.md §6.1](moderation.md#61-读者)）：组件在 iframe 的存储里给每个浏览器生成一个访客令牌，`guest_consents.browser_hash` 是它的哈希。访客勾选「有人回复时通知我」时，只有这个邮箱在这个浏览器上确认过，这条评论才直接开通知；否则发一封确认信，确认后这个浏览器记为已同意，这条评论（`comment_id`）开通知。这样拿别人的邮箱留言，换一个浏览器也只能让对方收到一封确认信，收不到回复通知。`guest_emails.confirm_sent_at` 限制每个邮箱一天最多一封确认信，`confirmed_at` 记这个邮箱第一次确认的时间，`opted_out_at` 是整体退订，退订时删掉它的全部同意。
- `email_claims` 只能由发起它的账号完成验证，验证和确认都要这个账号的控制台会话（[sites.md §6](sites.md#6-认领历史评论)）。一个认领只能确认一次（`confirmed_at`），只认领验证（`verified_at`）之前用这个邮箱留下的访客评论；之后再用这个邮箱留的访客评论仍是访客的。
- 发信一律经 `outbox`，由 worker 发送和重试；接口层不直接连 SMTP。`coalesce_key` 相同的待发邮件由 worker 合并成一封：回复通知按读者或访客邮箱合并，站长通知按站点和管理者合并。

### 3.7 导入与杂项

```sql
CREATE TABLE imports (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id      bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    format       text        NOT NULL CHECK (format IN ('json', 'waline', 'twikoo', 'artrans', 'wxr', 'disqus', 'giscus')),
    status       text        NOT NULL DEFAULT 'uploaded'
                             CHECK (status IN ('uploaded', 'previewed', 'running', 'done', 'failed')),
    file         bytea                CHECK (octet_length(file) <= 104857600),
    mapping      jsonb       NOT NULL DEFAULT '{}',
    stats        jsonb       NOT NULL DEFAULT '{}',
    error        text                 CHECK (char_length(error) <= 2000),
    created_by   bigint               REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz
);

CREATE TABLE idp_events (
    event_id     text        PRIMARY KEY,
    kind         text        NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE idp_sync (
    id          smallint    PRIMARY KEY CHECK (id = 1),
    cursor      text        NOT NULL,
    synced_at   timestamptz NOT NULL
);

CREATE TABLE pow_spent (
    challenge_id  bytea       PRIMARY KEY,
    expires_at    timestamptz NOT NULL
);
```

- `imports.file` 是解析后的评论（JSON），访客邮箱已经加密成密文和 HMAC；上传的原始文件解析完就丢弃，从不落库。`file` 在上传 24 小时后置空（§4），只留统计；`mapping` 是博主在预览时加的地址改写规则（[sites.md §7](sites.md#7-导入)）。
- `idp_events` 只记录从事件查询（对账）里读到并处理过的事件 ID，同一个事件只处理一次。推送只是提示，不记 ID：推送里的事件 ID 是推送方自己写的，记下来的话，一条伪造的推送就能抢先占住真事件的 ID，让对账跳过它；`idp_sync` 只有一行，是每小时对账的游标（[login.md §7](login.md#7-账号删除与停用)）。
- `pow_spent` 记录已用过的访客工作量证明，防止同一个解重复提交（[moderation.md §3.2](moderation.md#32-访客)）。

---

## 4. 保留期

由 worker 每小时清理一次。数值只写在这里，代码常量在 `internal/policy`。

| 数据 | 保留 |
|---|---|
| 登录交易（包括已兑换的） | 过期后 1 天删除（有效期见 [login.md §4.5](login.md#45-参数)） |
| 控制台会话、站点会话 | 过期即删；撤销时立即删除 |
| 站点验证令牌、已用过的工作量证明 | 过期即删 |
| 作者删除的评论（`deleted`） | 立即从页面消失；正文 14 天后清空，只留占位（有回复时显示「该评论已删除」） |
| 隐藏和垃圾评论（`hidden`、`spam`） | 管理者 90 天内可以恢复；之后清空正文 |
| 评论的历史版本 | 30 天 |
| `ip_hmac` | 30 天后置空 |
| 通知 | 180 天 |
| 已发出的邮件 | 发出 7 天后删除 |
| 发送失败的邮件 | 30 天 |
| 已处理的举报 | 180 天 |
| 审核记录 | 1 年 |
| 未确认的访客邮箱 | 30 天 |
| 未确认的访客回复同意（`guest_consents`） | 30 天 |
| 认领请求 | 没验证的过期即删；完成的 30 天后删除 |
| 导入的解析结果（`imports.file`） | 上传 24 小时后置空，不管导入进行到哪一步；原始文件从不保存 |
| 未验证的站点和 origin | 从未验证过任何 origin、也没被批准过的站点，7 天后连同登记删除；站点里从未验证过的别名 7 天后删除 |
| 封禁 | 解除时删除；有期限的过期后删除。读者的封禁记身份 `(issuer, subject)`，账号删除后照样保留到这时（§3.5） |
| 已处理的身份服务事件 ID | 30 天 |
| 删除的站点 | 30 天内可以恢复；之后连同讨论页和评论彻底删除 |

账号删除按 [login.md §7](login.md#7-账号删除与停用) 立即执行，不等保留期；选择「删除评论」时，评论走 `deleted` 的流程；队列里还没发给这个账号的邮件一并删除，worker 发信前也会重新读收件人，已删除、已暂停、关了通知或静音了讨论页的不发。备份的保存期见 [architecture.md §9](architecture.md#9-部署与运维)。

---

## 5. schema 守护

沿用 Explore 的做法：`testdata/schema/columns.txt` 列出每张表的每一列，迁移后和实际 schema 比对，不一致 CI 就失败。加列必须同时改这个文件和本文，审查时对照 §1 的「永远不出现」清单。

`TestSchemaColumns`（`internal/store`）就是这个守护；它另外拒绝看起来像「永远不出现」的列名（`ip`、`user_agent`、`email`、`password`、`content` 等，`_enc`、`_hmac` 结尾的除外）。

另有几条自动化测试，都已实现：

1. **读取不留痕**（`TestT8ReadsCarryNoSession`，以及浏览器里的 T8 测试）：浏览器同时带着有效的站点会话 Cookie 和控制台 Cookie，依次调用所有匿名读取接口（[api.md §3](api.md#3-组件读取匿名)）和 iframe 文档之后，任何表的行数和内容都不变，会话的 `last_used_at` 也不变。
2. **邮箱不落明文**（`TestEmailsNeverStoredInPlainText`，以及认领、导入的测试）：灌入带邮箱的测试数据（原生、访客、导入三种来源）后，扫描全库文本列和二进制（`bytea`）列，找不到任何明文邮箱；导入保存的解析结果也在其中。
3. **ID 不跨站**（`TestAuthorIDsDifferAcrossSites`）：同一个读者在两个站点发表评论，两个站点的接口和导出里找不到相同的作者 ID。
