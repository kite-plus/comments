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
| `deploy` | Dockerfile, Compose files (standalone and edge mode), Caddy site files, environment examples |
| `scripts` | Import boundary check, encrypted backups |

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

## Deployment

Not deployed yet. Production and staging will run on the US server that also hosts Explore and the identity service, behind Explore's Caddy, the only thing on ports 80 and 443 there ([architecture.md section 9](docs/design/architecture.md#9-部署与运维)).

- `deploy/docker-compose.yaml` runs PostgreSQL, the migration, the HTTP server (service `server`) and the worker. With `COMPOSE_FILE=docker-compose.yaml:docker-compose.edge.yaml` in `deploy/.env` (edge mode, as in production), the server publishes no port and joins the shared `kite-edge` network as `comments`, trusting `X-Forwarded-For` from that network only. Without it (standalone mode), the server listens on `127.0.0.1:8081` for a reverse proxy on the same host, and `deploy/Caddyfile` is the site block.
- `deploy/edge/comments.caddy` and `comments-routes.caddyfile` go into Explore's `sites/` directory, then `docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile` there. The routes file holds the Cookie rules for both modes.
- No workflow publishes the image yet. Build it in the checkout on the server, with `COMMENTS_VERSION` in `deploy/.env` set to the same tag:

  ```sh
  git clone https://github.com/kite-plus/comments /opt/comments && cd /opt/comments
  docker build -f deploy/Dockerfile --build-arg VERSION=$(git describe --tags --always) -t ghcr.io/kite-plus/comments:<tag> .
  cp deploy/.env.example deploy/.env && chmod 600 deploy/.env   # fill in, uncomment COMPOSE_FILE
  cd deploy && docker compose up -d --wait
  ```

- Staging is a second checkout (`/opt/comments-staging`) whose `.env` also sets `COMPOSE_PROJECT_NAME`, `COMMENTS_EDGE_ALIAS` and `COMMENTS_SUBNET` (see `deploy/.env.example`); its Caddy file is `deploy/edge/comments-staging.caddy`. It runs only for tests and upgrades.
- `scripts/backup.sh` (daily from cron) writes an age-encrypted dump to `BACKUP_DIR` and, with `BACKUP_REMOTE` set, copies it off the server with rclone. The keys in `.env` are not in it; keep them offline. To restore into an empty stack of the same version:

  ```sh
  age -d -i age.key /var/backups/comments/comments-20261011T031500Z.tar.age | tar -xf - comments.dump
  cd deploy && docker compose up -d --wait postgres
  docker compose exec -T postgres pg_restore -U comments -d comments --no-owner --exit-on-error < ../comments.dump
  docker compose up -d --wait
  ```

## License

Apache-2.0, see [LICENSE](LICENSE).
