# 接口

> 状态：规划草案，经独立审阅修订，C2 开工前可调整 · 最近更新：2026-09-26
> 登录流程见 [login.md](login.md)，表结构见 [data-model.md](data-model.md)。本文只列接口和约定，业务规则链接到各自的文档。

---

## 1. 约定

沿用 Explore 的接口约定（[Explore api.md §1](https://github.com/kite-plus/explore/blob/main/docs/design/api.md)），只在分页大小上不同：

- 前缀 `/api/v1`，不兼容的改动放 `/api/v2`。JSON 字段用 snake_case。
- 时间是 UTC 的 RFC 3339。`bigint` 的 ID（评论、讨论页）以字符串传递；站点、会话用 `uuid`；读者在组件接口里用按站点区分的作者 ID（[data-model.md §3.1](data-model.md#31-账号与身份)）。
- 错误统一为 `{"error": {"code": "…", "message": "…"}}`，`message` 按 `Accept-Language` 返回中文或英文，错误码见 §10。
- 列表返回 `{"data": […], "next_cursor": "…" | null}`，游标是 `"unixnanos:id"` 的 base64url。评论列表默认 20 条，最多 50 条；其余列表默认 30 条，最多 100 条。
- 匿名读取返回 `Cache-Control: public, max-age=30` 和弱 ETag；带身份的响应一律 `private, no-store`。
- **没有带凭据的 CORS**。所有带身份的调用方都和接口同源：组件 iframe、弹窗页、读者中心、控制台都在 `comments.kite.plus` 上。唯一的跨源读取是宿主页加载 `embed.js`（§3）。

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
  "replies": []
}
```

- `author.id` 是这位读者在**这个站点**的作者 ID，换一个站点就不同。`author.kind` 为 `guest`、`external` 时没有 `id`；`former_user` 时 `name` 是「已注销用户」。邮箱永远不出现在组件接口里。
- `status` 只在管理者看到的响应里出现；公开响应只含 `published` 的评论，所以省略这个字段。
- `replies` 只在顶层评论里出现，**带前 3 条回复**，其余用 §3 的回复接口分页取。

---

## 2. 鉴权

| 凭据 | 用在哪里 | 怎么带 | 防跨站请求 |
|---|---|---|---|
| 无 | 匿名读取 | 不带：`embed.js` 以 `crossorigin="anonymous"` 加载，组件的读取请求用 `credentials: "omit"`；iframe 文档和匿名读取路由的 Cookie 请求头在反向代理处删掉 | 不需要 |
| 站点会话 | 组件里的写操作、管理者读取和「我是谁」 | 分区 Cookie `__Host-comments_site_<站点 ID>`；浏览器不支持时退回 `Authorization: Bearer <令牌>`，两者是同一个令牌（[login.md §5](login.md#5-站点会话)） | 宿主页和组件共享 Cookie 分区，所以组件接口要求 `Sec-Fetch-Site: same-origin`，没有这个头时要求 `Origin` 为 `https://comments.kite.plus`，两者都没有就拒绝 |
| 控制台会话 | 读者中心、站点控制台 | Cookie `__Host-comments_session`：`HttpOnly`、`Secure`、`SameSite=Lax`（[login.md §3](login.md#3-三种会话)） | 非 GET 请求带 `X-CSRF-Token`，算法同 Explore |
| 站点 API 令牌（C4） | Kite 后台等服务端集成 | `Authorization: Bearer kcs_…` | 只从服务端调用 |
| 签名 | 身份服务的事件推送、back-channel logout | 签名请求头、Logout Token（[login.md §6](login.md#6-退出)、[§7](login.md#7-账号删除与停用)） | —— |

- 站点会话只对签发它的站点有效：组件接口都要带 `site`，服务端按它取对应的 Cookie，并核对会话的 `site_id`，不一致按未登录处理。
- 登录入口另有自己的校验，只接受 `comments.kite.plus` 自己页面发出的请求（§4）。

---

## 3. 组件读取（匿名）

这些路由在反向代理处删掉 Cookie 请求头，服务端收不到任何会话（[widget.md §4](widget.md#4-iframe-地址)）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/embed.js` | 宿主页加载器（[widget.md §3](widget.md#3-结构)），带 `Access-Control-Allow-Origin: *` |
| GET | `/embed/v1.<次版本>.<补丁>.js` | 固定版本的加载器，内容不可变，同样允许跨源读取，可以配合 SRI |
| GET | `/embed/v1/thread` | iframe 文档，参数和响应头见 [widget.md §4](widget.md#4-iframe-地址) |
| GET | `/api/v1/threads/comments` | 参数 `site`、`key`（讨论页标识）、`sort`、`cursor`；或用 `around=<评论 ID>` 取包含这条评论的那一页。返回已发布的顶层评论，各带前几条回复（§1），外加 `count` 和 `locked` |
| GET | `/api/v1/comments/{id}/replies` | 某条顶层评论的其余回复，按时间正序分页 |
| GET | `/avatars/{作者 ID}` | 缓存的头像，带 `?v=` 时长期缓存 |
| GET | `/avatars/generated/{种子}.svg` | 生成的头像 |

讨论页不存在时返回空列表和 `count: 0`，不建记录（[data-model.md §1](data-model.md#1-数据分类)）。

---

## 4. 组件登录

流程和每一步的校验见 [login.md §4](login.md#4-组件登录协议)。「只接受本站页面」指：`Origin` 必须是 `https://comments.kite.plus`，有 `Sec-Fetch-Site` 时必须是 `same-origin`。

| 方法 | 路径 | 调用方 | 说明 |
|---|---|---|---|
| GET | `/auth/popup` | 组件用 `window.open` 打开 | 弹窗页：与打开它的组件握手 |
| POST | `/auth/transactions` | 弹窗页用 `fetch` 调用 | `{site, challenge}` → 建登录交易，返回身份服务的授权地址。只接受本站页面；另要求 `Sec-Fetch-Mode` 不是 `navigate`、`Content-Type` 为 JSON；从不返回跳转 |
| POST | `/auth/redirect` | 组件提交的 `target="_top"` 表单 | 整页跳转入口，字段 `site`、`challenge`、`return`。只接受本站页面；`return` 必须属于站点的已验证 origin；建交易后跳到身份服务 |
| GET | `/auth/callback` | 身份服务回跳 | 校验 `state` 和 binder，完成交易；弹窗模式显示「登录成功」并尝试关闭，整页跳转模式显示确认页，`console` 模式建立控制台会话后跳回 |
| POST | `/auth/confirm` | 确认页的表单 | 字段是交易 ID。只接受本站页面，binder 要和交易一致，交易须为整页跳转模式且已 `authenticated`；通过后跳回 `return#kc-login` |
| POST | `/api/v1/widget/sessions` | 组件 | 兑换：`{site, verifier}`。交易不存在或还没完成返回 `202 {"status": "pending"}`；完成则设置这个站点的分区 Cookie 并返回 `201 {"token", "expires_at", "viewer"}`，组件据此选载体；过期或已兑换返回 `410` |
| GET | `/api/v1/widget/viewer` | 组件 | 参数 `site`，返回当前读者在本站的作者 ID、显示名、角色和通知设置；未登录返回 `{"viewer": null}` |
| DELETE | `/api/v1/widget/session` | 组件 | 参数 `site`，退出此站点 |
| GET | `/auth/login` | 读者中心、控制台 | 第一方 OIDC 登录（交易模式 `console`），参数 `return`（本站相对路径） |
| POST | `/auth/logout` | 读者中心、控制台 | 删除控制台会话 |

---

## 5. 组件写入与管理者读取

都要求站点会话（访客发表除外）和 `Sec-Fetch-Site: same-origin`。

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/comments` | 发表。`{site, key, url, title, published?, parent_id?, body}`；访客另带 `guest: {name, email, url?, notify}` 和 `pow: {id, nonce}`。讨论页不存在时，用这次的 `url`、`title`、`published` 创建它，之后不再改写（[data-model.md §3.4](data-model.md#34-讨论页与评论)）。返回评论对象，状态可能是待审。发布规则见 [moderation.md §2](moderation.md#2-发布规则) |
| PATCH | `/api/v1/comments/{id}` | 作者在编辑窗口内修改 `{body}` |
| DELETE | `/api/v1/comments/{id}` | 作者删除 |
| POST | `/api/v1/comments/{id}/reports` | 举报 `{reason, note?}` |
| GET | `/api/v1/widget/threads/comments` | 只对本站管理者开放，参数同 §3 的讨论页评论接口，另可按 `status` 筛选；返回包括待审、隐藏、垃圾在内的评论，响应 `private, no-store`。组件在管理者登录后改用它 |
| POST | `/api/v1/comments/{id}/moderation` | 管理者处置 `{action}`：`approve`、`hide`、`spam`、`restore`、`pin`、`unpin`。封禁只在控制台里做（§7） |
| PUT | `/api/v1/threads/lock` | 管理者锁定或解锁 `{site, key, locked}` |
| POST | `/api/v1/preview` | Markdown 预览 `{body}` → `{html}`，和发表用同一个渲染器 |
| GET | `/api/v1/pow/challenge` | 参数 `site`，访客的工作量证明题目（[moderation.md §3.2](moderation.md#32-访客)） |

普通读者不需要带身份读取评论：自己待审核的评论只在读者中心显示，不在组件里显示（[widget.md §9](widget.md#9-界面)）。

---

## 6. 读者中心

控制台会话，页面在 `comments.kite.plus/me`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET、PATCH | `/api/v1/me` | 资料：显示名、头像来源、个人主页、语言、回复通知、删号时评论的处理方式 |
| DELETE | `/api/v1/me` | 删除本服务里的数据，`{comments: "remove" | "anonymize"}`。读者是某个站点唯一的所有者时返回 `409 sole_owner`。Kite Plus 账号本身要在 `id.kite.plus` 删除（[login.md §7](login.md#7-账号删除与停用)） |
| GET | `/api/v1/me/comments` | 我在所有站点的评论，可按站点筛选；含待审核和被隐藏的，并标明状态 |
| GET | `/api/v1/me/notifications` | 回复通知 |
| POST | `/api/v1/me/notifications/read` | 标为已读，`{ids}` 或 `{all: true}` |
| PUT、DELETE | `/api/v1/me/mutes/{thread}` | 关闭、恢复某个讨论页的通知 |
| GET | `/api/v1/me/site-sessions` | 已登录的站点：站点名、浏览器、最近使用时间 |
| DELETE | `/api/v1/me/site-sessions/{id}` | 退出某个站点 |
| DELETE | `/api/v1/me/site-sessions` | 退出所有站点 |
| POST | `/api/v1/me/claims` | 发起认领 `{email}`，总是返回 `202`（[sites.md §6](sites.md#6-认领历史评论)） |
| POST | `/api/v1/me/claims/{id}/verify` | 提交邮件链接里的令牌 `{token}`。只有发起这次认领的账号能验证；通过后返回可认领的数量和站点 |
| POST | `/api/v1/me/claims/{id}/confirm` | 确认认领 |
| GET | `/api/v1/me/export` | 导出我的资料和全部评论 |

认领邮件里的链接指向读者中心的页面 `/me/claims/{id}?t=<令牌>`：打开页面不改变任何状态，页面在读者确认后才调用 `verify`；没登录或登录的不是发起认领的账号，页面只提示「这封邮件不是发给你的，忽略即可」。邮件网关自动打开链接，什么也不会发生。

---

## 7. 站点控制台

控制台会话，页面在 `comments.kite.plus/console`。`{site}` 是站点 ID；除特别说明外，所有者和协管员都能调用。

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET、POST | `/api/v1/sites` | 登录即可 | 我管理的站点；新建站点 |
| GET、PATCH | `/api/v1/sites/{site}` | 所有者 | 设置 |
| DELETE | `/api/v1/sites/{site}` | 所有者 | 删除，进入恢复窗口（[sites.md §4](sites.md#4-站点设置与讨论页管理)） |
| POST | `/api/v1/sites/{site}/restore` | 所有者 | 在恢复窗口内找回站点 |
| POST、DELETE | `/api/v1/sites/{site}/origins[/{origin}]` | 所有者 | 增删 origin，只接受 https |
| POST | `/api/v1/sites/{site}/verification` | 所有者 | 生成新的验证令牌，只在这次响应里返回明文 |
| POST | `/api/v1/sites/{site}/verification/check` | 所有者 | 验证全部未验证的 origin，返回每个 origin 的结果 |
| GET | `/api/v1/sites/{site}/comments` | | 审核队列：`status`、`thread`、`cursor`；这里的评论对象带访客邮箱 |
| POST | `/api/v1/sites/{site}/comments/moderation` | | 批量处置 `{ids, action}` |
| GET | `/api/v1/sites/{site}/threads` | | 讨论页列表 |
| PATCH | `/api/v1/sites/{site}/threads/{thread}` | | 锁定、解锁；修改标题、地址和发布时间 |
| POST | `/api/v1/sites/{site}/threads/{thread}/move` | 所有者 | 迁移到另一个标识 `{to_key}`，目标已存在则合并；可在审核记录里撤销 |
| GET、POST、DELETE | `/api/v1/sites/{site}/bans[/{id}]` | | 封禁 |
| GET、PATCH | `/api/v1/sites/{site}/reports[/{id}]` | | 举报与处理 |
| GET | `/api/v1/sites/{site}/events` | | 审核记录 |
| POST | `/api/v1/sites/{site}/imports` | 所有者 | 上传（multipart），返回导入 ID |
| GET | `/api/v1/sites/{site}/imports/{id}` | 所有者 | 预览和进度 |
| PUT | `/api/v1/sites/{site}/imports/{id}/mapping` | 所有者 | 地址改写规则 |
| POST | `/api/v1/sites/{site}/imports/{id}/run` | 所有者 | 执行 |
| GET | `/api/v1/sites/{site}/export` | 所有者 | `format=json`、`artrans`，C3 起加 `wxr`（[sites.md §8](sites.md#8-导出)） |

---

## 8. 平台管理

只对 `is_platform_admin` 开放，前缀 `/api/v1/admin`：站点的批准与暂停、平台举报队列、账号暂停与全网封禁、垃圾域名清单。服务器操作者另有命令行：`comments users promote-admin`、`comments sites suspend`、`comments migrate up`。

---

## 9. 回调与其他

邮件里的链接一律只打开页面，由页面上的按钮用 POST 完成操作，邮件网关自动打开链接不会改变任何状态。RFC 8058 的一键退订本身就是 POST，不受影响。

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/hooks/idp` | 身份服务的事件推送，校验签名，按事件 ID 去重 |
| POST | `/auth/backchannel-logout` | 身份服务的 back-channel logout，校验 Logout Token 后按 `sid` 删除会话（[login.md §6](login.md#6-退出)） |
| GET、POST | `/unsubscribe` | 退订页；POST 退订，支持 RFC 8058 一键退订 |
| GET、POST | `/mute` | 登录读者关闭某个讨论页通知的页面，令牌来自通知邮件 |
| GET、POST | `/guest-email/confirm` | 访客邮箱确认页；POST 才确认 |
| GET | `/healthz`、`/readyz` | 存活与就绪检查 |

---

## 10. 错误码

| HTTP | `code` | 含义 |
|---|---|---|
| 400 | `invalid_request` | 参数缺失或格式不对 |
| 400 | `comment_too_long`、`too_many_links` | 超出 [moderation.md §3.4](moderation.md#34-限额-待定) 的限额 |
| 400 | `origin_not_allowed` | 页面地址不属于这个站点 |
| 400 | `pow_invalid` | 工作量证明无效或已用过 |
| 401 | `unauthorized` | 没有会话或会话已失效 |
| 403 | `forbidden` | 没有权限；封禁也返回这个，不说明原因；登录入口的来源校验不通过也是这个 |
| 403 | `site_inactive` | 站点未验证、待批准、被暂停或已删除 |
| 403 | `thread_locked` | 讨论页已锁定或已自动关闭 |
| 403 | `edit_window_closed` | 超过编辑窗口 |
| 403 | `guest_not_allowed` | 站点不接受访客评论 |
| 404 | `not_found` | 资源不存在，或无权知道它存在 |
| 409 | `conflict` | 比如 origin 已属于别的站点 |
| 409 | `sole_owner` | 删除数据前要先处理自己唯一拥有的站点 |
| 410 | `login_expired` | 登录交易过期或已兑换 |
| 422 | `verification_failed` | 站点验证没通过，`message` 说明查到了什么 |
| 422 | `import_invalid` | 导入文件无法解析 |
| 429 | `rate_limited` | 超出限流，带 `Retry-After` |
| 500 | `internal` | 服务端错误 |
