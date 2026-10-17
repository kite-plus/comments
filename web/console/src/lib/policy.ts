// Product numbers the console shows or checks before the server does. The
// single source is the server's internal/policy (moderation.md 3.4,
// data-model.md 4); keep these in step.
export const POLICY = {
  aliasOrigins: 5,
  blockedWords: 200,
  queryKeys: 5,
  closeAfterDaysMax: 3650,
  siteRestoreDays: 30,
  displayNameMax: 40,
  homepageMax: 300,
  siteNameMax: 100,
  verificationTokenDays: 7,
  publishedFutureHours: 24,
} as const

/** Stages of the roadmap the console cares about; flip these when the server ships them. */
export const FEATURES = {
  claims: true,
} as const

export const IDENTITY_URL = import.meta.env.VITE_IDENTITY_URL ?? 'https://id.kite.plus/'

/** Deleted sites are kept this long (data-model.md 4) when purge_at is missing. */
export function purgeDate(deletedAt: string, purgeAt?: string | null) {
  if (purgeAt) return purgeAt
  return new Date(new Date(deletedAt).getTime() + POLICY.siteRestoreDays * 86_400_000).toISOString()
}

/**
 * Parses an origin the way sites.md section 2 describes: scheme, host and
 * port only, lower case, punycode for IDNs, https only.
 */
export function parseOrigin(raw: string): { origin: string } | { error: 'format' | 'https' } {
  const value = raw.trim()
  if (!value) return { error: 'format' }
  let url: URL
  try {
    url = new URL(value)
  } catch {
    return { error: 'format' }
  }
  if (url.protocol !== 'https:') return { error: url.protocol === 'http:' ? 'https' : 'format' }
  if ((url.pathname !== '/' && url.pathname !== '') || url.search || url.hash || url.username)
    return { error: 'format' }
  return { origin: url.origin.toLowerCase() }
}
