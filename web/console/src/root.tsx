import { ScrollRestoration, useLocation } from 'react-router'
import { useAuth } from '@/context/auth'
import { AppShell } from '@/components/layout/app-shell'
import {
  AuthErrorScreen,
  CheckingScreen,
  DeletedScreen,
  SignInScreen,
} from '@/features/auth/screens'
import { ClaimSignedOut } from '@/features/me/claim-signed-out'

/** Decides between the sign-in screen and the app for every route. */
export function Root() {
  const { state } = useAuth()
  const { pathname } = useLocation()

  let content: React.ReactNode
  switch (state.status) {
    case 'loading':
      content = <CheckingScreen />
      break
    case 'signed-out':
      // A claim link opened without the right session must not look like it
      // could succeed (api.md section 6).
      content = /^\/me\/claims\/[^/]+$/.test(pathname) ? (
        <ClaimSignedOut />
      ) : (
        <SignInScreen reason={state.reason} />
      )
      break
    case 'deleted':
      content = <DeletedScreen />
      break
    case 'error':
      content = <AuthErrorScreen error={state.error} />
      break
    case 'signed-in':
      content = <AppShell />
  }

  return (
    <>
      <ScrollRestoration />
      {content}
    </>
  )
}
