-- +goose Up
-- Fixes from the 2026-10 security review; docs/design/data-model.md
-- describes the result.

-- A platform suspension survives the owner deleting and restoring the
-- site: the marker is kept apart from the status.
ALTER TABLE sites ADD COLUMN suspended_at timestamptz;
UPDATE sites SET suspended_at = updated_at WHERE status = 'suspended';

-- The identity service's suspensions are kept apart from the platform's,
-- so reactivating an account never lifts a platform suspension.
ALTER TABLE users ADD COLUMN idp_suspended_at timestamptz;
UPDATE users SET idp_suspended_at = suspended_at, suspended_at = NULL, suspend_reason = NULL
WHERE suspend_reason LIKE 'idp:%';

-- A reader's ban names the identity, not the account row, so it survives
-- the reader deleting their data and applies when they sign in again.
ALTER TABLE bans ADD COLUMN issuer text CHECK (char_length(issuer) <= 300),
                 ADD COLUMN subject text CHECK (char_length(subject) <= 300);
UPDATE bans b SET issuer = i.issuer, subject = i.subject
FROM (SELECT DISTINCT ON (user_id) user_id, issuer, subject FROM user_identities ORDER BY user_id, linked_at) i
WHERE i.user_id = b.user_id;
DELETE FROM bans WHERE user_id IS NOT NULL AND subject IS NULL;
ALTER TABLE bans DROP COLUMN user_id;
ALTER TABLE bans ADD CHECK (num_nonnulls(subject, email_hmac, ip_hmac) = 1),
                 ADD CHECK ((issuer IS NULL) = (subject IS NULL));
CREATE INDEX bans_identity ON bans (issuer, subject) WHERE subject IS NOT NULL;

-- Only a verified origin is exclusive; an unverified one blocks nobody and
-- expires with its verification token.
ALTER TABLE site_origins DROP CONSTRAINT site_origins_pkey;
ALTER TABLE site_origins ADD PRIMARY KEY (site_id, origin);
DROP INDEX site_origins_site;
CREATE UNIQUE INDEX site_origins_verified ON site_origins (origin) WHERE verified_at IS NOT NULL;

-- A guest's consent to reply mail is per browser: the widget's guest
-- token, confirmed by mail (moderation.md section 6.1).
ALTER TABLE guest_emails DROP COLUMN confirm_token_hash;
CREATE TABLE guest_consents (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email_hmac          bytea       NOT NULL,
    browser_hash        bytea,
    comment_id          bigint               REFERENCES comments (id) ON DELETE CASCADE,
    confirm_token_hash  bytea       NOT NULL UNIQUE,
    created_at          timestamptz NOT NULL DEFAULT now(),
    confirmed_at        timestamptz
);
CREATE INDEX guest_consents_browser ON guest_consents (email_hmac, browser_hash) WHERE confirmed_at IS NOT NULL;

-- A claim is confirmed once.
ALTER TABLE email_claims ADD COLUMN confirmed_at timestamptz;

-- +goose Down
-- Migrations only go forward; see docs/design/architecture.md section 9.
SELECT 1;
