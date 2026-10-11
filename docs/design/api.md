# 接口

> 状态：C2 已实现，以代码为准同步更新 · 最近更新：2026-10-11
> 登录流程见 [login.md](login.md)，表结构见 [data-model.md](data-model.md)。本文只列接口和约定，业务规则链接到各自的文档。读者中心和站点控制台的前端约定见 §11。

---

## 1. 约定

沿用 Explore 的接口约定（[Explore api.md §1](https://github.com/kite-plus/explore/blob/main/docs/design/api.md)），只在分页大小上不同：

- 前缀 `/api/v1`，不兼容的改动放 `/api/v2`。JSON 字段用 snake_case。
- 时间是 UTC 的 RFC 3339。`bigint` 的 ID（评论、讨论页、举报、封禁、审核记录、导入）以字符串传递；站点、会话、账号用 `uuid`；读者在组件接口里用按站点区分的作者 ID（[data-model.md §3.1](data-model.md#31-账号与身份)）。
- 错误统一为 `{"error": {"code": "…", "message": "…"}}`，`message` 按 `Accept-Language` 返回中文或英文（没有这个头时中文），错误码见 §10。
- 列表返回 `{"data": […], "next_cursor": "…" | null}`，游标是 `"unixnanos:id"` 的 base64url，原样传回 `cursor` 参数即可。评论列表默认 20 条，最多 50 条（`limit`）；其余列表默认 30 条，最多 100 条。
- 匿名读取返回 `Cache-Control: public, max-age=30` 和弱 ETag；带身份的响应一律 `private, no-store`。
- **没有带凭据的 CORS**。所有带身份的调用方都和接口同源：组件 iframe、弹窗页、读者中心、控制台都在 `comments.kite.plus` 上。唯一的跨源读取是宿主页加载 `embed.js`（§3）。
- 请求体是 JSON 的接口要求 `Content-Type: application/json`，否则 `415`。

评论对象：

```json
{
  "id": "18342",
  "root_id": null,
  "parent_id": null,
  "reply_to": null,
  "author": {
    "kind": "user",
    "id": "5c1e…",
    "name": "某读者",
    "url": "https://example.org",
    "avatar": "/avatars/5c1e…?v=3",
    "badge": "owner"
  },
  "body_html": "<p>…</p>",
  "status": "published",
  "pinned": false,
  "created_at": "2026-09-25T08:00:00Z",
  "edited_at": null,
  "reply_count": 4,
  "replies": [],
  "replies_cursor": "…"
}
```

- `author.id` 是这位读者在**这个站点**的作者 ID，换一个站点就不同。`author.kind` 为 `guest`、`external` 时没有 `id`；`former_user` 时 `name` 是空串，由界面显示「已注销用户」。`avatar` 是本站的相对地址：读者选了身份服务的头像时是 `/avatars/<作者 ID>?v=<版本>`，否则是 `/avatars/generated/<种子>.svg`（访客的种子按站点和邮箱 HMAC 算，各站不同）。邮箱永远不出现在组件接口里。
- `badge` 只在作者是本站所有者（`owner`）或协管员（`moderator`）时出现。
- `reply_to` 只在回复的不是所在顶层评论时出现：`{"id", "name"}`。
- `status` 只在管理者看到的响应和作者自己发表、编辑的响应里出现；公开响应只含 `published` 的评论，所以省略这个字段。作者看到的「垃圾」显示为 `pending`（[moderation.md §2](moderation.md#2-发布规则)）。
- `replies` 只在顶层评论里出现，**带前 3 条回复**；`reply_count` 是已发布回复的总数，`replies_cursor` 是接着取其余回复的游标（§3）。
- **占位**：顶层评论已删除、被隐藏或是垃圾，但下面还有可见的回复时，返回 `{"id", "root_id", "parent_id", "created_at", "placeholder": "deleted" | "hidden", "reply_count", "replies", "replies_cursor"}`，没有作者和正文，保持讨论结构。

---

## 2. 鉴权

| 凭据 | 用在哪里 | 怎么带 | 防跨站请求 |
|---|---|---|---|
| 无 | 匿名读取 | 不带：`embed.js` 以 `crossorigin="anonymous"` 加载，组件的读取请求用 `credentials: "omit"`；iframe 文档和匿名读取路由的 Cookie 请求头在反向代理处删掉，服务端也在这些路由上删掉（[widget.md §4](widget.md#4-iframe-地址)） | 不需要 |
| 站点会话 | 组件里的写操作、管理者读取和「我是谁」 | 分区 Cookie `__Host-comments_site_<站点 ID>`；浏览器不支持时退回 `Authorization: Bearer <令牌>`，两者是同一个令牌（[login.md §5](login.md#5-站点会话)） | 宿主页和组件共享 Cookie 分区，所以 §4、§5 的组件接口要求 `Sec-Fetch-Site: same-origin`，没有这个头时要求 `Origin` 为服务自己的 origin，两者都没有就拒绝（`403 forbidden`） |
| 控制台会话 | 读者中心、站点控制台、平台管理 | Cookie `__Host-comments_session`：`HttpOnly`、`Secure`、`SameSite=Lax`（[login.md §3](login.md#3-三种会话)） | 非 GET 请求带 `X-CSRF-Token`，值是 `GET /api/v1/me` 返回的 `csrf_token`（由会话令牌派生，算法同 Explore）；缺少或不对返回 `403 forbidden` |
| 站点 API 令牌（C4） | Kite 后台等服务端集成 | `Authorization: Bearer kcs_…` | 只从服务端调用 |
| 签名 | 身份服务的事件推送、back-channel logout | `ZITADEL-Signature` 请求头、Logout Token（[login.md §6](login.md#6-退出)、[§7](login.md#7-账号删除与停用)） | —— |

- 站点会话只对签发它的站点有效：组件接口都要带 `site`，服务端按它取对应的 Cookie，并核对会话的 `site_id`，不一致按未登录处理。
- 登录入口另有自己的校验，只接受服务自己页面发出的请求（§4）。
- 本地开发服务器跑在纯 http 上时，三个 Cookie 去掉 `__Host-` 前缀（浏览器不接受 http 上的这个前缀），名字是 `comments_session`、`comments_binder`、`comments_site_<站点 ID>`；站点会话 Cookie 仍带 `Secure`、`SameSite=None`、`Partitioned`，Chrome 在 `http://localhost` 上接受（[login.md §3](login.md#3-三种会话)）。

---

## 3. 组件读取（匿名）

这些路由在反向代理处删掉 Cookie 请求头，服务端也删，收不到任何会话（[widget.md §4](widget.md#4-iframe-地址)）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/embed.js` | 宿主页加载器（[widget.md §3](widget.md#3-结构)），带 `Access-Control-Allow-Origin: *`，`max-age=300`。服务端把文件里的占位换成自己的 origin（[widget.md §10](widget.md#10-加载体积与降级)） |
| GET | `/embed/v1.<次版本>.<补丁>.js` | 固定版本的加载器，内容不可变，长期缓存，同样允许跨源读取，可以配合 SRI |
| GET | `/api/v1/embed-info` | `{version, script_url, pinned_url, integrity}`：控制台显示固定版本写法用，`integrity` 是 `sha384-…` |
| GET | `/embed/v1/thread` | iframe 文档，参数和响应头见 [widget.md §4](widget.md#4-iframe-地址) |
| GET | `/static/widget/<文件>` | 组件界面的脚本和弹窗页脚本，文件名带哈希，长期缓存 |
| GET | `/api/v1/threads/comments` | 参数 `site`、`key`（讨论页标识，即 iframe 文档里内联的 `key`）、`sort`（`oldest`、`newest`，省略时用站点设置）、`cursor`、`limit`；或用 `around=<评论 ID>` 取包含这条评论的那一页 |
| GET | `/api/v1/comments/{id}/replies` | 某条顶层评论的其余回复，按时间正序分页；第一页用评论对象的 `replies_cursor` |
| GET | `/avatars/{作者 ID}` | 缓存的头像，带与 ETag 相同的 `?v=` 时长期缓存 |
| GET | `/avatars/generated/{种子}.svg` | 生成的头像 |

`GET /api/v1/threads/comments` 的响应：

```json
{"data": [评论对象…], "next_cursor": "…" | null, "count": 12, "locked": false, "closed": false, "partial": false, "thread_id": "77"}
```

- `count` 是已发布评论数（含回复）；`locked` 是管理者锁定；`closed` 是已到自动关闭的时间（[sites.md §4](sites.md#4-站点设置与讨论页管理)）。
- 第一页（没有 `cursor`）先列置顶的顶层评论，再按排序列其余的；`next_cursor` 只翻非置顶的部分。
- `around`：目标在前 200 条顶层评论里时，返回从头到包含它的那一页，回复带到目标为止；更靠后时页面从它开始，`partial` 为 `true`，界面给出「查看全部评论」。目标不存在或不可见时当作没有 `around`。
- 讨论页不存在时返回空列表、`count: 0`、没有 `thread_id`，不建记录（[data-model.md §1](data-model.md#1-数据分类)）。
- 站点不是 `active` 或 `suspended`：`403 site_inactive`。

---

## 4. 组件登录

流程和每一步的校验见 [login.md §4](login.md#4-组件登录协议)。「只接受本站页面」指：`Origin` 必须是服务自己的 origin，有 `Sec-Fetch-Site` 时必须是 `same-origin`。

| 方法 | 路径 | 调用方 | 说明 |
|---|---|---|---|
| GET | `/auth/popup` | 组件用 `window.open` 打开 | 弹窗页：与打开它的组件握手；`frame-ancestors 'none'`，不带 COOP |
| POST | `/auth/transactions` | 弹窗页用 `fetch` 调用 | `{site, challenge}` → `{authorize_url}`。只接受本站页面；另要求 `Sec-Fetch-Mode` 不是 `navigate`、`Content-Type` 为 JSON；从不返回跳转。`challenge` 是 `base64url(SHA-256(verifier))`，32 字节；重复的 challenge 返回 `409 conflict`；身份服务连不上返回 `503 unavailable` |
| POST | `/auth/redirect` | 组件提交的 `target="_top"` 表单 | 整页跳转入口，字段 `site`、`challenge`、`return`。只接受本站页面；`return` 必须属于站点的已验证 origin，片段去掉；建交易后 `303` 到身份服务。出错时显示页面而不是 JSON |
| GET | `/auth/callback` | 身份服务回跳 | 校验 `state` 和 binder，完成交易；弹窗模式显示「登录成功」并尝试关闭，整页跳转模式显示确认页，`console` 模式建立控制台会话后 `303` 回 `return` |
| POST | `/auth/confirm` | 确认页的表单 | 字段 `tx`（交易 ID）。只接受本站页面，binder 要和交易一致，交易须为整页跳转模式且已 `authenticated`；通过后 `303` 到 `return#kc-login` |
| POST | `/api/v1/widget/sessions` | 组件 | 兑换：`{site, verifier}`。交易不存在或还没完成返回 `202 {"status": "pending"}`；完成则设置这个站点的分区 Cookie 并返回 `201 {"token", "expires_at", "viewer"}`，组件据此选载体；过期或已兑换返回 `410 login_expired`；账号被暂停返回 `403` |
| GET | `/api/v1/widget/viewer` | 组件 | 参数 `site`，返回 `{"viewer": {author_id, name, avatar, role, notify_replies, has_email}}`，`role` 是 `""`、`owner` 或 `moderator`；未登录或被暂停返回 `{"viewer": null}` |
| DELETE | `/api/v1/widget/session` | 组件 | 参数 `site`，退出此站点，同时让分区 Cookie 过期 |
| GET | `/auth/login` | 读者中心、控制台 | 第一方 OIDC 登录（交易模式 `console`），参数 `return`（本站以 `/` 开头的相对路径，不合规时用 `/me`） |
| POST | `/auth/logout` | 读者中心、控制台 | 删除控制台会话；要求控制台会话和 `X-CSRF-Token`，返回 `204` |

---

## 5. 组件写入与管理者读取

都要求 `Sec-Fetch-Site: same-origin`（§2）；除访客发表和预览外都要求站点会话。

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/comments` | 发表，见下 |
| GET | `/api/v1/comments/{id}/source` | 参数 `site`。作者取自己评论的 Markdown 原文，编辑用：`{body}` |
| PATCH | `/api/v1/comments/{id}` | 作者在编辑窗口内修改 `{site, body}`，重新走发布规则的第 5–7 条（[moderation.md §2](moderation.md#2-发布规则)）；超过窗口 `403 edit_window_closed`；作者被封禁时 `403 forbidden`；站点不是 `active` 时 `403 site_inactive` |
| DELETE | `/api/v1/comments/{id}` | 参数 `site`。作者删除，`204`。站点暂停、待审或在恢复窗口里也能删 |
| POST | `/api/v1/comments/{id}/reports` | 登录读者举报 `{site, reason, note?}`，`reason` 是 `spam`、`abuse`、`illegal`、`privacy`、`other`；只能举报已发布的评论；同一读者对同一条评论只记一次 |
| GET | `/api/v1/widget/threads/comments` | 只对本站管理者开放，参数同 §3 的讨论页评论接口，另可按 `status` 筛选（`published`、`pending`、`hidden`、`spam`）；返回包括待审、隐藏、垃圾在内的评论，响应 `private, no-store`。组件在管理者登录后改用它 |
| POST | `/api/v1/comments/{id}/moderation` | 管理者处置 `{site, action}`：`approve`、`hide`、`spam`、`restore`、`pin`、`unpin`，返回评论对象。封禁只在控制台里做（§7） |
| PUT | `/api/v1/threads/lock` | 管理者锁定或解锁 `{site, key, locked}`；讨论页不存在返回 `404` |
| POST | `/api/v1/preview` | Markdown 预览 `{body}` → `{html}`，和发表用同一个渲染器 |
| GET | `/api/v1/pow/challenge` | 参数 `site`，访客的工作量证明题目 `{algorithm, challenge, maxnumber, salt, signature}`（[moderation.md §3.2](moderation.md#32-访客)）；站点不接受访客时 `403 guest_not_allowed` |

`POST /api/v1/comments` 的请求体：

```json
{
  "site": "站点 ID",
  "key": "p:/posts/hello",
  "url": "https://blog.example.com/posts/hello/",
  "title": "文章标题",
  "published": "2026-09-01",
  "parent_id": "18342",
  "body": "Markdown",
  "lang": "zh-CN",
  "guest": {"name": "昵称", "email": "a@b.example", "url": "https://…", "notify": true, "website": "", "token": "浏览器的访客令牌"},
  "pow": {"challenge": "…", "salt": "…", "signature": "…", "number": 12345}
}
```

- `key` 是 iframe 文档内联的讨论页标识。服务端不盲信它：以 `k:` 开头时按 `page-key` 的规则校验；否则必须等于按 `url` 重新算出的标识（[widget.md §5](widget.md#5-讨论页标识)）。`url` 的 origin 必须是站点的已验证 origin，否则 `400 origin_not_allowed`。
- 讨论页不存在时，用这次的 `url`、`title`、`published` 创建它，之后不再改写（[data-model.md §3.4](data-model.md#34-讨论页与评论)）。`published` 晚于现在 24 小时以上时按没给处理（[sites.md §4](sites.md#4-站点设置与讨论页管理)）。
- 没有站点会话时必须带 `guest` 和 `pow`；`guest.website` 是对人不可见的蜜罐字段，有内容就 `400`。`lang` 用于访客的确认信和回复通知。`guest.token` 是组件存在 iframe 里的访客令牌（43 个 base64url 字符）：`notify` 为 `true` 时，这个邮箱在这个令牌上确认过才直接开通知，否则发确认信（[moderation.md §6.1](moderation.md#61-读者)）；不带或格式不对就当作没确认过的浏览器。
- 返回 `201` 和评论对象，带 `status`（可能是 `pending`）。同一作者 10 分钟内发出相同正文返回 `409 conflict`。发布规则和顺序见 [moderation.md §2](moderation.md#2-发布规则)。

普通读者不需要带身份读取评论：自己待审核的评论只在读者中心显示，不在组件里显示（[widget.md §9](widget.md#9-界面)）。

---

## 6. 读者中心

控制台会话，页面在 `comments.kite.plus/me`（§11）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/me` | 资料，见下；未登录 `401` |
| PATCH | `/api/v1/me` | 改资料：`display_name`（1–40 字，去掉零宽和双向控制字符）、`avatar_kind`（`generated`、`idp`）、`homepage_url`（http/https，空串清除）、`locale`（`zh-CN`、`en`）、`notify_replies`、`on_account_delete`（`remove`、`anonymize`），返回新的资料 |
| DELETE | `/api/v1/me` | 删除本服务里的数据，`{comments: "remove" | "anonymize"}`，`204` 并清掉控制台 Cookie。读者是某个站点唯一的所有者时返回 `409 sole_owner`。Kite Plus 账号本身要在身份服务删除（[login.md §7](login.md#7-账号删除与停用)） |
| GET | `/api/v1/me/avatar` | 读者自己的头像图片（选了身份服务头像时） |
| GET | `/api/v1/me/comments` | 我在所有站点的评论，参数 `site`（站点 ID）、`cursor`、`limit`；含待审核和被隐藏的，并标明状态；不含已删除的 |
| DELETE | `/api/v1/me/comments/{id}` | 删除我的一条评论，`204`，和组件里的删除相同（→ `deleted`）。不看站点状态：站点暂停、待审或在恢复窗口里也能删，读者不用回到一个可能已经打不开的博客。不是自己的或已删除的 `404` |
| GET | `/api/v1/me/notifications` | 回复通知，`{data, next_cursor, unread}` |
| POST | `/api/v1/me/notifications/read` | 标为已读，`{ids: ["…"]}`（最多 500 个）或 `{all: true}`，`204` |
| GET | `/api/v1/me/mutes` | 关闭了通知的讨论页 ID：`{data: ["77", …]}` |
| PUT、DELETE | `/api/v1/me/mutes/{thread}` | 关闭、恢复某个讨论页的通知，`204` |
| GET | `/api/v1/me/site-sessions` | 已登录的站点，见下 |
| DELETE | `/api/v1/me/site-sessions/{id}` | 退出某个站点，`204` |
| DELETE | `/api/v1/me/site-sessions` | 退出所有站点，`{ended: n}` |
| GET | `/api/v1/me/claims/suggestion` | 用账号自己已验证的邮箱留下的访客评论：`{count, sites: [{id, name, count}]}`（[sites.md §6](sites.md#6-认领历史评论) 第 6 步） |
| POST | `/api/v1/me/claims` | 发起认领 `{email}`，总是返回 `202 {id, verified}`；`verified` 为 `true` 表示这是账号自己已验证的邮箱，不发邮件，直接可以 `verify` |
| POST | `/api/v1/me/claims/{id}/verify` | 提交邮件链接里的令牌 `{token}`（自己邮箱的认领传空串）。只有发起这次认领的账号能验证，别人或令牌不对一律 `404`；通过后返回 `{count, sites}` |
| POST | `/api/v1/me/claims/{id}/confirm` | 确认认领，`{claimed: n}` |
| GET | `/api/v1/me/export` | 导出我的资料和全部评论，附件形式的 JSON：`{format: "kite-comments-reader", version: 1, exported_at, profile, comments}` |

资料：

```json
{
  "id": "账号 ID", "display_name": "某读者", "avatar": "/api/v1/me/avatar", "avatar_kind": "idp", "has_idp_avatar": true,
  "homepage_url": "", "locale": "zh-CN", "email": "a@b.example", "notify_replies": true, "on_account_delete": "remove",
  "is_platform_admin": false, "suspended": false, "created_at": "…", "account_url": "https://id.kite.plus/ui/console/users/me",
  "sites": [{"id": "站点 ID", "name": "某某的博客", "role": "owner", "status": "active"}],
  "csrf_token": "…"
}
```

`email` 是身份服务标记为已验证的邮箱，只给读者本人看。`account_url` 是身份服务的账号页，改邮箱、密码和删除 Kite Plus 账号都在那里。我的评论和通知里的评论：

```json
{"id", "root_id", "parent_id", "site": {"id", "name"}, "thread": {"id", "url", "title"}, "url": "文章地址#kc-评论 ID",
 "status", "body_html", "author": {"kind", "name"}, "created_at", "edited_at"}
```

通知是 `{"id", "kind": "reply", "created_at", "read_at", "comment": 上面的评论}`，只列仍是已发布状态的回复。已登录的站点：`{"id", "site": {"id", "name", "origin"}, "client": "Chrome · macOS", "created_via": "popup" | "redirect", "created_at", "last_used_at", "expires_at"}`。

认领邮件里的链接指向读者中心的页面 `/me/claims/{id}?t=<令牌>`：打开页面不改变任何状态，页面在读者确认后才调用 `verify`；没登录或登录的不是发起认领的账号（`verify` 返回 `404`），页面只提示「这封邮件不是发给你的，忽略即可」。邮件网关自动打开链接，什么也不会发生。

---

## 7. 站点控制台

控制台会话，页面在 `comments.kite.plus/console`（§11）。`{site}` 是站点 ID；不是这个站点的成员一律 `404`。除特别说明外，所有者和协管员都能调用；只限所有者的，协管员调用返回 `403`。

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/api/v1/sites` | 登录即可 | 我管理的站点：`{data: [站点…]}`，每个另带 `pending_count`、`reported_count` |
| POST | `/api/v1/sites` | 登录即可 | 新建站点 `{name, origin, aliases?, guest_policy}`：`guest_policy` 必填（`off`、`review`、`publish`，[sites.md §4](sites.md#4-站点设置与讨论页管理)）；origin 只接受 https（开发服务器另见 [sites.md §2](sites.md#2-注册与验证)）；别名最多 5 个；origin 已被别的站点**验证** `409 conflict`（只登记没验证的不算）；每个账号每天 5 个，超出 `429`；账号已有 10 个未删除的站点 `403 forbidden` |
| GET | `/api/v1/sites/{site}` | | 站点 |
| PATCH | `/api/v1/sites/{site}` | 所有者 | 设置：`name`、`guest_policy`、`member_policy`、`sort_order`、`close_after_days`（1–3650）或 `clear_close_after_days: true`、`query_keys`（最多 5 个）、`allow_localhost`、`owner_notify`、`blocked_words`（最多 200 个，每个最多 100 字，去重） |
| DELETE | `/api/v1/sites/{site}` | 所有者 | 删除，进入恢复窗口，`204`（[sites.md §4](sites.md#4-站点设置与讨论页管理)） |
| POST | `/api/v1/sites/{site}/restore` | 所有者 | 在恢复窗口内找回站点；被平台暂停的站点恢复成 `suspended`，暂停说明保留 |
| POST | `/api/v1/sites/{site}/origins` | 所有者 | 加别名 `{origin}`，返回站点；已被别的站点验证 `409 conflict` |
| DELETE | `/api/v1/sites/{site}/origins/{origin}` | 所有者 | 删别名；`{origin}` 要整段百分号编码，如 `https%3A%2F%2Fwww.example.com`；主 origin 不能删 |
| POST | `/api/v1/sites/{site}/verification` | 所有者 | 生成新的验证令牌，只在这次响应里返回明文：`{token, expires_at, dns: {name, value}, meta, file: {path, content}}` |
| POST | `/api/v1/sites/{site}/verification/check` | 所有者 | 验证全部未验证的 origin：`{results: [{origin, ok, method, detail}], site}`；一个都没通过时 `422`，响应另带 `error`（`verification_failed`）。每站点每小时 10 次。别的站点已经验证了的 origin 不通过，`detail` 说明；`detail` 不含解析出的地址（[sites.md §2](sites.md#2-注册与验证)） |
| GET | `/api/v1/sites/{site}/comments` | | 审核队列：`status`（`pending`、`published`、`spam`、`hidden`、`deleted`、`reported`，省略为除已删除外的全部）、`thread`（讨论页 ID）、`cursor`；新的在前。评论对象另带 `status`、`thread: {id, url, title}`、`url`（固定链接），访客评论带 `guest_email` |
| POST | `/api/v1/sites/{site}/comments/moderation` | | 批量处置 `{ids, action}`（最多 200 条），返回 `{changed}`，不适用的跳过 |
| GET | `/api/v1/sites/{site}/threads` | | 讨论页列表，最近有评论的在前：`{id, key, url, title, published_at, locked, comment_count, last_comment_at, created_at}` |
| PATCH | `/api/v1/sites/{site}/threads/{thread}` | | 锁定、解锁；修改标题、地址和发布时间：`{title?, url?, published_at?, clear_published_at?, locked?}`，地址必须属于站点；发布时间晚于现在 24 小时以上 `400` |
| POST | `/api/v1/sites/{site}/threads/{thread}/move` | 所有者 | 迁移到另一个标识 `{to_key}` 或 `{to_url}`，目标已存在则合并；返回目标讨论页。可在审核记录里撤销 |
| GET | `/api/v1/sites/{site}/bans` | | 有效的封禁：`{id, kind: "user" | "email" | "ip", author_id, name, reason, created_at, expires_at}` |
| POST | `/api/v1/sites/{site}/bans` | | 封禁 `{kind, comment_id? , author_id?, email?, reason?, days?}`：`user` 按 `comment_id` 的作者或 `author_id`，记下这位读者的身份，删除账号后照样生效（[data-model.md §3.5](data-model.md#35-审核举报与封禁)）；`email` 按 `comment_id` 的访客邮箱或 `email`；`ip` 按 `comment_id` 存下的 IP HMAC（[moderation.md §4.1](moderation.md#41-站点管理者)）。`days` 省略为不过期 |
| DELETE | `/api/v1/sites/{site}/bans/{id}` | | 解除 |
| GET | `/api/v1/sites/{site}/reports` | | 举报：参数 `status`（`open`、`resolved`、`dismissed`）；`{id, reason, note, status, created_at, resolved_at, site, comment}` |
| PATCH | `/api/v1/sites/{site}/reports/{id}` | | 处理 `{status: "resolved" | "dismissed", action?: "hide" | "spam"}`，`204` |
| GET | `/api/v1/sites/{site}/events` | | 审核记录：`{id, comment_id, actor, action, detail, created_at}` |
| POST | `/api/v1/sites/{site}/events/{id}/undo` | 所有者 | 撤销一次讨论页迁移，`204`；已撤销或不是迁移 `400` |
| POST | `/api/v1/sites/{site}/imports` | 所有者 | 上传（multipart：`format` 是 `json`、`waline`、`twikoo`、`artrans`、`wxr`，`file` 最大 50 MB、最多 10 万条评论），解析后返回导入对象和预览。只有已批准或已上线的站点能导入，否则 `403 site_inactive`；每站点每天 5 次，超出 `429`；文件超过条数上限，或站点导入的评论会超过 20 万条，`422 import_invalid`，`message` 说明。只保存解析结果，原始文件不保存（[sites.md §7](sites.md#7-导入)） |
| GET | `/api/v1/sites/{site}/imports/{id}` | 所有者 | 导入对象：`{id, format, status, mapping, stats, error, created_at, finished_at}`；`stats.preview` 是预览（[sites.md §7](sites.md#7-导入)），完成后另有 `inserted`、`skipped`、`failed`、`unmatched` |
| PUT | `/api/v1/sites/{site}/imports/{id}/mapping` | 所有者 | 地址改写规则 `{origin?, rewrites?: [{from, to}], wordpress_keys?: "url" | "post_id"}`，重新预览 |
| POST | `/api/v1/sites/{site}/imports/{id}/run` | 所有者 | 执行，`202`；worker 在后台导入，轮询 `GET` 看结果 |
| GET | `/api/v1/sites/{site}/export` | 所有者 | 附件：`format=json`（[export-format.md](../export-format.md)）、`artrans` 或 `wxr`（[sites.md §8](sites.md#8-导出)）；作者删除的评论只有占位，正文为空 |

站点对象：

```json
{
  "id": "站点 ID", "name": "某某的博客", "status": "active", "status_note": "", "role": "owner",
  "guest_policy": "off", "member_policy": "publish", "sort_order": "oldest", "close_after_days": null,
  "query_keys": [], "allow_localhost": true, "owner_notify": "instant", "blocked_words": [],
  "origins": [{"origin": "https://blog.example.com", "primary": true, "verified_at": "…", "last_checked_at": "…", "failing_since": null}],
  "created_at": "…", "verified_at": "…", "approved_at": null, "deleted_at": null,
  "embed": {"snippet": "<kite-comments site=\"…\"></kite-comments>\n<script src=\"https://comments.kite.plus/embed.js\" crossorigin=\"anonymous\" async></script>"}
}
```

---

## 8. 平台管理

只对 `is_platform_admin` 开放（其余登录读者 `403`），前缀 `/api/v1/admin`：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/admin/sites` | 全部站点，参数 `status`、`cursor`；内测期看 `status=pending_review` |
| POST | `/admin/sites/{site}/approve` | 批准 |
| POST | `/admin/sites/{site}/suspend` | 暂停，可带 `{note}`，记 `platform_suspend_site` |
| POST | `/admin/sites/{site}/unsuspend` | 解除暂停 |
| GET | `/admin/reports` | 平台队列：理由为「违法」「隐私」的举报 |
| PATCH | `/admin/reports/{id}` | 同站点的举报处理，处置记为 `platform_hide`、`platform_spam` |
| POST | `/admin/comments/{id}/hide` | 平台隐藏一条评论（`platform_hide`） |
| POST | `/admin/users/{id}/suspend`、`/unsuspend` | 暂停、恢复账号，`{id}` 是账号 ID |
| GET、POST、DELETE | `/admin/bans[/{id}]` | 全网封禁；`POST` 同站点封禁，另可用 `{kind: "user", user_id}` 按账号 ID |
| GET、POST、DELETE | `/admin/spam-domains[/{domain}]` | 垃圾域名清单（[moderation.md §3.1](moderation.md#31-通用规则)），`POST {domain}` |

服务器操作者另有命令行：`comments users promote-admin <邮箱|账号 ID>`、`demote-admin`、`comments sites list [--status]`、`approve`、`suspend [--note]`、`unsuspend`、`comments migrate up`。

---

## 9. 回调与其他

邮件里的链接一律只打开页面，由页面上的按钮用 POST 完成操作，邮件网关自动打开链接不会改变任何状态。RFC 8058 的一键退订本身就是 POST，不受影响。这些页面都不允许被嵌入。

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/hooks/idp` | 身份服务的事件推送，校验签名（不对 `401`），读取用户当前状态后才处理，推送的事件 ID 不记录（[login.md §7](login.md#7-账号删除与停用)）；身份服务读不到或删除未得到确认时 `503`，留给对账 |
| POST | `/auth/backchannel-logout` | 身份服务的 back-channel logout，表单字段 `logout_token`，校验后按 `sid`（没有时按 `sub`）删除会话（[login.md §6](login.md#6-退出)）；无效返回 `400 {"error": "invalid_request"}` |
| GET、POST | `/unsubscribe?t=` | 退订页；POST 退订，支持 RFC 8058 一键退订。令牌是无状态的签名，旧邮件里的链接一直有效 |
| GET、POST | `/mute?t=` | 登录读者关闭某个讨论页通知的页面，令牌来自通知邮件 |
| GET、POST | `/guest-email/confirm?t=` | 访客邮箱确认页；POST 才确认 |
| GET | `/healthz`、`/readyz` | 存活与就绪检查 |

---

## 10. 错误码

| HTTP | `code` | 含义 |
|---|---|---|
| 400 | `invalid_request` | 参数缺失或格式不对，或不适用于当前状态（比如回复一条未发布的评论、置顶一条回复） |
| 400 | `comment_too_long`、`too_many_links` | 超出 [moderation.md §3.4](moderation.md#34-限额-待定) 的限额 |
| 400 | `origin_not_allowed` | 页面地址不属于这个站点 |
| 400 | `pow_invalid` | 工作量证明缺失、无效或已用过 |
| 401 | `unauthorized` | 没有会话或会话已失效 |
| 403 | `forbidden` | 没有权限；封禁也返回这个，不说明原因；登录入口和组件写接口的来源校验不通过、缺少 CSRF 令牌也是这个 |
| 403 | `site_inactive` | 站点未验证、待批准、被暂停或已删除 |
| 403 | `thread_locked` | 讨论页已锁定或已自动关闭 |
| 403 | `edit_window_closed` | 超过编辑窗口 |
| 403 | `guest_not_allowed` | 站点不接受访客评论 |
| 404 | `not_found` | 资源不存在，或无权知道它存在 |
| 409 | `conflict` | 比如 origin 已被别的站点验证、重复的 challenge、10 分钟内的重复评论 |
| 409 | `sole_owner` | 删除数据前要先处理自己唯一拥有的站点 |
| 410 | `login_expired` | 登录交易过期或已兑换 |
| 415 | `invalid_request` | 请求体不是 JSON |
| 422 | `verification_failed` | 站点验证没通过，结果里说明查到了什么 |
| 422 | `import_invalid` | 导入文件无法解析，或超出条数、站点配额 |
| 429 | `rate_limited` | 超出限流，带 `Retry-After` |
| 500 | `internal` | 服务端错误 |
| 503 | `unavailable` | 身份服务连不上，或组件资源没有构建进二进制 |

---

## 11. 读者中心和站点控制台的前端约定

读者中心和站点控制台是 `web/console/` 下的 React 单页应用（[architecture.md §7](architecture.md#7-技术选型)），和接口同源，构建产物打进 Go 二进制：

- **构建**：Vite 的 `base` 是 `/static/console/`，输出到 `web/console/dist`（`index.html` 加 `assets/` 下带哈希的文件）。`web/console/dist/.gitkeep` 是提交进仓库的占位，没有构建时 Go 也能编译；Vite 清空 `dist` 后由 `make web` 放回。
- **服务端怎么交付**：`GET /me`、`/me/*`、`/console`、`/console/*` 都返回 `web/console/dist/index.html`（`Cache-Control: no-cache`），路由由前端处理；`/static/console/<路径>` 返回 `dist/<路径>`，`assets/` 下长期缓存，其余缓存 5 分钟；但 `.html` 文件和目录一律 `404`，页面只经上面的路由、带着下面的 CSP 交付，免得 `/static/console/index.html` 绕开 `frame-ancestors 'none'` 被嵌进别的网站。没有构建时这些页面返回 `503` 和一句说明。
- **CSP**：`default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`。不能有内联脚本、第三方脚本、网络字体和外链图片；组件库的内联样式可以。
- **登录**：`GET /api/v1/me` 返回 `401` 时，整页跳到 `/auth/login?return=<当前路径>`；登录后回到那个路径。退出用 `POST /auth/logout`。写请求带 `X-CSRF-Token`（`/api/v1/me` 的 `csrf_token`）。
- **服务端会链接到的页面**，前端要有：`/me`（读者中心首页，组件菜单里的「读者中心」）、`/me/claims/{id}?t=<令牌>`（认领邮件，§6）、`/console/sites/{site}`（站点）、`/console/sites/{site}/comments`（审核队列，站长通知邮件和组件里的「在控制台管理」）、`/console/sites/{site}/reports`（举报，紧急举报邮件）。
- **语言**：界面按读者资料的 `locale`，接口的 `message` 按 `Accept-Language`。
