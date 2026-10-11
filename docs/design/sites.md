# 站点、接入与迁移

> 状态：§2–§4、§6–§8 已实现（C2 加上 C3 的导入、认领、迁移、复查）；§7 的格式按各项目源码核对过，还要用真实数据验收 · 最近更新：2026-10-11
> 表结构见 [data-model.md §3.3](data-model.md#33-站点)，嵌入代码见 [widget.md §2](widget.md#2-接入代码)。数量上限统一写在 [moderation.md §3.4](moderation.md#34-限额-待定)，保留期统一写在 [data-model.md §4](data-model.md#4-保留期)。

---

## 1. 角色

| 角色 | 谁 | 能做什么 |
|---|---|---|
| 站点所有者 | 注册站点的人 | 站点设置、验证、审核、导入导出、删除站点；C4 起可以添加协管员、把站点转给别人 |
| 协管员 | 所有者邀请的人（C4） | 审核和封禁，不能改设置、不能导出、不能删站点 |
| 平台维护者 | Kite Plus 运营方 | 见 [moderation.md §4.2](moderation.md#42-平台) |

一个人可以拥有多个站点；一个站点在 C4 之前只有一个所有者。唯一的所有者要删除自己在评论服务里的数据，得先处理自己的站点（[login.md §7](login.md#7-账号删除与停用)）。

---

## 2. 注册与验证

1. 在 `comments.kite.plus/console` 用 Kite Plus 账号登录。
2. 新建站点：填名称和主 origin（如 `https://blog.example.com`），可再加几个别名 origin（如 `https://www.blog.example.com`，数量上限见 moderation.md §3.4）。**只接受 https**：http 页面里的内容能被网络上的任何人篡改，等于把「已验证宿主」的身份交给了他们。origin 只包含协议、主机和端口，统一转成小写，国际化域名转成 punycode。
3. 系统生成验证令牌，7 天有效。每个 origin 用下面任意一种方式证明控制权：

   | 方式 | 放什么 | 适合 |
   |---|---|---|
   | DNS TXT | `_kite-comments.<主机名>` 的 TXT 记录 `kite-comments-site=<令牌>` | 能管理域名解析的博主 |
   | HTML meta | origin 首页 `<head>` 里的 `<meta name="kite-comments-site" content="<令牌>">` | 用 GitHub Pages、Vercel 等平台子域名、改不了 DNS 的博主 |
   | 文件 | `https://<主机名>/.well-known/kite-comments.txt`，内容就是令牌 | 同上 |

4. 点「验证」后，服务端查询 DNS 或抓取页面：超时 10 秒，响应最多读 1 MB，只跟随同一主机内最多 3 次跳转；拒绝解析到内网地址（和 Explore 抓取的防护相同）。
5. 至少有一个 origin 验证通过，站点就变为 `active`；C2 内测期间先进 `pending_review`，由平台批准（`internal/policy` 的 `SiteApprovalRequired`，C3 开放注册时关掉）。还没验证的 origin 不影响站点，只是不出现在 `frame-ancestors` 里，组件在那个 origin 上显示不出来。所以给正在使用的站点加一个别名，不会让站点下线。

实现上，令牌只存哈希：服务端查 DNS 和页面，把找到的候选值算哈希，和站点有效令牌的哈希比对。验证通过时记下那个令牌的哈希（`site_origins.proof_hash`），复查认它。没通过时，结果里写明查到了什么（没有 TXT 记录、首页没有那个 meta、文件取不到，或者都在但对不上）。

**本地开发**：`COMMENTS_DEV_HTTP_ORIGINS=true` 时站点可以用 http 的 origin，组件也能嵌进 http 页面；`COMMENTS_ALLOW_PRIVATE_NETWORKS=true` 时验证可以访问内网地址和任意端口，`*.localhost` 解析到本机。前一个开关在 `COMMENTS_PUBLIC_URL` 是 https 时拒绝启动；两个都只给开发机用。`comments dev seed` 直接把两个测试博客登记成已验证、已批准的站点。

**验证记录请保留**。C3 起，服务端每 30 天复查一次每个已验证的 origin（已实现：worker 每小时挑出到期的，每次最多 50 个）；另一个账号试图验证同一个 origin 时，立即复查一次（未实现，见下）。复查失败先通知所有者；连续 14 天失败，这个 origin 就从 `frame-ancestors` 里去掉，组件在那里不再显示，也不再接收来自那里的评论，所有者再收到一封邮件。

**域名易主**（未实现）：一个 origin 只能属于一个站点。新的控制者验证通过、且旧站点对这个 origin 的复查已经失败时，这个 origin 转给新站点。现在的实现里 origin 是 `site_origins` 的主键，别的站点加它直接 `409`，所以易主要等复查把它从旧站点去掉（去掉的只是验证状态，行还在）之后再做，需要一个「待转移」的状态，C3 开放注册前补上。**旧站点的评论不随域名转移**：它们是旧博主和读者之间的数据，新的域名持有人无权接手。旧所有者仍可以登录导出。复查放在 C3，和开放注册同时上线：在那之前站点都经过平台批准，买下过期域名的人没法冒充旧博客。

**与 Explore 互认**（C4）：同一个 Kite Plus 账号已在 Explore 用 `_explore-claim.<主机名>` 认领过的博客（[Explore accounts.md §5](https://github.com/kite-plus/explore/blob/main/docs/design/accounts.md#5-数据模型)），在这里注册时视为已验证。

---

## 3. 嵌入

控制台按博客系统给出现成的代码（§5）。通用写法、属性和讨论页标识规则都只写在 [widget.md](widget.md)。

---

## 4. 站点设置与讨论页管理

| 设置 | 选项 | 默认 |
|---|---|---|
| 访客评论 | 关闭、先审后发、直接发布 | C2 固定为关闭；C3 起新建站点时由博主明确选择，没有默认值 |
| 登录读者的评论 | 直接发布、先审后发 | 直接发布 |
| 排序 | 最早在前、最新在前 | 最早在前 |
| 自动关闭 | 文章发布 N 天后不再接收新评论；留空不关闭 | 不关闭 |
| 屏蔽词 | 命中的评论进入待审 | 空 |
| 计入标识的查询参数 | 如 WordPress 朴素链接的 `p` | 空 |
| 本地预览 | 在 `localhost`、`127.0.0.1` 的页面里显示只读的组件（[widget.md §4](widget.md#4-iframe-地址)） | 允许 |
| 站长通知 | 即时、每日汇总、关闭 | 即时 |

屏蔽词和查询参数的数量上限见 [moderation.md §3.4](moderation.md#34-限额-待定)。

**自动关闭**按文章的发布时间算：嵌入代码给了 `page-published` 就用它，没给就用讨论页的创建时间，也就是第一条评论的时间。发布时间只在讨论页创建时记下，博主可以在讨论页管理里改。不认后来的评论报上来的时间，是为了防止有人给还没发布的文章提前「占座」，让它一发布就被关闭。

**讨论页管理**：

- 列出站点的所有讨论页：标题、地址、评论数、最近一条评论的时间。
- 锁定、解锁。
- 修改标题、地址和发布时间。讨论页由第一条评论创建，标题和地址来自那次提交，只有管理者能改（[data-model.md §3.4](data-model.md#34-讨论页与评论)）。
- **迁移**：文章改了链接，把旧标识（如 `p:/2021/hello`）的评论迁到新标识；新标识已有评论时合并，按时间排序。每次迁移写进审核记录，可以撤销。实现上旧讨论页保留为空，撤销时把记录里的评论 ID 移回去；目标是这次迁移新建的、撤销后又空了的，一并删掉；同一次迁移只能撤销一次。目标可以给标识，也可以给新地址由服务端算标识。

**删除站点**：嵌入立即失效；数据在恢复窗口内保留，期间可以找回，之后彻底删除（[data-model.md §4](data-model.md#4-保留期)）。删除前控制台会提示先导出。

---

## 5. 各博客系统的接入

| 系统 | 做法 | 讨论页标识 | 阶段 |
|---|---|---|---|
| Kite | 官方评论插件 `plugin-comments` 的服务选项：插件设置里选「Kite Plus 评论」、填站点 ID，所有主题都能用；front matter 写 `comments: false` 可关闭单篇 | 文章 ULID | C2 |
| Hexo、Hugo、Jekyll、Astro | 在主题的文章模板里粘贴接入代码；控制台给出各自的模板片段 | 路径 | C2 |
| VitePress、Docusaurus 等单页应用 | 在布局组件里渲染 `<kite-comments>`，路由切换时框架会更新属性（[widget.md §2](widget.md#2-接入代码)） | 路径或页面 ID | C2 |
| WordPress | C2 粘贴到主题的 `comments.php`；C4 提供插件：替换原生评论、导入已有评论、自动设置 `p` 参数 | `wp:<文章 ID>` | C2 / C4 |
| Typecho | C2 粘贴到主题的 `comments.php`；C4 提供插件 | 路径 | C2 / C4 |
| Halo 2 | C2 用主题模板或代码注入；C4 提供实现评论组件扩展点的插件，用本地 `halo-local` 验证 | `halo:<文章 name>` | C2 / C4 |

### 5.1 Kite 评论插件

Kite 的评论由官方插件 [`plugin-comments`](https://github.com/kite-plus/plugin-comments) 负责，它已经支持 Giscus、Waline、Twikoo，站长在后台的插件设置里选一个服务。Kite Plus 评论作为第四个选项加进这个插件，不写进任何主题（2026-10-10 改：原设计是默认主题内置，那时还没有这个插件）。

- **为什么放进插件**：评论是站点的选择，不是主题的。放在插件里，所有主题都能用，换主题不丢设置；从 Giscus、Waline、Twikoo 换过来也只是改一个选项，旧评论按 §7 导入。
- **设置**：`provider` 增加 `kite-plus`，显示为「Kite Plus 评论」；新增 `site`（站点 ID，在控制台复制），只在选中时显示。选中却没填时，和其他服务一样在评论位置提示缺哪一项。插件已有的「页面下也显示」（`on_pages`）照常生效。
- **输出**：插件的 `comments.js` 找评论位置（主题标了 `data-kite-comments` 的元素，没有就放在正文末尾），在那里插入组件并加载 `embed.js`。意思如下，具体写法以插件实现为准：

  ```html
  <kite-comments site="{{ 站点 ID }}"
                 page-key="{{ .Page.ID }}"
                 page-title="{{ 文章标题 }}"
                 page-published="{{ 文章日期，ISO 8601 }}"
                 lang="{{ 站点语言 }}"></kite-comments>
  <script src="https://comments.kite.plus/embed.js" crossorigin="anonymous" async></script>
  ```

  讨论页用文章的 ULID 标识，改链接不丢评论；`page` 不传，组件自己取 canonical 地址或当前地址（[widget.md §2](widget.md#2-接入代码)）。
- **`plugin.yaml`**：`hosts` 加上 `comments.kite.plus`。
- **深浅色**：组件的 `theme="auto"` 跟随宿主页背景（[widget.md §7](widget.md#7-外观)），插件不用像对 Giscus 那样监听主题切换。
- **本地预览**：`kite run` 在 `localhost:1717` 预览时，组件以只读方式显示（站点设置「本地预览」）。
- **没有私有通道**：插件输出的就是 §5 表里其他博客用的同一套嵌入代码。

### 5.2 放在哪里

组件放在文章正文之后、上一篇/下一篇导航之前或之后都可以。不要放进会被 `display: none` 隐藏的折叠区：懒加载靠元素进入视口触发，被隐藏的元素永远不会加载。

---

## 6. 认领历史评论

读者在各个博客用访客身份留过的评论（包括博主导入的），可以认领到自己的账号下，这是「追溯自己的评论」对过去那部分的回答。

1. 在读者中心点「认领访客评论」，填邮箱。
2. 系统向这个邮箱发一封验证邮件，链接 1 小时内有效。**无论这个邮箱有没有评论都照发**，页面也不显示数量，防止有人借此探测别人在哪些博客留过言。邮件写明「如果不是你本人发起的，忽略这封邮件即可」。
3. 链接打开的是读者中心的页面，**只有发起这次认领的账号能完成验证**：没登录或登录的是别的账号，页面只提示这封邮件不是发给你的；是本人，页面上点「验证」（POST）才生效。打开链接本身不改变任何状态，所以邮件网关自动打开链接也不会误验证。
4. 验证通过后，读者看到全网用这个邮箱留下的访客评论的数量和所在站点，再点「认领」确认。
5. 认领后，这些评论改为显示账号当前的显示名和头像，访客邮箱的密文清除；原来的昵称记进审核记录。
6. 认领的邮箱就是账号在身份服务已验证的邮箱时，跳过第 2、3 步，读者中心直接提示「发现 N 条用你的邮箱留下的评论」，仍需读者确认。

为什么第 3 步必须绑定发起人：如果谁点开链接谁就完成验证，攻击者可以拿受害者的邮箱发起认领，等受害者好奇点开链接（或者邮件网关自动打开），就把受害者的旧评论认领到自己名下，还能看到受害者在哪些博客留过言。

这和 Explore「不按邮箱自动合并账号」的原则一致：认领的是评论，不是账号，而且每次都由读者主动确认。

没有可认领邮箱的评论导入为 `external` 作者（[data-model.md §3.4](data-model.md#34-讨论页与评论)），不能按邮箱认领：Disqus 的导出文件实际上不含邮箱；Giscus 的评论属于 GitHub 用户；本服务导出的登录读者的评论只带作者在原站点的 ID 和显示名。Artalk 会把无效邮箱替换成 `anonymous@example.org`，按空邮箱处理。Giscus 的评论记下了 GitHub 用户名，C4 可以改为用 GitHub 登录认领。

---

## 7. 导入

以下格式细节按 2026-09 各项目的源码核对（Artalk v2.10.0、Waline server 1.41.6、Twikoo 2.0.9），实现前再核对一次。

| 格式 | 文件 | 正文 | 能否按邮箱认领 | 阶段 |
|---|---|---|---|---|
| 本服务的 JSON | 本服务导出的文件（§8），用于在实例之间搬迁，也用于往返测试 | Markdown | 访客评论能；登录读者的评论导入为 `external` | C3 |
| Waline | 后台导出的 `waline.json`（`{type: "waline", version: 1, data}`），只读其中的 `Comment` 表 | Markdown | 能 | C3 |
| Twikoo | 管理面板导出的 JSON 数组；较老的导出是每行一个 MongoDB 风格的对象 | 净化过的 HTML | 能 | C3 |
| Artrans | Artalk 导出的 JSON 数组，字段全是字符串；Artalk 的转换工具也能把 Typecho、Valine 等转成它 | Markdown | 能 | C3 |
| WordPress | WordPress 后台自带的导出工具生成的 WXR | 原始 HTML | 能 | C3 |
| Disqus | 导出的 XML | HTML | 不能 | C4 |
| Giscus | 没有导出功能，要用 GitHub GraphQL 接口拉取讨论和回复 | Markdown | 用 GitHub 登录认领 | C4 |

流程：

1. **上传**：控制台上传文件，大小上限见 [moderation.md §3.4](moderation.md#34-限额-待定)。
2. **预览**：解析后列出总数、各状态的数量、时间范围、带邮箱的访客评论数、解析失败的条目；按 [widget.md §5](widget.md#5-讨论页标识) 算出每条评论的讨论页标识，列出**对不上站点 origin 的地址**。原系统的页面标识规则各不相同：Waline 用 `decodeURI` 过的路径、不处理末尾斜杠，Twikoo 把 `/p` 和 `/p/` 视为同一页，Artalk 用未解码的路径；统一按我们的规则规范化后，预览里列出多个原标识合并成一个讨论页的情况。
3. **映射**：博主可以加地址改写规则（旧域名 → 新域名、旧路径前缀 → 新前缀），或者给只存了路径的格式指定所属 origin（默认是站点的主 origin）。改完重新预览。WordPress 另有一个选项：讨论页按地址（默认）或按 `wp:<文章 ID>` 标识，后者和 C4 的 WordPress 插件一致。

   实现上的映射是 `{origin, rewrites: [{from, to}], wordpress_keys}`，改写按顺序做前缀替换。预览是 `{total, by_status, first, last, with_email, threads, failures: [{index, reason}], unmatched_origins, unmatched_comments, merged: [{key, pages}]}`。对不上站点 origin 的评论执行时跳过；回复的上级被跳过或在另一个讨论页时，回复改为顶层评论。
4. **执行**：worker 在后台导入，以 `(站点, 来源, 原 ID)` 去重，重复导入不会产生重复评论。
   - 状态：已通过 → `published`，待审 → `pending`，垃圾 → `spam`。WordPress 只导入普通评论（`comment_type` 为空或 `comment`），跳过 pingback、trackback 和编辑备注；回收站里的评论导入为 `deleted`。
   - 结构：多层嵌套压成两层（[data-model.md §3.4](data-model.md#34-讨论页与评论)），保留「回复 @某人」的关系。
   - 保留原时间和置顶；点赞数丢弃（C2 没有点赞）。
   - 讨论页的标题和地址取原数据里的值，没有就用映射后的地址，博主之后可以在讨论页管理里改。
   - 正文：Markdown 原样走 [moderation.md §5](moderation.md#5-内容格式) 的渲染；只有 HTML 的，先转成 Markdown 再渲染，转不了的标签只保留文字。Waline 默认把上传的图片以 base64 内嵌在正文里，一律替换成「[图片]」。
   - **不导入 IP 和 User-Agent**。访客邮箱规范化后加密保存，可以被认领；空邮箱和占位邮箱不能认领。
   - **丢弃原系统的密钥**：Waline 的 `Users` 表（含密码哈希和两步验证密钥）整张不读；Twikoo 的 `uid` 字段不导入。博主本人评论上的 `uid` 等于 Twikoo 管理员密码的 MD5，在自托管的 Twikoo 上能直接当管理员令牌用。
5. **结果**：显示成功、跳过、失败的数量；解析失败的条目在预览里列出序号和原因（下载失败条目还没做）。上传的原始文件可能带着上面这些密钥，按保留期尽早删除（[data-model.md §4](data-model.md#4-保留期)）。

各格式实际读哪些字段（`internal/importer`，2026-10 按源码和样例实现，真实数据验收是 C3 的退出条件）：Waline 只读 `data.Comment` 的 `objectId`、`comment`、`insertedAt`（或 `createdAt`）、`mail`、`nick`、`link`、`url`、`status`、`pid`、`sticky`；Twikoo 读 `_id`、`nick`、`mail`、`link`、`href`（没有时 `url`）、`comment`、`pid`、`created`、`isSpam`、`top`，兼容 MongoDB 风格的 `$oid`、`$numberLong`；Artrans 读 `id`、`rid`、`content`、`date`、`nick`、`email`、`link`、`page_key`、`page_title`、`is_pending`、`is_pinned`；WXR 读每篇文章的 `link`、`title`、`post_id` 和其中的 `comment`，跳过 `comment_type` 不为空且不是 `comment` 的。HTML 转 Markdown 只认识段落、换行、粗体、斜体、删除线、代码、引用、列表和 http/https/mailto 链接，图片换成「[图片]」，脚本和样式整段丢掉。

导入的评论不会自动归到已注册的账号下，要读者自己认领（§6）。

**往返测试**（C3）：导出一个站点，导入一个空实例，比较讨论页（标识、标题、地址、锁定）、评论（正文、状态、时间、置顶、层级关系）、访客资料和作者显示名，必须完全一致；唯一允许的差别是登录读者的评论变成 `external` 作者。

---

## 8. 导出

- **博主**：随时可以导出整个站点：讨论页、全部状态的评论、作者显示名和个人主页、访客填写的邮箱。格式有三种：
  - **本服务的 JSON**，是主格式：带版本号，字段有类型，同时记录 `parent_id` 和 `root_id`、正文格式、包括删除和垃圾在内的状态、讨论页标识和地址、导入来源和原 ID。字段已定稿，见 [docs/export-format.md](../export-format.md)。
  - **Artrans**：Artalk 和 Twikoo 都能导入它。它没有版本号和正式规范，只作为兼容格式提供，不作为主格式。
  - **WXR**（C3，未做）：WordPress 和 Disqus 都能导入它。
- **访客的邮箱给博主，登录读者的邮箱不给**。访客是在这个站点的评论框里留下的邮箱，和博主自己搭 Waline 时能看到的一样；登录读者的身份属于 Kite Plus 账号，博主拿到的是这位读者在本站的作者 ID 和显示名。作者 ID 按站点区分，两个博主的导出之间对不上同一个读者（[data-model.md §3.1](data-model.md#31-账号与身份)）。隐私政策里要写清这一条。
- **读者**：在读者中心随时导出自己的资料和全部评论（JSON）。

「数据随时能带走」是博主愿意把评论交给集中服务的前提（[architecture.md §0.1](architecture.md#0-两个核心判断)），导出在 C2 就要有，不能等到 C3。

---

## 9. 与 Kite 的深度集成（C4）

- **后台**：Kite 后台的仪表盘显示待审核和最新评论，并能直接审核。用站点级 API 令牌（只读、审核两种权限），由 Kite 服务端调用，令牌不下发给浏览器。
- **Webhook**：`comment.published`、`comment.pending` 等事件，带 HMAC 签名，供 Kite 动态模式或博主自己的服务使用。
- **同步回站点文件**：`kite comments pull` 把已发布评论写进站点目录（如 `data/comments/<文章 ID>.json`），主题在静态页面里直接输出评论，组件只负责发表和交互。这样评论也成了博主手里的文件，还能被搜索引擎收录；这也符合 Kite「内容保存在你掌握的文件里」的原则。
