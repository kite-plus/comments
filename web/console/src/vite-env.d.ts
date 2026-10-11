/// <reference types="vite/client" />

/** True in `pnpm dev`, where the in-memory fake replaces the server. */
declare const __MOCK__: boolean

interface ImportMetaEnv {
  /** Where the "delete your Kite Plus account" link points. */
  readonly VITE_IDENTITY_URL?: string
}
