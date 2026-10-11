-- +goose Up
-- The schema of docs/design/data-model.md section 3. Every column is also
-- listed in testdata/schema/columns.txt; see section 5 of that document.

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

CREATE INDEX user_identities_user ON user_identities (user_id);

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
    deleted_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    CHECK ((status = 'deleted') = (deleted_at IS NOT NULL))
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

CREATE TABLE console_sessions (
    token_hash  bytea       PRIMARY KEY,
    user_id     bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    idp_sid     text        NOT NULL CHECK (char_length(idp_sid) <= 200),
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);

CREATE INDEX console_sessions_sid ON console_sessions (idp_sid);
CREATE INDEX console_sessions_user ON console_sessions (user_id);

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

CREATE INDEX login_transactions_expires ON login_transactions (expires_at);

-- An http origin is accepted only by a development server; see
-- docs/design/sites.md section 2.
CREATE TABLE site_origins (
    origin           text        PRIMARY KEY
                                 CHECK (origin ~ '^https?://' AND char_length(origin) <= 300),
    site_id          bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    is_primary       boolean     NOT NULL DEFAULT false,
    verified_at      timestamptz,
    proof_hash       bytea,
    last_checked_at  timestamptz,
    failing_since    timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX site_origins_primary ON site_origins (site_id) WHERE is_primary;
CREATE INDEX site_origins_site ON site_origins (site_id);

CREATE TABLE site_members (
    site_id           bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    user_id           bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role              text        NOT NULL CHECK (role IN ('owner', 'moderator')),
    last_notified_at  timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, user_id)
);

CREATE INDEX site_members_user ON site_members (user_id);

CREATE TABLE site_verifications (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id      bigint      NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    token_hash   bytea       NOT NULL,
    created_by   bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_error   text                 CHECK (char_length(last_error) <= 500)
);

CREATE INDEX site_verifications_site ON site_verifications (site_id);

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
-- For the spam rule on one body posted across sites within an hour.
CREATE INDEX comments_body_recent ON comments (md5(body_md), created_at);

CREATE TABLE comment_revisions (
    comment_id  bigint      NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
    revised_at  timestamptz NOT NULL,
    body_md     text        NOT NULL,
    PRIMARY KEY (comment_id, revised_at)
);

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

CREATE INDEX reports_site ON reports (site_id, status, created_at DESC);
CREATE INDEX reports_comment ON reports (comment_id);

CREATE TABLE bans (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id     bigint               REFERENCES sites (id) ON DELETE CASCADE,
    user_id     bigint               REFERENCES users (id) ON DELETE CASCADE,
    email_hmac  bytea,
    ip_hmac     bytea,
    reason      text        NOT NULL DEFAULT '' CHECK (char_length(reason) <= 500),
    created_by  bigint               REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz,
    CHECK (num_nonnulls(user_id, email_hmac, ip_hmac) = 1)
);

CREATE INDEX bans_site ON bans (site_id);

CREATE TABLE moderation_events (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    site_id        bigint               REFERENCES sites (id) ON DELETE CASCADE,
    comment_id     bigint               REFERENCES comments (id) ON DELETE SET NULL,
    actor_user_id  bigint               REFERENCES users (id) ON DELETE SET NULL,
    action         text        NOT NULL CHECK (char_length(action) <= 40),
    detail         jsonb       NOT NULL DEFAULT '{}',
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX moderation_events_site ON moderation_events (site_id, created_at DESC);
CREATE INDEX moderation_events_comment ON moderation_events (comment_id);

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
    confirm_token_hash  bytea,
    confirm_sent_at     timestamptz,
    confirmed_at        timestamptz,
    opted_out_at        timestamptz,
    locale              text        NOT NULL DEFAULT 'zh-CN' CHECK (locale IN ('zh-CN', 'en')),
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE email_claims (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id        bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email_hmac     bytea       NOT NULL,
    token_hash     bytea       NOT NULL UNIQUE,
    created_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,
    verified_at    timestamptz,
    claimed_count  integer     NOT NULL DEFAULT 0
);

CREATE INDEX email_claims_user ON email_claims (user_id, created_at DESC);

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
CREATE INDEX outbox_sent     ON outbox (coalesce_key, sent_at DESC) WHERE status = 'sent';

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

CREATE INDEX imports_site ON imports (site_id, created_at DESC);

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

-- The platform's list for the spam rule in docs/design/moderation.md
-- section 3.1.
CREATE TABLE spam_domains (
    domain      text        PRIMARY KEY CHECK (char_length(domain) BETWEEN 1 AND 253),
    created_by  bigint               REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
-- Migrations only go forward; see docs/design/architecture.md section 9.
SELECT 1;
