// Dev-only entry: `pnpm dev` swaps the API transport for the in-memory fake.
// Production builds never import this file (see __MOCK__ in vite.config.ts).
import { setTransport } from '@/api/client'
import { createDb, type Db } from './db'
import { createMockTransport } from './server'

const STORAGE_KEY = 'kc-mock-db'

function load(): Db {
  try {
    const saved = sessionStorage.getItem(STORAGE_KEY)
    if (saved) return JSON.parse(saved) as Db
  } catch {
    // Fall through to fresh data.
  }
  return createDb()
}

function save(db: Db) {
  try {
    sessionStorage.setItem(STORAGE_KEY, JSON.stringify(db))
  } catch {
    // The fake then forgets changes on reload, which is fine.
  }
}

let db: Db | undefined

export function installMocks() {
  db = load()
  setTransport(createMockTransport(db, { latency: 250, onWrite: save }))
  console.info('[comments-console] using the in-memory mock API; run `pnpm dev:backend` for the real one')
}

/** Stands in for the /auth/login round trip through the identity service. */
export function mockSignIn() {
  if (!db) return
  if (db.deleted) {
    const fresh = createDb()
    db.me = { ...fresh.me, display_name: 'Reader', homepage_url: null, is_platform_admin: false }
    db.deleted = false
  }
  db.signedIn = true
  save(db)
}

/** Back to the seed data. */
export function resetMocks() {
  try {
    sessionStorage.removeItem(STORAGE_KEY)
  } catch {
    // Nothing saved.
  }
}
