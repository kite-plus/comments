# 登录与会话

> 状态：C2 已实现（代码在 `internal/api`、`internal/oidc`、`web/widget`）；本地 Chrome 和真实 ZITADEL 开发栈上的实测结论已回填，真机矩阵仍待 C0 · 最近更新：2026-10-11
> 跨项目的身份约定（OIDC、`(iss, sub)`、各应用独立会话）写在 [Explore identity-and-comments.md](https://github.com/kite-plus/explore/blob/main/docs/design/identity-and-comments.md)，本文不重复，只写评论服务自己的部分。

---

## 1. 目标与边界

**目标**：读者只需要一个 Kite Plus 账号。在任何接入的博客上点一次「登录」就能以这个身份评论；已经在身份服务登录过的，这一次点击不需要输入任何东西，弹窗一闪就关。之后这个博客会记住读者。

**不做「打开页面就自动认出读者」**，原因有两条：

1. **浏览器不允许**。组件运行在别人的域名下，对 `id.kite.plus` 来说是第三方环境。Safari（以及 iOS 上的所有浏览器和 App 内浏览器）默认拦截第三方 Cookie，Firefox 把它们按站点分区，Chrome 无痕模式也拦截（§10）。在这些环境里，组件读不到身份服务的登录状态。
2. **即使能做到也不该做**。在 Chrome 常规模式下，靠第三方 Cookie 确实能做到打开即识别；但这等于评论服务能在每个接入博客的每一次页面访问中认出读者，这就是跨站追踪。Kite Plus 的原则是读者不登录、不操作就不留痕（Explore：「匿名访问零 Cookie」）。

所以基线是**每个站点点一次**，而且在所有浏览器里行为一致。以后在 Chromium 上可以用 FedCM，由浏览器代为确认、自动登录（§9）。

**宿主页也不该知道谁在看**。组件在宿主页里的一切可观察行为（高度、消息、加载的请求）都不随读者身份变化（§4.3、[widget.md §9](widget.md#9-界面)）。

---

## 2. 身份服务

「Kite Plus 账号」由身份服务 `id.kite.plus` 提供。它是 Kite Plus 所有服务的基础设施，单独在 [identity 仓库](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md) 规划：选型、登录方式、部署、运维和升级时限都以那里为准，本文不重复。

评论服务依赖它的这几项，编号是 [identity architecture.md §1](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md#1-各服务对身份服务的要求) 里的要求：

| 要求 | 评论服务用来做什么 |
|---|---|
| R1、R2：issuer 是 `https://id.kite.plus`，各应用的 `sub` 一致 | 按 `(iss, sub)` 认人（[data-model.md §3.1](data-model.md#31-账号与身份)） |
| R3：Authorization Code + PKCE，第一方应用不弹授权确认 | §4 的弹窗和整页跳转登录。身份服务会话还在时，授权请求直接回跳，读者什么也看不到。**不用 `prompt=none`**：ZITADEL 的 login v2 不返回 `login_required`，而是显示登录页或错误页（identity 仓库 README，2026-10）；普通的授权请求在会话还在时本来就直接回跳。`prompt=select_account` 可用，「换一个账号」以后用它 |
| R4：ID Token 带 `sid`，支持 back-channel logout | §6 的退出 |
| R5：用户事件的推送和查询 | §7 的账号删除、停用和对账 |
| R7：登录保持时间足够长 | 读者重启浏览器后，点「登录」时弹窗仍然一闪而过（C0 的 T11） |

评论服务的 OIDC 客户端是机密客户端，生产和预发布各一个，登记回调地址和 back-channel logout 地址（§6）。

---

## 3. 三种会话

| 会话 | 谁签发 | 存在哪里 | 作用 | 时长 |
|---|---|---|---|---|
| 身份服务会话 | `id.kite.plus` | 身份服务的第一方 Cookie | 让登录弹窗不用输密码 | 见 identity 的 R7 |
| 控制台会话 | `comments.kite.plus` | 第一方 Cookie `__Host-comments_session` | 读者中心、站点控制台 | 30 天 |
| 站点会话 | `comments.kite.plus` | 按站点命名的分区 Cookie `__Host-comments_site_<站点 ID>`，不可用时退回 iframe 的 `localStorage`（§5） | 在**一个站点**上发表、编辑、删除评论 | 见 §5 |

另有一个不是会话的第一方 Cookie `__Host-comments_binder`，只在登录流程里用，把登录交易绑在发起它的浏览器上（§4.5）。Explore 有自己的会话，与这些互不影响。

**本地开发**：纯 http 的开发服务器上浏览器不接受 `__Host-` 前缀，三个 Cookie 改用 `comments_session`、`comments_binder`、`comments_site_<站点 ID>`，和 Explore 的做法一样。站点会话 Cookie 仍带 `Secure`、`SameSite=None`、`Partitioned`：Chrome 在 `http://localhost` 上接受，2026-10 实测分区键是 `http://blog-a.localhost` 这样的测试博客站点。

身份服务会话是「一处登录」的那一处：有了它，每个站点第一次点「登录」都是瞬间完成的。站点会话是「这个博客记住我」：它存在按宿主站点分区的 Cookie 或存储里，所以一个站点一份，互不通用。

---

## 4. 组件登录协议

### 4.1 参与方

| 代号 | 是什么 | origin |
|---|---|---|
| B | 宿主页（博客文章） | `https://blog.example.com`，不可信 |
| W | 组件 iframe | `https://comments.kite.plus` |
| P | W 打开的登录弹窗 | 开始和结束时是 `https://comments.kite.plus`，中途跳到身份服务 |
| I | 身份服务 | `https://id.kite.plus` |
| C | 评论服务的服务端 | `https://comments.kite.plus` |

- `verifier` 是 W 生成的 32 字节随机数，以 base64url 文本表示；`challenge = base64url(SHA-256(verifier 的 ASCII 文本))`，和 PKCE 的 S256 相同。verifier 只存在于 W 里，兑换时直接发给 C；此外只有 challenge 会离开 W。
- `binder` 是每个浏览器一个的随机数，存在 `comments.kite.plus` 的第一方 Cookie 里。登录交易记下它的哈希，回调和确认时都要对得上。
- **交易只能由 W 发起**：C 建交易的两个入口（§4.2 第 4 步、§4.4 第 2 步）都只接受 `comments.kite.plus` 自己页面发出的请求。这是整个协议的安全前提。

### 4.2 弹窗登录（默认）

```text
W   加载时预先生成 verifier 和 challenge
W → P   点击时同步调用 window.open("/auth/popup")
P → W   postMessage ready（目标 origin 限定为 comments.kite.plus）
W → P   postMessage begin {site, challenge}
P → C   fetch POST /auth/transactions → 建交易，返回授权地址
P → I   授权请求（state、nonce、PKCE）
I → P   已登录则立即回跳，否则显示登录或注册
P → C   GET /auth/callback → 校验，交易变为 authenticated
P → W   postMessage done（opener 还在的话），随后关闭弹窗
W → C   POST /api/v1/widget/sessions {site, verifier} → 签发站点会话
```

1. **预先生成**：W 加载时就生成 verifier 并算好 challenge。`crypto.subtle.digest` 是异步的；等到点击后再算，`window.open` 就不在用户手势的同步调用栈里了，Safari 会拦截弹窗。每用掉一对（发出 `begin`，或发起整页跳转）就立刻生成下一对，重试不会撞上同一个 challenge。
2. **打开弹窗**：点击时同步调用 `window.open("https://comments.kite.plus/auth/popup", "kite-comments-login", "popup,width=480,height=720")`。返回 `null` 有两种可能：弹窗被拦截；或者宿主页设了 `COOP: same-origin`，弹窗其实打开了，只是没有 opener。两种情况 W 都走 §4.4；P 发现自己没有 opener 时，提示读者关掉这个窗口、回到文章里继续。
3. **握手**：P 加载后向 `window.opener` 发 `ready`，目标 origin 写死为 `https://comments.kite.plus`。W 只接受 `event.source` 是自己打开的那个窗口、且 `event.origin` 为 `https://comments.kite.plus` 的 `ready`，然后回复 `begin {site, challenge}`。P 只接受来自 `window.opener`、origin 为 `https://comments.kite.plus` 的 `begin`。W 转入整页跳转后，再收到的 `ready` 一律忽略。P 页面不设置 COOP，否则握手之前 opener 就断了。
4. **建交易**：P 用 `fetch` 调用 `POST /auth/transactions {site, challenge}`。C 只接受自己页面发来的 JSON：`Origin` 必须是 `https://comments.kite.plus`；有 `Sec-Fetch-Site` 时必须是 `same-origin`；`Sec-Fetch-Mode` 不能是 `navigate`；`Content-Type` 必须是 `application/json`；响应只返回 JSON，从不跳转。所以跨站页面没法在读者的浏览器里用自己的 challenge 建交易。然后 C 检查站点是 `active`；浏览器里没有 binder Cookie 就新建一个，有就沿用并续期；生成随机的 `state`、`nonce` 和 PKCE，写入 `login_transactions`（模式 `popup`，有效期见 §4.5），返回授权地址，P 跳过去。
5. **身份服务**：读者已登录时 I 直接回跳，读者什么也看不到；否则显示登录或注册页。
6. **回调**：C 用 `state` 找到交易，要求请求带着的 binder 与交易记下的一致（防登录 CSRF）；用授权码加 PKCE 换令牌，校验 ID Token 的签名、`iss`、`aud`、有效期和 `nonce`；按 `(iss, sub)` 找到或创建用户；交易变为 `authenticated`，记下用户和 ID Token 里的 `sid`。**binder 不删除**：同一个浏览器里可能同时有别的登录在进行，比如另一个标签页正在等验证邮件。页面尝试向 opener 发 `done` 并关闭自己。
7. **兑换**：W 收到 `done`，或者在轮询中（§4.5），调用 `POST /api/v1/widget/sessions {site, verifier}`。C 按 `SHA-256(verifier)` 找交易：
   - 找不到，返回 `202`：弹窗可能还没建好交易。
   - 状态还是 `pending`，返回 `202`。
   - 弹窗模式已 `authenticated`、整页跳转模式已 `confirmed`，站点一致、未过期、未兑换：在一个事务里把交易标为已兑换，签发站点会话（带上交易的 `sid`），设置这个站点的分区 Cookie，同时在响应里返回令牌，W 据此选载体（§5）。
   - 已过期或已兑换，返回 `410`。

**登录链路上的页面可能切断弹窗和 W 的联系**。ZITADEL 自己的页面不带 COOP，但链路上的第三方页面可能带，比如用 Google 登录时 Google 的授权页。弹窗一旦经过带 COOP 的页面，opener 就被永久切断：P 发不出 `done`，W 看到的 `popup.closed` 还会误报为 `true`，其实窗口还开着。所以 `done` 只是加速，**完成登录只靠 W 的轮询兑换**；`popup.closed` 为 `true` 也不能当作读者取消了登录。

**弹窗页和回调页都不能带 COOP 头**。Go 这边没有 Helmet 那种默认加 `COOP: same-origin` 的中间件，但以后加安全头时要记得排除这两个页面。

**弹窗页的实现**：P 收到 `begin` 之前每 500 毫秒重发一次 `ready`，最多 7 次，防止 W 第一次没接住。2026-10 用本地 ZITADEL v4.19.4 实测：身份服务会话还在时，授权请求直接回跳，弹窗不到一秒自己关掉；ZITADEL 的登录页没有 COOP，`done` 能送到。

**同类做法 `[EV]`**：Disqus 也是用弹窗登录，会话放在自己域名的 Cookie 里，另在 iframe 自己的存储里放一份备用会话，读者回到页面时重新检查登录。Giscus 则让 GitHub 授权后跳回宿主页，会话出现在宿主页的地址里，存进宿主页的 `localStorage`，再传给 iframe，宿主页能拿到这个凭据，这正是我们要避免的。

**注册时去邮箱点了验证链接**：链接可能在新标签页打开，回调在那个标签页完成。只要是同一个浏览器（binder 在），交易照样完成，W 的轮询照样兑换成功。

### 4.3 为什么这样能防住宿主页

| 攻击 | 为什么不成立 |
|---|---|
| B 读取读者的令牌 | 令牌在 `HttpOnly` 的分区 Cookie 里，或在 W 的内存和存储里，跨源的宿主页都读不到 |
| B 自己打开 `/auth/popup` 发起登录 | P 只接受来自 `comments.kite.plus` 的 opener 的 `begin`；B 的 origin 不对，握手失败，P 不会建交易 |
| 任意网站用自己的 challenge 发起交易（调用建交易接口，或把读者导到整页跳转入口），读者完成登录后，攻击者在自己的服务器上兑换 | 两个入口都只接受 `comments.kite.plus` 自己页面发出的请求（`Origin`、`Sec-Fetch-Site`），跨站页面发起的一律拒绝。攻击者在自己的服务器上伪造请求头能建出交易，但交易绑定的是攻击者自己的 binder，读者浏览器里的回调对不上 |
| 伪造回调，把读者登录成攻击者的账号 | `state` 和 binder 绑定，不匹配就拒绝 |
| B 冒用读者身份调用写接口 | B 发出的请求即使带上了分区 Cookie，`Sec-Fetch-Site` 也是 `cross-site`，写接口一律拒绝（§5） |
| B 用透明层诱导读者点 W 的登录按钮 | 结果只是读者在 B 上登录了评论，令牌仍在 W 里，B 拿不到；删除等操作在 W 内二次确认 |
| 在未验证的站点嵌入组件，冒充别人的博客 | `frame-ancestors` 只列站点的已验证 origin，浏览器拒绝渲染（[widget.md §4](widget.md#4-iframe-地址)） |
| B 从 iframe 的高度判断读者是否登录，或认出某位读者 | 组件高度不随读者身份变化：登录前后输入框一样高，自己待审核的评论不在组件里显示（[widget.md §9](widget.md#9-界面)） |
| B 让读者加载 `embed.js` 和 iframe 时带上会话 Cookie，把浏览和身份关联起来 | `embed.js` 以 `crossorigin="anonymous"` 加载，不带 Cookie；iframe 文档和匿名读取接口的 Cookie 在反向代理处删掉（[widget.md §4](widget.md#4-iframe-地址)） |
| 伪造一个像登录弹窗的页面钓鱼 | 登录只在 `id.kite.plus` 的顶层页面进行，地址栏可见；推荐 Passkey，它绑定域名，钓不走 |

### 4.4 整页跳转降级

**什么时候用**：

- `window.open` 返回 `null`，或者 3 秒内没收到 P 的 `ready`。这时 W 显示「在当前页面登录」按钮，要读者再点一次。
- User-Agent 表明是微信、QQ、微博的 App 内浏览器时，W 的登录按钮直接走整页跳转，不尝试弹窗。这些环境里 `window.open` 要么没反应、要么在同一个页面里打开，`window.opener` 为空，`window.close()` 也不起作用；这是社区的长期反馈，没有官方文档，C0 实测确认。按 User-Agent 分支只用在这一处。

步骤：

1. W 把 verifier 存进自己的 `localStorage`，键名带站点 ID，并写明过期时间。然后在点击处理里提交一个 `method="post"`、`target="_top"` 的表单到 `https://comments.kite.plus/auth/redirect`，字段是 `site`、`challenge` 和 `return`（当前文章地址）。表单由 iframe 提交，所以请求的 `Origin` 是 `comments.kite.plus`；跨源 iframe 让顶层页面跳转需要读者和它交互过，这次点击本身就满足，Chrome、Firefox、Safari 都是如此。
2. C 像建交易接口一样确认请求来自自己的页面：`Origin` 必须是 `https://comments.kite.plus`，有 `Sec-Fetch-Site` 时必须是 `same-origin`（这是一次导航，所以不查 `Sec-Fetch-Mode`）。**W 的文档不能用 `Referrer-Policy: no-referrer`**：在这个策略下浏览器给表单导航发 `Origin: null`，入口只能拒绝（2026-10 在 Chrome 里实测到）。iframe 文档和服务自己的页面因此用 `strict-origin`，请求里仍然没有文章地址。`return` 必须属于站点的已验证 origin（防开放跳转）。binder 同 §4.2。建交易（模式 `redirect`），跳到身份服务。
3. 回调同 §4.2 第 6 步，然后显示确认页：「以 某读者 的身份回到『某某的博客』（blog.example.com）？」页面里带着交易 ID。读者点「确认」提交 `POST /auth/confirm`：`Origin` 检查同上，binder 必须和交易一致，交易必须是 `redirect` 模式、状态为 `authenticated`。通过后交易变为 `confirmed`，跳回 `return` 加上 `#kc-login`。
4. `embed.js` 看到 `#kc-login`，不等懒加载，立即加载组件、滚动到组件处，并用 `history.replaceState` 去掉这个标记（[widget.md §8](widget.md#8-深链接)）。W 从 `localStorage` 取回 verifier 兑换，成功后删掉它。

验证邮件在新标签页里完成时，确认页和返回的文章都在新标签页里，W 仍能从 `localStorage` 取到 verifier：同一个浏览器、同一个分区。存储不可用时（部分 Android WebView 关掉了 DOM storage），verifier 带不过整页跳转，组件在这种环境里只读，提示读者在浏览器中打开。

**确认页是第二道防线**。入口已经只接受组件自己提交的表单，攻击者没法用自己的 challenge 发起交易。确认页让读者知道自己将回到哪个站点；将来万一出现能替别人提交表单的页面，它也能拦住没有读者参与的登录。

### 4.5 参数

| 项 | 值 |
|---|---|
| verifier | 32 字节随机数，base64url |
| 交易有效期 | 30 分钟（注册可能要等验证邮件） |
| binder Cookie | `__Host-comments_binder`：32 字节随机数，`HttpOnly`、`Secure`、`SameSite=Lax`、`Path=/`；每次建交易续期到 1 天后过期，回调时不删除。回调前可能经过 GitHub 等第三方站点，`Strict` 会丢 |
| 兑换 | 只能一次；没见过的 challenge 返回 `202`，过期或已兑换返回 `410` |
| 轮询 | 前 15 秒每秒一次，之后每 3 秒一次；宿主页不可见时暂停，回到页面时立即查一次；交易过期即停止。按这个间隔实现，本地 Chrome 加真实 ZITADEL 实测：已登录身份服务时一两次轮询就兑换成功，`done` 消息到达时立即再查一次。真机上的表现由 C0 复测，必要时再调 |
| 限流 | 建交易和兑换都有上限，数值见 [moderation.md §3.4](moderation.md#34-限额-待定) |

---

## 5. 站点会话

**载体：按站点命名的分区 Cookie 为主，`localStorage` 降级。**

| | 主：分区 Cookie（CHIPS） | 降级：`localStorage` + `Authorization` 头 |
|---|---|---|
| 形式 | `__Host-comments_site_<站点 ID>`：`HttpOnly`、`Secure`、`SameSite=None`、`Partitioned`，由服务端设置 | W 把令牌存在自己的 `localStorage`，键名带站点 ID，请求时放进 `Authorization: Bearer` |
| 哪些浏览器能用 | Chrome、Edge 114+；Firefox；Safari 与 iOS 26.2+（§10） | 都能用，但 Safari 把第三方 iframe 的 `localStorage` 只放在内存里，退出 Safari、重启设备就没了；Brave 在关掉站点的所有标签页后约 30 秒清除 |
| W 里有 XSS 时 | 令牌读不走，但能被利用 | 令牌能被读走 |
| 跨站请求伪造 | 宿主页和 W 共享同一个 Cookie 分区，宿主页发出的请求也会带上 Cookie，必须防御（下面） | 浏览器不会自动附带，天然免疫 |

**怎么选**：兑换成功时（§4.2 第 7 步），服务端既设置分区 Cookie，也在响应里返回同一个令牌。W 随即不带 `Authorization`、只带 Cookie 请求一次 `GET /api/v1/widget/viewer`：认得出读者，说明 Cookie 可用，W 丢弃内存里的令牌；认不出，W 把令牌存进 `localStorage`，以后放在 `Authorization` 头里。服务端两种载体都接受，值是同一个令牌。

**每次加载**：W 加载后带着站点会话请求一次 `GET /api/v1/widget/viewer?site=…`，决定显示哪种输入框、要不要改用管理者的读取接口。这是匿名读者之外唯一一个带凭据的请求，它不带文章地址，`Referer` 也只有服务自己的 origin，所以服务端知道「这位读者此刻打开了这个站点的某一页」，不知道是哪一页。会话的 `last_used_at` 和过期时间每天最多顺延一次。

**为什么按站点命名**：浏览器按顶层站点（协议加可注册域名）分区。同一个可注册域名下的两个站点，比如 `a.example.edu.cn` 和 `b.example.edu.cn`，共用一个分区；只用一个 Cookie 名的话，登录其中一个会把另一个顶掉。服务端按请求里的 `site` 取对应的 Cookie。

**要防的跨站请求**：宿主页的脚本可以向 `comments.kite.plus` 发带 Cookie 的请求。所以所有改变状态的组件接口都要求 `Sec-Fetch-Site: same-origin`：W 调用自己的接口总是 `same-origin`，宿主页发出的是 `cross-site`。没有这个头时（Safari 16.3 及更早版本，或者不是 HTTPS），退回检查 `Origin` 必须等于 `https://comments.kite.plus`；两个都没有就拒绝。登录回调由身份服务跳转到达，不在此列，它靠 binder 和 `state` 防护（§4.2）。

**读取不带会话**：会话 Cookie 用 `__Host-` 前缀，路径只能是 `/`，浏览器会在 iframe 文档和所有请求上都带上它。所以 iframe 文档和匿名读取接口的 Cookie 请求头在反向代理处删掉（[widget.md §4](widget.md#4-iframe-地址)），服务端的读取代码也从不解析会话、不续期。

**两个已知的例外**：

- iOS 26.2 以前的设备只能用降级方案，读者每次重新打开 Safari 都要再点一次登录。升级不了系统的旧 iPhone 会一直这样。
- 一个域名被大量网站当作第三方嵌入时，Safari 的跟踪防护可能把它归为跨站跟踪域名，这类域名可能不被允许使用分区 Cookie（§10）。C0 的 T5 要在真机上测，并在 C2 之后持续观察；被归类时，Safari 读者自动落到降级方案。

其余规则：

- **范围**：只对一个站点有效。W 每个请求都带 `site`，服务端核对会话的站点。
- **时长**：90 天，每次使用顺延，最长 400 天。
- **撤销**：W 的「退出此站点」、读者中心、账号删除或暂停、站点删除都会让它立即失效。
- **丢了怎么办**：浏览器清掉了分区存储，W 显示未登录，读者再点一次即可；身份服务会话还在的话，一点就好。
- **降低 W 的 XSS 风险**：评论 HTML 只由服务端净化后输出（[moderation.md §5](moderation.md#5-内容格式)）；W 的 CSP 不允许内联脚本和第三方脚本，并启用 Trusted Types；令牌永远不出现在地址里。

---

## 6. 退出

| 操作 | 效果 |
|---|---|
| 组件里「退出此站点」 | 删除这个站点的站点会话，清空 W 的存储 |
| 读者中心撤销某个站点，或「退出所有站点」 | 立即删除对应的站点会话；读者下次在那些站点操作时要重新点登录 |
| 读者中心、控制台「退出」 | 删除控制台会话 |
| 在 `id.kite.plus` 退出 | 身份服务通过 back-channel logout 通知评论服务，带上被结束的身份服务会话的 `sid`。评论服务删除**这次身份服务会话期间**签发的控制台会话和站点会话 |

站点会话和控制台会话在签发时记下 ID Token 里的 `sid`（[data-model.md §3.2](data-model.md#32-会话与登录交易)），back-channel logout 靠它精确找到要删的会话。

站点会话比身份服务会话长得多。更早的身份服务会话期间签发、还在有效期内的站点会话，不会因为这次退出而删除；想在所有站点退出，用读者中心的「退出所有站点」。身份服务会话自然过期时**不发** back-channel logout：ZITADEL 只在会话被结束时发（登录页退出、`end_session`、管理员结束会话），最多重试 3 次（identity 仓库 README，2026-10）。

**Logout Token 的校验**（`internal/oidc`）：签名、`iss`、`aud`、`iat` 在 10 分钟内、带 back-channel logout 事件、有 `sid` 或 `sub`、没有 `nonce`；带 `exp` 时不能已过期。有 `sid` 时删除这次身份服务会话签发的会话；只有 `sub` 时删除这个账号的全部会话。2026-10 用本地 ZITADEL 实测：管理员结束会话后几十毫秒内，这次会话签发的站点会话被删除。

---

## 7. 账号删除与停用

**两种删除**：

- **删除评论服务里的数据**：在读者中心操作。删除资料、会话、通知和各站点的作者 ID，评论按读者的选择处理（下面）；Kite Plus 账号本身保留，下次登录评论服务会得到一份全新的资料。
- **删除 Kite Plus 账号**：在身份服务操作（账号页是 `{issuer}/ui/console/users/me`，`GET /api/v1/me` 的 `account_url` 给出完整地址，读者中心链接过去）。身份服务向 Explore 和评论服务推送事件，各自删除本地数据；评论服务按账号设置里的 `on_account_delete` 处理评论。

**评论的两种处理**：

- **删除**（默认）：全部评论改为 `deleted`，页面上立即消失；有回复的留下「该评论已删除」的占位，保持讨论结构；正文按保留期清空（[data-model.md §4](data-model.md#4-保留期)）。
- **保留并匿名**：评论保留，作者显示为「已注销用户」。作者 ID 是按站点区分的，删掉之后，这些评论和原账号再也对不上。

**删除的步骤**：

1. **先看有没有站点要托付**。读者是某个站点唯一的所有者时，不能在读者中心删除自己的数据，要先删除这些站点（C4 起也可以转给别人）。在身份服务上直接删账号时拦不住，这些站点没有了所有者：立即暂停，按站点删除的流程，在恢复窗口过后彻底删除。
2. **在一个事务里完成**：先改写评论，两种处理都把 `author_kind` 改为 `former_user`，选「删除」的同时把状态改为 `deleted`；然后删除用户行，资料、会话、通知、站点作者 ID、站点成员随之删除。数据库约束保证这个顺序：`author_kind` 还是 `user` 的评论不允许 `author_user_id` 为空，所以没先改写评论就删不掉用户（[data-model.md §3.4](data-model.md#34-讨论页与评论)）。

**事件**：

- 身份服务推送到 `POST /hooks/idp`，C 校验签名，按事件 ID 去重（`idp_events`）。以 ZITADEL 为例，它用 Actions v2 推送 `user.removed`、`user.deactivated`、`user.reactivated`、`user.locked`、`user.unlocked`，签名在 `ZITADEL-Signature` 头里：`t=<秒>,v1=<十六进制>`，是以签名密钥对 `<t>.<原始请求体>` 做的 HMAC-SHA256，时间偏差超过 5 分钟拒绝，多个 `v1` 有一个对上即可。事件 ID 是 `<aggregateID>:<sequence>`，`aggregateID` 就是 `sub`。
- **推送只是提示**：各服务的同步令牌（`IAM_OWNER_VIEWER`）能读到所有推送目标的签名密钥，一个服务被攻破就能伪造发给另一个服务的推送（identity 仓库 README）。所以配置了 API 令牌时，C 收到事件（推送或对账）后先用 `GET /v2/users/{sub}` 读这个用户的当前状态，按状态处理：停用 → 暂停；正常 → 解除身份服务造成的暂停；锁定 → 不动。**删除要两样都对上**：用户读不到（`404`，而且响应体是身份服务自己的「找不到用户」错误，gRPC 代码 5，不是代理的错误页），并且权威的事件查询（下面的对账接口，按这个用户过滤）里有它的 `user.removed`；只对上一样就先不删。身份服务读不到或删除还没得到确认时，推送返回 `503`，事件不记入 `idp_events`，留给对账。没有令牌时（测试）按事件类型处理。
- 用户删除 → 按上面处理；用户停用 → 暂停（撤销全部会话，不能发表）；重新启用 → 解除暂停。**锁定和解锁什么也不做**（2026-10 改）：谁都能靠故意输错密码把一位读者锁住，身份服务也在关掉永久锁定，暂停只跟着停用走。
- **推送不可靠**：以 ZITADEL v4.19 为例，每个事件只投递一次，10 秒超时，失败不重试，评论服务停机维护时必然漏收。所以 worker 每小时另跑一次对账：用事件查询 API（`POST /admin/v1/events/_search`，按 `eventTypes`（删除、停用、重新启用）、`aggregateTypes` 过滤，`from` 不含边界）取游标之后的用户事件，补上漏收的。游标是最后一个事件的 `creationDate`，存在 `idp_sync`；查询从游标往前 5 分钟开始，防止乱序提交的事件漏掉，重复的按事件 ID 跳过。某个用户的状态读不到时跳过它、继续处理其余的，存下的游标不越过最早一个跳过的事件，下一轮重试。第一次对账从 30 天前开始。

删除在几秒内生效。备份里的数据随备份轮换消失（保存期见 [architecture.md §9](architecture.md#9-部署与运维)），隐私政策里写明这一点。

---

## 8. 威胁模型与安全检查

C2 上线前逐项核对：

1. **OIDC**：`state`、`nonce`、PKCE 都用上；ID Token 验签并校验 `iss`、`aud`、有效期；回调地址精确匹配；客户端密钥只在服务端环境变量里；JWKS 缓存并能跟随轮换。
2. **弹窗握手**：两个方向都校验 `event.source` 和 `event.origin`；转入整页跳转后忽略迟到的 `ready`；发给宿主页的消息里没有凭据和身份（[widget.md §6](widget.md#6-宿主页与-iframe-的消息)）。
3. **交易**：binder 绑定；只能兑换一次；按时过期；challenge 唯一，每次重试换新的。
4. **入口**：建交易、整页跳转入口和确认都只接受 `comments.kite.plus` 自己页面的请求（`Origin`、`Sec-Fetch-Site`；建交易另要求 `fetch` 和 JSON）；`return` 只允许站点的已验证 origin。
5. **嵌入**：所有 iframe 文档（包括错误页）都带站点的 `frame-ancestors`；本地预览只给只读文档（[widget.md §4](widget.md#4-iframe-地址)）；登录相关页面（弹窗页、确认页）一律 `frame-ancestors 'none'`。
6. **身份不外露**：iframe 高度和发给宿主页的消息不随读者身份变化；iframe 文档和匿名读取接口收不到 Cookie；`embed.js` 以 `crossorigin="anonymous"` 加载。
7. **XSS**：W 的 CSP 和 Trusted Types；净化器用已知 XSS 用例集测试；渲染器升级时重渲染旧评论。
8. **跨站请求**：组件写接口检查 `Sec-Fetch-Site`、`Origin`；控制台检查 CSRF 令牌。
9. **限流**：建交易、兑换、发表、认领、站点验证都有上限。
10. **令牌**：数据库只存哈希；所有撤销路径都有测试。
11. **身份服务事件**：校验签名，防重放。
12. **日志**：不记 IP、令牌和邮箱；访问日志只记路由模板，和 Explore 一样用 `gin.New()`。
13. **依赖**：依赖和镜像漏洞扫描。身份服务的升级时限和限流由 identity 负责（[identity architecture.md §5](https://github.com/kite-plus/identity/blob/main/docs/design/architecture.md#5-运维)）。
14. **`embed.js`**：保持极小，改动单独审查；固定版本提供 SRI（[widget.md §10](widget.md#10-加载体积与降级)）。

C0 的 T8 把宿主页和第三方网站能做的攻击写成自动化测试，C2 起在 CI 里跑。

**2026-10 的实现核对**（不等于 C2 的安全检查已通过，那要逐项复核并请外部的人审）：第 1–12 项都有实现，其中第 2、3、4、5、6、8、10、11 项有自动化测试（`internal/api` 的协议测试、`internal/idp`、`e2e/` 的浏览器测试），第 7 项的净化器有 XSS 用例集；第 9 项的上限都在 `internal/policy`，只测了一部分；第 12 项靠 `gin.New()` 加只记路由模板的日志中间件，没有测试。第 13 项（依赖和镜像扫描）没有做，第 14 项的体积由构建检查。

---

## 9. 以后：FedCM

FedCM 是浏览器提供的联合登录接口，不依赖第三方 Cookie：页面请求登录时，浏览器自己弹出「以 某读者 身份继续」，回访的读者还可以自动登录。浏览器替读者请求账号列表时，不告诉身份服务是哪个网站在请求。它能在不追踪的前提下补上「打开即识别」的体验，但目前只有 Chromium 系的浏览器支持（§10）。

要做的事：

- **支持范围**：只有 Chrome、Edge 108 起支持；Android WebView、Firefox、Safari 都不支持，也没有进入 Interop 2026。所以它只能是锦上添花，基线仍是 §4。
- **在 iframe 里调用**：iframe 要带 `allow="identity-credentials-get"`，这由 `embed.js` 负责。浏览器的对话框显示宿主页的站点名；身份服务的客户端元数据声明自己相对顶层页面是第三方时，也会显示 `comments.kite.plus`。
- **身份服务一侧**：要提供 FedCM 的配置文件、账号列表、签发断言等端点，并用 `Set-Login` 响应头报告登录状态。ZITADEL 不支持充当 FedCM 身份提供方，这些端点得自己写，怎么和身份服务的会话打通 `[待定]`。
- **不需要根域名上的文件**：身份服务和组件同属 `kite.plus`，浏览器对同站的身份提供方跳过 `/.well-known/web-identity` 检查（Chrome 122 起，规范也这样规定），所以 `kite.plus` 根域名可以继续由 GitHub Pages 跳转到 `www`。
- **自动登录的条件**：读者以前在这个站点用过 FedCM、只有一个可用账号、身份服务会话还在，且 10 分钟内没有自动登录过。

**现在只预留一处接口**：`embed.js` 创建 iframe 时就带上 `allow="identity-credentials-get"`（[widget.md §4](widget.md#4-iframe-地址)）。以后再加的话，所有自托管 `embed.js` 的博主都得升级。

是否要做，看 C2 的数据：读者在「点一下登录」这一步流失得多，才值得投入。

---

## 10. 依据

- [OpenID Connect Core 1.0](https://openid.net/specs/openid-connect-core-1_0.html)：授权码流程、ID Token 校验、`iss`/`sub`。
- [RFC 7636 PKCE](https://www.rfc-editor.org/rfc/rfc7636) 与 [RFC 9700 OAuth 2.0 安全最佳实践](https://www.rfc-editor.org/rfc/rfc9700)。

以下是 2026-09 核实的浏览器现状，C0 用真机复测：

- **Chrome 常规模式仍默认允许第三方 Cookie，无痕模式拦截**。2025 年 4 月 Google 决定不再推出单独的选择提示；2025 年 10 月宣布退役大部分 Privacy Sandbox 技术，保留 CHIPS、FedCM 和 Private State Tokens，Storage Access API 不受影响。[Privacy Sandbox 后续安排](https://privacysandbox.google.com/blog/privacy-sandbox-next-steps)、[2025 年 10 月更新](https://privacysandbox.google.com/blog/update-on-plans-for-privacy-sandbox-technologies)。
- **Safari 自 13.1 起拦截全部第三方 Cookie**；iOS 上的浏览器都用 WebKit，iOS 14 起所有 WKWebView 应用默认开启跟踪防护。第三方 iframe 的 `localStorage`、IndexedDB 按顶层站点分区，而且只存在内存里。[WebKit 跟踪防护](https://webkit.org/tracking-prevention/)。
- **Safari 的 CHIPS**：18.4 上线，18.5 因网络层的缺陷停用，26.2 重新启用；18.4 的说明提到跨站跟踪域名可能不被允许使用分区 Cookie。[Safari 18.4](https://webkit.org/blog/16574/webkit-features-in-safari-18-4/)、[Safari 26.2](https://webkit.org/blog/17640/webkit-features-for-safari-26-2/)、[caniuse](https://caniuse.com/mdn-http_headers_set-cookie_partitioned)。
- **Firefox** 默认按站点分区全部第三方状态，存储是持久的；CHIPS 在 141 重新启用。[MDN：状态分区](https://developer.mozilla.org/en-US/docs/Web/Privacy/Guides/State_Partitioning)。
- **Chrome 115 起**第三方 iframe 的存储按顶层站点分区，数据持久保存。[存储分区](https://privacysandbox.google.com/cookies/storage-partitioning)。
- **`Sec-Fetch-Site`**：Chrome 76、Firefox 90、Safari 16.4 起发送，只在 HTTPS 下发送；它比较的是发起请求的 origin 和目标，iframe 调用自己的接口总是 `same-origin`。OWASP 要求在缺少时退回检查 `Origin`。[caniuse](https://caniuse.com/mdn-http_headers_sec-fetch-site)、[OWASP CSRF 防护](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)。
- **弹窗与 COOP**：弹窗进入带 COOP 的页面后 opener 被永久切断，打开者看到的 `closed` 为 `true`；Chrome 83、Firefox 79、Safari 15.2 起如此。[HTML 规范](https://html.spec.whatwg.org/multipage/browsers.html#cross-origin-opener-policies)、[MDN：Cross-Origin-Opener-Policy](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Cross-Origin-Opener-Policy)。
- **Safari 的弹窗拦截**：`await crypto.subtle.digest()` 之后再调用 `window.open` 会被拦截，必须在点击的同步调用栈里打开。[WebKit 源码](https://github.com/WebKit/WebKit/blob/main/Source/WebCore/page/LocalDOMWindow.cpp)。
- **微信**：iOS 14 起微信内置浏览器不支持第三方 Cookie（[微信文档](https://developers.weixin.qq.com/miniprogram/dev/wxcloudservice/wxcloud/guide/web/third-party-cookie.html)）；`window.open` 的行为只有社区反馈（[微信开放社区](https://developers.weixin.qq.com/community/develop/doc/00020c077601e059ffccdca555b000)）。
- **FedCM**：[规范](https://w3c-fedid.github.io/FedCM/)、[依赖方接入](https://developer.chrome.com/docs/identity/fedcm/implement/relying-party)、[身份提供方接入](https://developer.chrome.com/docs/identity/fedcm/implement/identity-provider)。
