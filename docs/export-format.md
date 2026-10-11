# 导出格式（版本 1）

> 状态：C2 定稿 · 最近更新：2026-10-11
> 博主在控制台导出整个站点（`GET /api/v1/sites/{site}/export?format=json`，[api.md §7](design/api.md#7-站点控制台)）得到这个格式的 JSON 文件。它是本服务的主格式：带版本号，字段有类型，能原样导入另一个实例（[sites.md §7](design/sites.md#7-导入)、[§8](design/sites.md#8-导出)）。

## 顶层

```json
{
  "format": "kite-comments",
  "version": 1,
  "exported_at": "2026-10-11T08:00:00Z",
  "site": {"id": "站点 ID", "name": "某某的博客", "origins": ["https://blog.example.com"]},
  "threads": [讨论页…],
  "comments": [评论…]
}
```

- `format` 永远是 `kite-comments`；`version` 是整数，不兼容的改动才加一。导入只接受认识的版本。
- 时间一律是 UTC 的 RFC 3339。ID 都是字符串，只在一个文件里有意义：导入时按 `(站点, 来源 json, 原 ID)` 去重，重复导入不会产生重复评论。

## 讨论页

```json
{"id": "77", "key": "p:/posts/hello", "url": "https://blog.example.com/posts/hello/", "title": "你好",
 "published_at": "2026-09-01T00:00:00Z", "locked": false, "created_at": "2026-09-02T03:04:05Z"}
```

| 字段 | 说明 |
|---|---|
| `key` | 讨论页标识，规则见 [widget.md §5](design/widget.md#5-讨论页标识) |
| `url`、`title` | 第一条评论创建讨论页时记下的地址和标题，或管理者改过的 |
| `published_at` | 文章的发布时间，用于自动关闭；没有时为 `null` |
| `locked` | 是否锁定 |

## 评论

```json
{
  "id": "18342", "thread_id": "77", "parent_id": null, "root_id": null,
  "author": {"kind": "guest", "name": "访客", "url": "https://example.org", "email": "a@b.example"},
  "body": {"format": "markdown", "source": "评论的 **Markdown** 原文"},
  "status": "published", "pinned": false,
  "created_at": "…", "edited_at": null, "published_at": "…",
  "source": null
}
```

| 字段 | 说明 |
|---|---|
| `parent_id`、`root_id` | 两层结构：顶层评论两者都为 `null`；回复的 `root_id` 是所在顶层评论，`parent_id` 是被回复的那条（[data-model.md §3.4](design/data-model.md#34-讨论页与评论)） |
| `author.kind` | `user` 登录读者、`guest` 访客、`external` 导入的没有可认领邮箱的作者、`former_user` 已注销 |
| `author.id` | 只有 `user` 有：这位读者在**这个站点**的作者 ID，和组件里看到的一样，别的站点的导出里对不上 |
| `author.name`、`author.url` | 显示名和个人主页；`former_user` 的 `name` 是空串 |
| `author.email` | 只有访客有：访客在这个站点的评论框里留下的邮箱。登录读者的邮箱永远不导出（[sites.md §8](design/sites.md#8-导出)） |
| `author.ref` | 导入来源记下的作者引用，比如 Giscus 的 GitHub 用户名（C4） |
| `body` | `format` 现在只有 `markdown`；`source` 是原文，HTML 由导入方重新渲染 |
| `status` | `published`、`pending`、`spam`、`hidden`、`deleted`；删除和垃圾也导出。`deleted` 的评论只是占位，`body.source` 一律是空串：作者删掉的内容不再交给博主 |
| `pinned` | 是否置顶，只有顶层评论可能为 `true` |
| `source` | 从别的系统导入的评论记下 `{"system": "waline", "id": "原 ID"}`，原生评论为 `null` |

## 导入本格式

- 登录读者的评论导入为 `external` 作者，保留显示名和个人主页：账号属于原来的实例。访客评论保留邮箱（加密保存），可以被认领。
- 导入不读也不写 IP 和 User-Agent，本格式里也没有这两项。
- 往返测试（`e2e/roundtrip`）导出一个站点、导入一个空实例再导出，比较讨论页（标识、标题、地址、锁定、发布时间）和评论（正文、状态、时间、置顶、层级、作者显示名、个人主页、访客邮箱）必须完全一致，唯一的差别是 `user` 变成 `external`。
