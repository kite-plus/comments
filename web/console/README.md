# Reader center and site console

The single-page app behind `comments.kite.plus/me` (reader center) and `comments.kite.plus/console` (site console). It is specified by [docs/design](../../docs/design/README.md): [api.md](../../docs/design/api.md) for every call, [login.md](../../docs/design/login.md) for sessions, sign-out and account deletion, [sites.md](../../docs/design/sites.md) and [moderation.md](../../docs/design/moderation.md) for what the console offers.

React 19, TypeScript, Vite, Tailwind CSS v4, shadcn/ui (Radix), TanStack Query and React Router. The look, tokens and UI components follow Explore's admin (`explore/web/src/admin`), which ports [shadcn-admin](https://github.com/satnaing/shadcn-admin) (MIT, see `src/LICENSE-shadcn-admin.txt`).

## Run

Node 22.12 or later and pnpm 10.

```sh
pnpm install
pnpm dev            # http://localhost:5180/me, against the in-memory mock API
pnpm dev:backend    # the same, proxying /api, /auth and /avatars to http://localhost:8081
```

`COMMENTS_BACKEND=http://host:port pnpm dev:backend` points the proxy elsewhere. Against a real server, sign in through `/auth/login` as usual; the console session cookie is first-party on the dev origin through the proxy. A server on plain http has to issue cookies without the `__Host-` prefix, which the Go server does in development.

The mock keeps its data in `sessionStorage`, so changes survive reloads in a tab. Clear site data (or open a new tab) to start from the seed in `src/mocks/db.ts`. In the mock, "Sign in" signs straight in, and hosts containing `fail` never pass site verification.

## Check

```sh
pnpm test        # vitest + Testing Library, against the mock API
pnpm typecheck
pnpm lint        # ESLint with typescript-eslint, react-hooks and jsx-a11y
pnpm build       # typecheck, then dist/
```

Tests render the whole app at a route on a fresh mock backend (`src/test/render.tsx`) and assert on both the screen and the requests sent. No browser is downloaded.

## Build output and serving

`pnpm build` writes `dist/` with Vite `base: '/static/console/'`:

- The Go server answers `GET /me`, `/me/*`, `/console` and `/console/*` with `dist/index.html`, and serves `/static/console/*` from `dist/`. Files under `assets/` have hashed names and can be cached for good; `index.html`, `theme-init.js` and `favicon.svg` cannot.
- Client routes live under `/me` and `/console`. `vite dev` and `vite preview` rewrite those paths to `index.html` the same way.
- Every page is its own lazy chunk. The production build contains no mock code (`__MOCK__` is false outside `pnpm dev`).
- CSP: scripts are same-origin files only (`theme-init.js` sets the saved theme and language before the first paint, so no inline script is needed). Radix and sonner insert `<style>` elements at runtime, so the page needs `style-src 'self' 'unsafe-inline'`. Images are same-origin (`/avatars/...`) or `data:`. Downloads use `blob:` URLs on an anchor.

## How it talks to the server

- `src/api/client.ts`: one `fetch` wrapper. Same-origin, cookies included, `Accept-Language` set to the interface language so error messages match, `X-CSRF-Token` on every non-GET request (the token comes from `GET /api/v1/me`, as in Explore). Errors become `ApiError` with the api.md section 10 code; a 401 after sign-in sends the reader back to the sign-in screen.
- `src/api/index.ts`: every endpoint the screens call, typed by `src/api/types.ts`.
- `src/api/normalize.ts`: maps responses onto those types. Where api.md leaves a field name open it accepts both the name assumed here and the one the Go server on main uses, so either works.
- Sign-in navigates to `/auth/login?return=<path>`; sign-out is `POST /auth/logout`.

## Language and theme

zh-CN and en. The interface follows a choice saved in this browser, else the account's language from `/api/v1/me`, else the browser's languages. Catalogs are `src/i18n/zh-CN.ts` (the reference; TypeScript makes `en.ts` cover every key) and `src/i18n/en.ts`. The theme follows the system unless light or dark is chosen, stored under `localStorage.theme` as in Explore.

## API assumptions

api.md does not fix these; the console assumes what follows and the mock implements it. Items marked "server" match the Go server on main as of this branch.

| Where | Assumption |
|---|---|
| `GET /api/v1/me` | Returns `csrf_token`, `email`, `is_platform_admin`, and an avatar URL as `avatar_url` or `avatar` (server). |
| Lists | `limit` is the page-size parameter. A list without `next_cursor` is read as one page (server: sites, sessions, bans). |
| `GET /api/v1/me/comments` | Items carry `site {id, name}`, `thread {id, title, url}` and optionally `url`, the `#kc-` permalink (server). |
| `GET /api/v1/me/notifications` | Items carry the reply as `comment` with its site, thread, author and `body_html` (server), or flat `site`/`thread`/`comment.excerpt`; the unread total is `unread` (server) or `unread_count`. Muted threads come from `GET /api/v1/me/mutes` (server, not in api.md) when present. |
| `GET /api/v1/me/site-sessions` | Items carry `site {id, name, origin}`, `client` (server) or `client_label`, `created_via`, `last_used_at`, `expires_at`. |
| `POST /api/v1/me/claims` | May answer `{id, verified}`; `verified` means the address is the account's own (sites.md 6, step 6), confirmed without mail. `GET /api/v1/me/claims/suggestion` (server, not in api.md) offers those comments. |
| `POST /api/v1/sites` | Body `{name, origin, aliases, guest_policy}`; C2 always sends `guest_policy: "off"`. The response may include the first `verification {token, expires_at}`; otherwise the owner generates one on the verify page. |
| `GET /api/v1/sites` | Includes deleted sites in their recovery window, with a role, `pending_count` and `open_reports`/`reported_count` (server). The restore deadline is `purge_at`, else `deleted_at` + 30 days. |
| `PATCH /api/v1/sites/{site}` | Turning auto-close off sends `close_after_days: null` and `clear_close_after_days: true` (server). |
| `DELETE .../origins/{origin}` | The origin is percent-encoded as one path segment (server). |
| `POST .../verification/check` | Answers `{results: [{origin, ok or verified, method, detail}], site}`; a 422 `verification_failed` carries the same body when no origin passed (server). |
| `GET .../comments` | `status` may be `reported` (server); items include `thread {id, title, url}` and the guest's email as `guest_email` (server) or `author.email`. Moderators cannot delete others' comments (moderation.md 1.5), so the queue offers approve, hide, spam, restore, pin and unpin. |
| `PATCH .../threads/{thread}` | Fields `title`, `url`, `published_at`, `clear_published_at`, `locked` (server). |
| `POST .../threads/{thread}/move` | Body `{to_key}`; the response may say how many comments moved. |
| `POST .../bans` | Body `{kind: user, email or ip, comment_id or email, reason, days}`, `days: 0` for no end (server). Ban items carry `kind`/`target` and `name`/`label`; `site_id: null` marks a platform ban the site cannot lift. |
| `PATCH .../reports/{id}` | Body `{status: resolved or dismissed, action?: hide or spam}` (server). |
| `POST .../events/{id}/undo` | Undoes a thread move (server, not in api.md). |
| Platform admin | `GET /api/v1/admin/sites?status=` and `POST /api/v1/admin/sites/{site}/approve`, `/suspend` (`{note}`), `/unsuspend` (server). |
| `GET /api/v1/embed-info` | `{version, pinned_url, integrity}` for the pinned embed snippet (server, not in api.md); the tab is hidden without it. |
| Identity link | "Delete your Kite Plus account" opens `VITE_IDENTITY_URL`, default `https://id.kite.plus/`. |

## Not here yet

- Imports (C3): the API exists in api.md section 7 but has no screen.
- Members and moderator invitations (C4): no API.
- The platform report queue, user suspension, platform bans and the spam-domain list from api.md section 8: only site review is built.
