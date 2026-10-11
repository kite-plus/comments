# 嵌入组件

> 状态：C2 已实现（`web/widget`、`internal/api/thread.go`），本地 Chrome 实测通过；真机矩阵由 C0 复测后定稿 · 最近更新：2026-10-11
> 登录流程见 [login.md](login.md)；本文只讲组件怎么嵌、怎么和宿主页相处。

---

## 1. 目标

1. **两行代码接入**：任何能插入一段 HTML 的博客都能用，不依赖框架。Hexo、Hugo、Jekyll、Astro、VitePress、WordPress、Typecho、Halo、Kite 都在范围内；掘金、CSDN、公众号这类不允许插脚本的托管平台不在范围内。
2. **宿主页拿不到读者的任何凭据**：登录、写入和令牌都在 `comments.kite.plus` 的 iframe 里，宿主页只负责摆放和尺寸（[architecture.md §0.2](architecture.md#0-两个核心判断)）。
3. **读评论不带 Cookie、不留记录**：`embed.js` 以 `crossorigin="anonymous"` 加载，不带 Cookie；iframe 文档和匿名读取接口的 Cookie 请求头在反向代理处删掉，服务端根本收不到会话，也不记录谁读了什么。
4. **不拖累宿主页**：异步加载，滚动到附近才加载，服务出故障时宿主页照常显示。
5. **宿主页看不出谁在读**：组件的高度、发给宿主页的消息都不随读者身份变化（§6、§9）。

---

## 2. 接入代码

```html
<kite-comments site="站点 ID"></kite-comments>
<script src="https://comments.kite.plus/embed.js" crossorigin="anonymous" async></script>
```

站点 ID 在控制台复制。`<kite-comments>` 是 `embed.js` 定义的自定义元素，可以放在页面任何位置，脚本加载前后都行。`crossorigin="anonymous"` 让浏览器加载脚本时不带 Cookie（§1）；想锁定版本的博主，控制台另给一份带 `integrity` 的固定版本写法（§10）。

| 属性 | 必填 | 说明 |
|---|---|---|
| `site` | 是 | 站点 ID |
| `page` | 否 | 这篇文章的规范地址。默认取 `<link rel="canonical">`，没有就取当前地址 |
| `page-key` | 否 | 显式的讨论页标识，优先于 `page`。改过链接的站点、单页应用用它；Kite 传文章的 ULID（§5） |
| `page-title` | 否 | 用于通知邮件和控制台，默认取 `document.title` |
| `page-published` | 否 | 文章的发布时间（ISO 8601），用于「自动关闭旧文章的评论」；Kite 传文章日期 |
| `theme` | 否 | `auto`（默认，跟随宿主页背景）、`light`、`dark` |
| `accent` | 否 | 强调色，`#RRGGBB`，默认 `#3d65bd` |
| `font` | 否 | `sans`（默认）或 `serif`，都用读者设备上的字体 |
| `lang` | 否 | 界面语言。默认取宿主页 `<html lang>`，没有再取浏览器语言。三个来源用同一条规则归一：以 `zh` 开头的（`zh`、`zh-CN`、`zh-Hans`、`zh-TW`……，不分大小写）一律是 `zh-CN`，其余一律是 `en`。Kite 的评论插件原样传站点语言（如 `zh-Hans`、`en-US`），由这条规则处理 |
| `lazy` | 否 | 默认滚动到距离组件 800px 内才加载；`lazy="false"` 立即加载 |

**属性变化即生效**：改 `page`、`page-key` 会换到另一个讨论页，改 `theme`、`accent`、`lang` 当场生效，元素移出文档就销毁 iframe。所以 VitePress、Docusaurus、Next.js 这类单页应用切换路由时，框架照常重新渲染这个元素即可，不需要调用任何 API。

**现在就要定死的接口**：元素名 `kite-comments` 和上表属性、脚本标签上的 `crossorigin="anonymous"`、iframe 地址（§4）、宿主页与 iframe 的消息协议（§6）、讨论页标识规则（§5）、评论锚点 `#kc-<评论 ID>` 和登录返回标记 `#kc-login`（§8）。它们会被博主复制进成千上万个页面，以后改动只能加版本号，不能原地改。

---

## 3. 结构

```text
宿主页 https://blog.example.com/posts/hello
│
├─ embed.js（宿主页里运行，≤ 4 KB gzip，不带 Cookie 加载）
│    定义 <kite-comments>；算出页面地址、标识、语言和主题；
│    懒加载；创建 iframe；调整高度；处理 #kc- 深链接。
│    不接触凭据，不读写宿主页的 Cookie 和存储。
│
└─ <iframe src="https://comments.kite.plus/embed/v1/thread?…">
     评论列表：Go 渲染的外壳，首屏数据内联，匿名读取，Cookie 在反向代理处删掉
     界面：Preact 应用（≤ 40 KB gzip）
     登录：弹窗 → id.kite.plus → 回到 comments.kite.plus（login.md）
     站点会话：按站点命名的分区 Cookie，不可用时存在 iframe 自己的存储里
```

iframe 里的界面用 Preact 写，站点控制台和读者中心用 React（[architecture.md §7](architecture.md#7-技术选型)）。组件对体积敏感，控制台不敏感，两者不共享组件代码，只共享类型定义和 API 客户端。

---

## 4. iframe 地址

```text
https://comments.kite.plus/embed/v1/thread
    ?site=<站点 ID>
    &url=<页面规范地址>
    &key=<page-key，可省略>
    &title=<页面标题>
    &published=<发布时间，可省略>
    &lang=zh-CN&theme=light&accent=%233d65bd&font=serif
    &cs=<宿主页的 color-scheme>
    &focus=<评论 ID，可省略>
```

`theme` 是 `embed.js` 判断后的结果，只有 `light`、`dark`（`auto` 在宿主页上算完再传）；`cs` 是宿主页计算出的 `color-scheme`，iframe 设成一样的值（§7）；`lang` 已按 §2 归一。

服务端收到后：

1. **看站点状态**：`active` 正常显示；`suspended` 显示「评论已关闭」，只读；其余状态（未验证、待批准、已删除）显示「评论暂不可用」。
2. **看 `url` 决定模式**：
   - `url` 的 origin 是站点的已验证 origin（只接受 https）：正常模式，`frame-ancestors` 只列站点的已验证 origin。
   - `url` 是 `http://localhost:*` 或 `http://127.0.0.1:*`，且站点允许本地预览：本地预览模式。文档只读，不显示登录入口，`frame-ancestors` 只列这两个本地地址，已验证 origin 不在其中。所以本地预览放开不会让任何写操作暴露在本地页面里。
   - 其他情况：显示「此页面不属于这个站点」，`frame-ancestors` 仍只列已验证 origin。
   - 站点没有已验证 origin（或站点不存在）时，`frame-ancestors 'none'`。
   - **本地开发**：开发服务器开了 `COMMENTS_DEV_HTTP_ORIGINS` 时，http 的已验证 origin 也按正常模式处理，两个测试博客 `http://blog-a.localhost:8301`、`http://blog-b.localhost:8302` 靠它工作。`COMMENTS_PUBLIC_URL` 是 https 时服务拒绝启动（[sites.md §2](sites.md#2-注册与验证)）。
3. 按 §5 算出讨论页标识，查出第一页评论，内联进 HTML。讨论页不存在就返回空列表，**不创建记录**。
4. 响应头：
   - `Content-Security-Policy: frame-ancestors <按上面的模式>`。**这是「组件只能出现在已验证站点上」的真正保证**：iframe 里发出的请求，`Origin` 永远是 `comments.kite.plus`，服务端无法靠请求头判断宿主是谁，只能让浏览器按 `frame-ancestors` 拒绝在别处渲染。
   - 其余 CSP：`default-src 'none'; script-src 'self'; style-src 'self' 'sha256-…'; img-src 'self' data:; connect-src 'self'; form-action 'self' <身份服务的 origin>; base-uri 'none'; require-trusted-types-for 'script'; trusted-types kc`。不允许内联脚本；内联的只有组件的 CSS（整份，约 1.8 KB gzip），服务启动时算出它的哈希写进 `style-src`（文档可被缓存，不能用每次响应都变的 nonce）；首屏数据放在 `<script type="application/json">` 里，浏览器不执行它，不受 `script-src` 限制。头像、图标都由自己的域名提供。`form-action` 加上身份服务，是因为整页跳转的表单先到 `/auth/redirect` 再被重定向过去，浏览器对重定向也检查 `form-action`。评论 HTML 经名为 `kc` 的 Trusted Types 策略写入，只有它能创建。
   - `Referrer-Policy: strict-origin`，不是 `no-referrer`（[login.md §4.4](login.md#44-整页跳转降级)）。
   - `Cache-Control: public, max-age=30`，不带 `Set-Cookie`。
5. **收不到 Cookie**：反向代理对 iframe 文档、匿名读取接口、头像和 `embed.js` 删掉 Cookie 请求头。会话 Cookie 必须用 `__Host-` 前缀，路径只能是 `/`，浏览器会在这些请求上带上它；删掉之后，服务端在技术上就无法把读取和身份对上（[login.md §5](login.md#5-站点会话)）。
6. `embed.js` 创建 iframe 时带上 `title`（「评论」/「Comments」）和 `allow="identity-credentials-get"`。后者给将来的 FedCM 预留（[login.md §9](login.md#9-以后fedcm)），现在加上没有副作用，以后再加就得让所有自托管 `embed.js` 的博主升级。

---

## 5. 讨论页标识

一个站点内，同一个讨论页标识对应同一串评论。规则只写在这里，服务端由 `internal/pagekey` 实现，接入文档和导入工具都链接到这一节。

1. 有 `page-key`：去掉首尾空白，长度 1–200，不能含控制字符，标识为 `k:<page-key>`。
2. 否则取页面地址：`page` 属性 → `<link rel="canonical">` → 当前地址，相对地址按当前页面解析成绝对地址。
3. 地址的 origin 必须是站点的已验证 origin 之一。同一站点的所有 origin 共享一套标识，所以 `https://example.com/a` 和 `https://www.example.com/a` 是同一个讨论页。
4. 路径规范化：
   - 百分号编码的 UTF-8 解码成字符（`/`、`?`、`#`、`%` 除外），再做 Unicode NFC；`/posts/%E4%BD%A0%E5%A5%BD` 和 `/posts/你好` 相同。
   - 去掉 `.`、`..` 路径段。
   - 去掉末尾的 `index.html`、`index.htm`，再去掉末尾的 `.html`、`.htm`：GitHub Pages 等托管平台对 `/a.html` 和 `/a` 返回同一个文件。
   - 去掉末尾的 `/`，根路径 `/` 除外。
   - 保留大小写：服务器可能区分大小写。
5. 查询参数只保留站点 `query_keys` 里列出的，按名称排序；片段一律去掉。
6. 结果为 `p:<路径>[?<参数>]`。超过 512 个字符时改用 `h:` 加上这个 `p:` 标识的 SHA-256 十六进制串。三种前缀保证显式标识、路径和哈希永远不会撞上。

| 页面地址 | 标识 |
|---|---|
| `https://blog.example.com/posts/hello/` | `p:/posts/hello` |
| `https://www.blog.example.com/posts/hello.html?utm_source=x#top` | `p:/posts/hello` |
| `https://blog.example.com/?p=123`（`query_keys = {p}`） | `p:/?p=123` |
| `page-key="01J9Z3K6V4R8Q2X5N7M1B0C3D4"` | `k:01J9Z3K6V4R8Q2X5N7M1B0C3D4` |

建议有稳定 ID 的系统都传 `page-key`：Kite 传 front matter 里的文章 ULID，WordPress 插件传 `wp:<文章 ID>`。这样改标题、改链接、换域名都不会让评论丢失。只靠地址的站点改了链接，由博主在控制台把旧讨论页迁到新标识（[sites.md §4](sites.md#4-站点设置与讨论页管理)）。

---

## 6. 宿主页与 iframe 的消息

用 `postMessage` 传，消息都是 `{kc: 1, type: "…", …}`，`kc` 是协议版本。

| 方向 | `type` | 内容 | 用途 |
|---|---|---|---|
| iframe → 宿主 | `ready` | —— | 加载完成，`embed.js` 取消超时提示 |
| iframe → 宿主 | `resize` | `height` | 调整 iframe 高度 |
| iframe → 宿主 | `scroll` | `top` | 深链接定位：目标评论在 iframe 内的偏移（§8） |
| iframe → 宿主 | `count` | `count` | 已发布评论数，`embed.js` 在元素上派发 `kite-comments:count` 事件，主题可以拿去显示「12 条评论」 |
| 宿主 → iframe | `theme` | `mode`、`accent`、`colorScheme` | 宿主页切换深浅色 |
| 宿主 → iframe | `focus` | `commentId` | 宿主页地址的 `#kc-` 变了 |

校验规则：

- `embed.js` 只接受 `event.origin === "https://comments.kite.plus"` 且 `event.source` 是自己创建的那个 iframe 的消息。
- iframe 只接受 `event.source === window.parent`、且 `event.origin` 属于该站点已验证 origin 的消息；已验证 origin 列表由服务端写进 iframe 文档。本地预览模式另接受 `http://localhost:*`、`http://127.0.0.1:*`。
- iframe 发给宿主页的消息目标 origin 写 `*`：iframe 不知道自己被哪个已验证 origin 嵌入，而消息内容（高度、偏移、评论数）对任何人都一样。
- **消息里永远不出现凭据、读者身份和评论内容**。宿主页不应该知道正在看页面的是谁，评论数是唯一对外的数据，而且对所有读者都一样。`resize` 报告的高度同样不能泄露身份，界面上怎么保证见 §9。

---

## 7. 外观

- **高度**：iframe 内用 `ResizeObserver` 报告高度，`embed.js` 设置 `iframe.style.height`。加载前保留 160px，减少布局跳动。
- **深浅色**：`theme="auto"` 时，`embed.js` 取宿主页 `body`（透明就往上取 `html`）的计算背景色，相对亮度低于 0.4 判为深色；监听 `prefers-color-scheme` 变化，以及 `html`、`body` 上 `class`、`style`、`data-theme` 属性的变化（100ms 去抖），变了就发 `theme` 消息。显式的 `light`、`dark` 不做检测。
- **背景透明**：iframe 文档背景透明，融进宿主页。浏览器在 iframe 与宿主页的 `color-scheme` 不一致时会给 iframe 垫一层不透明底色，所以 `embed.js` 会把宿主页的计算 `color-scheme` 一并传进来，iframe 设成一样的值。
- **字体**：跨域 iframe 继承不了宿主页字体，只提供 `sans`、`serif` 两套系统字体栈，都带中文回退，不加载网络字体。
- **宽度**：跟随容器，最窄 320px。
- **无障碍**：全部操作可用键盘完成；新评论用 `aria-live` 播报；尊重 `prefers-reduced-motion`。

---

## 8. 深链接

每条评论的固定链接是 `<讨论页地址>#kc-<评论 ID>`，通知邮件和读者中心都用它。

1. `embed.js` 启动时发现 `location.hash` 形如 `#kc-123`，忽略懒加载立即加载，并把 `focus=123` 带进 iframe 地址；之后监听 `hashchange`。
2. iframe 取回包含这条评论的那一页（接口的 `around` 参数，[api.md §3](api.md#3-组件读取匿名)），高亮它，发 `scroll` 消息报告偏移。
3. `embed.js` 把宿主页滚动到 iframe 顶部加偏移处，上方留 80px。

评论不存在、被隐藏或还在审核中时，只显示讨论页开头，不提示原因。

**登录返回标记**：整页跳转登录回来时，地址带着 `#kc-login`（[login.md §4.4](login.md#44-整页跳转降级)）。`embed.js` 看到它就忽略懒加载立即加载组件，滚动到组件处，再用 `history.replaceState` 去掉这个标记。

---

## 9. 界面

| 区域 | 内容 |
|---|---|
| 顶部 | 「N 条评论」；排序切换（最早、最新），默认值由站点设置决定 |
| 输入框 | 登录前后占用同样的高度。未登录：「用 Kite Plus 账号登录」按钮，站点允许访客时另有「以访客身份评论」（展开后的昵称、邮箱、网址和「有人回复时通知我」是读者点开的，不算）；已登录：头像、显示名、Markdown 输入框（支持预览）、「发表」，菜单里有「退出此站点」「读者中心」 |
| 单条评论 | 头像、显示名（有个人主页时链接过去，带 `rel="ugc nofollow"`）、「博主」标记、「已编辑」标记、相对时间（悬停显示完整时间）、正文；操作：回复、举报，自己的评论可编辑、删除 |
| 回复 | 挂在顶层评论下，默认显示接口带回的前几条（[api.md §1](api.md#1-约定)），其余点「展开 N 条回复」；被回复者不是顶层作者时显示「回复 @某人」 |
| 管理者 | 站点所有者和协管员登录后，组件改用带身份的读取接口，能看到待审、隐藏和垃圾评论，每条评论多出通过、隐藏、恢复、置顶、标为垃圾；封禁和批量处理在控制台里做，组件里给出链接 |
| 状态 | 评论进了审核时，只在发表的那一刻显示「已提交，审核通过后显示」，之后在读者中心查看状态，组件里不再显示；讨论页锁定时显示「评论已关闭」；没有评论时显示「还没有评论」 |
| 底部 | 小字「Kite Plus 评论」，链接到 `comments.kite.plus` |

界面文字有 `zh-CN`、`en` 两套，从第一版起就有。

**实现**：输入区的高度固定为 172px，未登录时同样大小的框里放「用 Kite Plus 账号登录」按钮，已登录时放头像、显示名、菜单和输入框；讨论页锁定、本地预览也用同样高度的框显示说明。访客表单、回复框、举报框、编辑框和删除的二次确认都是读者自己点开的。顶层评论已删除或被隐藏、下面还有可见回复时显示占位「该评论已删除」或「该评论已隐藏」（[api.md §1](api.md#1-约定)）。删除用组件内的二次确认，不用 `confirm()`：跨源 iframe 里的对话框会被浏览器拦掉。访客表单把昵称、邮箱、网址记在 iframe 存储的 `kc:g` 里方便下次填写；另有一个随机的访客令牌 `kc:gt`（32 字节，base64url），随访客评论提交，回复通知的同意按它确认（[moderation.md §6.1](moderation.md#61-读者)）。iframe 的存储按宿主站点分区，所以同一个浏览器在不同博客上是不同的令牌，同一个邮箱在每个博客上各确认一次。2026-10 的浏览器测试比较了同一讨论页登录前后、以及自己有一条待审评论时的 iframe 高度，完全相同（[roadmap.md](roadmap.md) 的 T8）。

**高度不随身份变化**：宿主页能看到 iframe 的高度，所以对普通读者来说，同一个讨论页在任何人眼里都一样高：登录前后输入框一样高，自己待审核的评论不留在组件里。否则宿主页能看出读者有没有登录；博主甚至可以故意压着某位读者的评论不审，靠高度差认出这位读者什么时候来访。自己的评论显示的「编辑」「删除」放在每条评论都有的操作行里，不改变高度。管理者看到的界面不同，这不要紧：管理者就是博主和博主指定的人。C0 的 T8 会比较两种状态下的高度。

---

## 10. 加载、体积与降级

| 项 | 预算或规则 |
|---|---|
| `embed.js` | ≤ 4 KB gzip（现在 2.7 KB）；`Cache-Control: public, max-age=300`，改动 5 分钟内生效；带 `Access-Control-Allow-Origin: *`，配合 `crossorigin="anonymous"` |
| 固定版本 | 同时发布 `/embed/v1.<次版本>.<补丁>.js`，内容不可变、长期缓存；控制台从 `GET /api/v1/embed-info` 取地址和 `integrity`，给出锁定版本和自托管的写法（§12）。版本号是 `web/widget/package.json` 的 `version`；构建把当前版本写到 `dist/embed/`，旧版本发布时放进 `web/widget/public/embed/` 一起提交，继续可用 |
| 服务的 origin | 构建出的 `embed.js` 里只有占位，服务端返回时换成自己的 `COMMENTS_PUBLIC_URL`，SRI 按换完的内容算。所以下载下来自托管的副本仍然指向服务 |
| iframe 文档 | HTML 外壳、关键 CSS 和首屏 20 条顶层评论的数据，合计 ≤ 30 KB gzip |
| 界面脚本 | ≤ 40 KB gzip（现在 14 KB，Preact 加界面），文件名带哈希，长期缓存。`make web` 的构建在超出预算时失败 |
| 第三方请求 | 无：不加载网络字体、统计脚本或外链头像 |
| 懒加载 | `IntersectionObserver`，提前 800px；不支持时在 `load` 事件后加载 |
| 超时 | 10 秒没收到 `ready`，元素内显示「评论加载失败，重试」，并在控制台输出可能的原因（站点未验证、当前 origin 不在站点里、网络问题） |
| 服务不可用 | 只影响元素本身；宿主页的渲染、脚本和布局不受影响 |

---

## 11. 宿主页需要注意的

- **CSP**：宿主页有 CSP 的，要允许 `script-src https://comments.kite.plus`（或自托管 `embed.js`）和 `frame-src https://comments.kite.plus`。
- **COOP**：宿主页设置 `Cross-Origin-Opener-Policy: same-origin` 时，浏览器会切断登录弹窗和 iframe 之间的联系，登录会退回整页跳转（[login.md §4.4](login.md#44-整页跳转降级)）。建议改用 `same-origin-allow-popups`。
- **写在正文里**：Kite 默认会转义 Markdown 里的 HTML，把接入代码写进文章正文需要开启 `markdown.unsafeHTML`。推荐改在主题模板里接入（[sites.md §5](sites.md#5-各博客系统的接入)）。
- **SEO**：iframe 里的评论不算宿主页的内容，搜索引擎不会把它当作文章的一部分收录。需要的博主等 C4 的「把评论同步回站点文件」（[roadmap.md](roadmap.md)）。

---

## 12. 自托管 `embed.js`

博主可以下载固定版本的 `embed.js` 放到自己的域名下。这样读者滚动到评论区之前，页面不会向 `comments.kite.plus` 发出任何请求。

不想自托管、又想锁定版本的博主，用控制台给出的固定版本写法，带上 `integrity` 和 `crossorigin="anonymous"`。`embed.js` 运行在每个接入博客自己的页面里，评论服务一旦被入侵、`embed.js` 被篡改，所有用自动更新写法的博客会同时受影响（[architecture.md §11](architecture.md#11-风险)）；锁定版本加 SRI 的博客不受影响。

代价是不会自动更新。v1 的 iframe 地址和消息协议在 v2 发布后至少再兼容 12 个月，期间旧版本的 `embed.js` 照常工作。
