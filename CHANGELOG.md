# Changelog

All notable changes to Kite Plus Comments are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

[简体中文](CHANGELOG.zh-CN.md)

## [Unreleased]

The first code: the service of the invite-only stage, not yet deployed. It runs locally against the identity service's development stack (ZITADEL v4.19.4).

### Added

- Readers sign in from any blog's comment section with one click: a popup goes to the Kite Plus account and closes itself, or, where popups cannot work (in-app browsers, host pages with `Cross-Origin-Opener-Policy: same-origin`), the whole page goes there and comes back after the reader confirms the site. The widget finishes the login by polling, so a login page that cuts the popup off does not break it.
- Each blog remembers the reader on its own, in a partitioned cookie or, where the browser refuses one, in the widget's own storage; the host page can read neither. Signing out works per site, for all sites from the reader center, and from the Kite Plus account, which ends what that sign-in issued.
- `embed.js` (2.7 KB gzip) defines `<kite-comments>`: lazy loading, the page's address and language, light and dark following the host page, an accent color, serif or sans fonts, deep links to `#kc-<id>`, and a pinned version with an SRI hash. The `lang` attribute, the page's `<html lang>` and the browser language map to Chinese for anything starting with `zh` and to English otherwise.
- Comments with two levels of replies, Markdown rendered and sanitized on the server, editing within 24 hours, deleting, pinning, locking a page, reporting, a moderation queue, bans by account, email or IP, blocked words and spam rules, limits from one place in the code.
- Guest comments for sites that allow them, with a proof of work instead of a captcha; reply mail to a guest address starts only after it is confirmed by mail from the same browser.
- Mail for replies (several within ten minutes become one), new comments for site owners at once or as a daily digest at 09:00 Beijing time, and urgent reports; every notification has one-click unsubscribe, and links in mail only open pages.
- Site registration with DNS, HTML meta or file verification, platform approval, settings, deletion with a 30-day recovery window, rechecks every 30 days, and moving a page's comments to a new address with undo.
- Export of a site as JSON (documented in `docs/export-format.md`), Artrans or WordPress WXR; import from this service's JSON, Waline, Twikoo, Artrans and WordPress WXR with a preview and address rewriting; readers can claim the guest comments they left with their email address.
- Reader center and console APIs: my comments (with deleting one, on any site), notifications, profile, signed-in sites, export, deleting this service's data with the reader's choice for their comments.
- The identity service's user events arrive by webhook and by hourly reconciliation and are treated as hints: the user's current state is read first, deactivation suspends and reactivation resumes, locking changes nothing, and data is deleted only when the event search confirms the removal.
- Deployment files: one image for serve, worker and migrate; Compose, with an edge mode for a Caddy shared with other services on the server, where the HTTP server (Compose service `server`) joins the shared `kite-edge` network instead of publishing a port; Caddy routes that strip cookies from reads, used by both the standalone Caddyfile and the edge site files; environment examples; and an age-encrypted backup that can copy itself off the server with rclone.

### Changed

- The widget is drawn in the paper-kite look of the Kite Plus pages, still in the blog's accent and serif or sans font: ink buttons, dashed rules, a stamp for the blog owner, replies hung on a thin string, a sign-in invitation with a kite tied to the card, and a kite over the empty editor that is reeled into the post button when the reader starts writing. The composer is taller and has an emoji tray, Markdown tips, write and preview tabs and Cmd/Ctrl+Enter to post.
- Default avatars are seals with the first letter of the name in one of twelve inks instead of 5x5 pixel patterns; their address now carries the letter (`?c=`), which also retires the cached pixel ones.
- The widget no longer offers guest comments; the guest API and existing or imported guest comments are unchanged.

### Security

Fixes from the 2026-10 security review, before any deployment:

- A platform suspension survives the owner deleting and restoring the site, and an identity service reactivation no longer lifts a platform suspension of a reader.
- Bans name the reader's identity, so deleting the account's data does not lift them; that identifier is kept until the ban ends.
- The console's return path refuses control characters and anything but a local path; the login popup gets a random window name each time.
- Imports need an approved site, are limited per day and in size, are parsed as a stream up to a comment cap, keep only the parsed comments with encrypted addresses for at most 24 hours, and age from the import, so imported spam can still be restored.
- Only a verified origin belongs to a site; unverified claims expire with their token, and an account may own at most 10 sites and create 5 a day.
- Reconciliation gets past bursts of identity events, pushed events no longer mark an event ID as handled, and an event's type never decides anything: `COMMENTS_IDP_WEBHOOK_SECRET` without `COMMENTS_IDP_API_TOKEN` refuses to start.
- Guest reply mail needs consent from the same browser; queued mail is dropped with the account and rechecked against the reader's current settings when it is sent; SMTP errors are logged without addresses.
- The site session cookie slides with the session, banned readers cannot edit, page titles from the first commenter are cut and cleaned in mail, future page dates are refused, the spam-domain rule also checks homepages and internationalized names, exports leave out the text of comments their authors deleted, a claim applies once, and an account's email follows the identity service at each sign-in.
- `COMMENTS_ALLOW_PRIVATE_NETWORKS` refuses an https public URL, verification errors no longer show resolved addresses, and console pages are served only with their CSP.
