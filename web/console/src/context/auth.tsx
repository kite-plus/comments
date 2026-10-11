import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import * as api from '@/api'
import type { Me } from '@/api'
import { useI18n } from '@/i18n'
import { browser } from '@/lib/browser'

export type AuthState =
  | { status: 'loading' }
  | { status: 'signed-out'; reason: 'none' | 'expired' | 'signed-out' }
  | { status: 'signed-in'; me: Me }
  | { status: 'deleted' }
  | { status: 'error'; error: unknown }

type AuthContextValue = {
  state: AuthState
  /** The signed-in reader; throws outside the signed-in screens. */
  me: Me
  /** Opens /auth/login, which returns here after the identity service. */
  signIn: (returnTo?: string) => void
  signOut: () => Promise<void>
  /** After DELETE /api/v1/me the console session is gone as well. */
  markDeleted: () => void
  retry: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export const ME_KEY = ['me'] as const

function currentPath() {
  return window.location.pathname + window.location.search
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const queryClient = useQueryClient()
  const { adoptAccountLang } = useI18n()
  const [ended, setEnded] = useState<'expired' | 'signed-out' | 'deleted' | null>(null)

  const query = useQuery({
    queryKey: ME_KEY,
    queryFn: api.me.get,
    enabled: ended === null,
    staleTime: 60_000,
    retry: (count, error) =>
      !(api.isApiError(error) && error.status < 500) && count < 2,
  })

  const me = query.data
  useEffect(() => {
    if (!me) return
    api.setCsrfToken(me.csrf_token)
    adoptAccountLang(me.locale)
  }, [me, adoptAccountLang])

  // Any 401 after the first load means the session ended elsewhere: back
  // to the sign-in screen, and drop everything cached for the old session.
  useEffect(() => {
    api.setUnauthorizedHandler(() => {
      if (!queryClient.getQueryData(ME_KEY)) return
      setEnded('expired')
      queryClient.removeQueries()
    })
    return () => api.setUnauthorizedHandler(undefined)
  }, [queryClient])

  const signIn = useCallback((returnTo = currentPath()) => {
    if (__MOCK__) {
      void import('@/mocks').then((mocks) => {
        mocks.mockSignIn()
        browser.assign(returnTo)
      })
      return
    }
    browser.assign(api.loginUrl(returnTo))
  }, [])

  const signOut = useCallback(async () => {
    try {
      await api.auth.logout()
    } catch (error) {
      // Already gone on the server counts as signed out.
      if (!api.isApiError(error, 'unauthorized')) throw error
    }
    api.setCsrfToken(undefined)
    setEnded('signed-out')
    queryClient.removeQueries()
  }, [queryClient])

  const markDeleted = useCallback(() => {
    api.setCsrfToken(undefined)
    setEnded('deleted')
    queryClient.removeQueries()
  }, [queryClient])

  const retry = useCallback(() => {
    setEnded(null)
    void query.refetch()
  }, [query])

  const state: AuthState = useMemo(() => {
    if (ended === 'deleted') return { status: 'deleted' }
    if (ended) return { status: 'signed-out', reason: ended }
    if (me) return { status: 'signed-in', me }
    if (query.isPending) return { status: 'loading' }
    if (api.isApiError(query.error) && query.error.status === 401)
      return { status: 'signed-out', reason: 'none' }
    return { status: 'error', error: query.error }
  }, [ended, me, query.isPending, query.error])

  const value = useMemo<AuthContextValue>(
    () => ({
      state,
      get me() {
        if (state.status !== 'signed-in') throw new Error('Not signed in')
        return state.me
      },
      signIn,
      signOut,
      markDeleted,
      retry,
    }),
    [state, signIn, signOut, markDeleted, retry]
  )

  return <AuthContext value={value}>{children}</AuthContext>
}

export function useAuth() {
  const context = useContext(AuthContext)
  if (!context) throw new Error('useAuth must be used within AuthProvider')
  return context
}

/** The signed-in reader, for screens rendered only after sign-in. */
export function useMe() {
  return useAuth().me
}
