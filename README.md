# Kite Plus Comments

One Kite Plus account to comment on any participating blog.

Readers sign in once at `id.kite.plus` and comment on every blog that embeds the widget, with one click per blog. They can see all their comments and replies in one place. Bloggers add two lines of HTML, stop running their own comment backend, and can export everything at any time.

```html
<kite-comments site="SITE-ID"></kite-comments>
<script src="https://comments.kite.plus/embed.js" crossorigin="anonymous" async></script>
```

The service is not deployed yet. The backend and the widget of the invite-only stage (C2) are written and run locally against the identity service's development stack; parts of the open stage (C3) are in too. The reader center and site console are built on a separate branch. The design, written in Chinese like the other Kite Plus projects, is in [docs/design](docs/design/README.md); [roadmap.md](docs/design/roadmap.md) says honestly what is done.

## Layout

| Path | What |
|---|---|
| `cmd/comments`, `internal/` | The Go service: `serve` (HTTP), `worker` (mail, cleanup, reconciliation, imports), operator commands |
| `web/widget` | `embed.js`, the iframe app and the login popup's script (Preact, TypeScript, Vite) |
| `web/console` | Reader center and site console (React); only its build placeholder lives on `main` |
| `migrations` | PostgreSQL schema (goose); `testdata/schema/columns.txt` guards it |
| `e2e` | Browser tests with Playwright and the installed Google Chrome, their harness, and the export and import round trip |
| `deploy` | Dockerfile, Compose files, Caddyfile, environment examples |

The Go binary embeds `web/widget/dist` and `web/console/dist`; both directories hold a committed `.gitkeep`, so `go build` works without Node. Run `make web` first for a binary that serves the widget.

## Local development

Requirements: Go 1.26, Docker, Node 22 with pnpm 10, Google Chrome for the browser tests. Ports: 8081 (service), 5434 (PostgreSQL), 8301 and 8302 (test blogs).

```sh
make db-up                                  # PostgreSQL on 127.0.0.1:5434
cp deploy/.env.dev.example deploy/.env.dev  # then fill in the identity values, below
make web                                    # build the widget into web/widget/dist
make dev-seed                               # migrate and register the two test blogs
make dev                                    # the service on http://localhost:8081
make dev-worker                             # mail through Mailpit, reconciliation, imports
make dev-blogs                              # http://blog-a.localhost:8301 and http://blog-b.localhost:8302
```

The identity service runs from the `kite-plus/identity` checkout (`make dev-up dev-init` there). Copy the `COMMENTS_*` lines of `identity/dev/out/comments.env` into `deploy/.env.dev`; its issuer may not be the default port (see `identity/dev/out/NOTICE.txt`). Test users are in `identity/dev/out/users.txt`. Mail goes to Mailpit at `localhost:1025` (web UI on `http://localhost:8025`); when Mailpit is down, mail just waits in the outbox.

`deploy/.env.dev` turns on two settings that exist only for development and are refused with an `https` public URL: `COMMENTS_DEV_HTTP_ORIGINS` lets sites use `http` origins such as the test blogs, and `COMMENTS_ALLOW_PRIVATE_NETWORKS` lets site verification reach local addresses. On plain `http`, cookies drop the `__Host-` prefix.

To own blog A in the widget and the console, sign in once, then `make dev-seed SEED_ARGS='--owner-email alice@example.com'`. Platform admins: `go run ./cmd/comments users promote-admin <email>` with the dev environment loaded.

## Checks and tests

```sh
make check      # gofmt, vet, import boundaries, go mod tidy, golangci-lint, go test
make web-test   # vitest for the widget
make e2e        # browser tests against the harness (fake identity service), in Chrome
make e2e-real   # the browser test against the real local identity stack and make dev
```

`make test` runs the store, protocol and round-trip tests when the development database answers on port 5434 (or `COMMENTS_TEST_DATABASE_URL` is set) and skips them otherwise. The browser tests use `channel: "chrome"`; Playwright's own browsers are never downloaded (`PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1`). `make e2e-real` needs `make dev`, `make dev-worker` and `make dev-blogs` running; it creates and deletes a throwaway user with the identity stack's dev admin token.

## License

Apache-2.0, see [LICENSE](LICENSE).
