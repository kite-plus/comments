import { useLocation } from 'react-router'
import { Database, Loader2, LogIn, ServerCrash } from 'lucide-react'
import { useAuth } from '@/context/auth'
import { useI18n } from '@/i18n'
import { IDENTITY_URL } from '@/lib/policy'
import { errorMessage } from '@/hooks/use-api'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { LanguageSwitch, ThemeSwitch } from '@/components/layout/preferences'
import { useDocumentTitle } from '@/hooks/use-title'

/** A centered card for the screens shown before or instead of the app. */
export function StandaloneLayout({ children }: { children: React.ReactNode }) {
  const { t } = useI18n()
  return (
    <div className='flex min-h-svh flex-col'>
      <header className='flex items-center justify-between px-4 py-3'>
        <span className='flex items-center gap-2 text-sm font-semibold'>
          <img src={`${import.meta.env.BASE_URL}favicon.svg`} alt='' width={22} height={18} />
          {t('app.name')}
        </span>
        <div className='flex items-center'>
          <LanguageSwitch />
          <ThemeSwitch />
        </div>
      </header>
      <main id='main' className='flex flex-1 items-center justify-center px-4 pb-16'>
        <div className='w-full max-w-md space-y-6 rounded-xl border bg-card p-6 shadow-sm sm:p-8'>
          {children}
        </div>
      </main>
    </div>
  )
}

export function CheckingScreen() {
  const { t } = useI18n()
  return (
    <div
      role='status'
      className='flex h-svh items-center justify-center gap-2 text-sm text-muted-foreground'
    >
      <Loader2 className='size-4 animate-spin' aria-hidden='true' />
      {t('auth.checking')}
    </div>
  )
}

export function SignInScreen({ reason }: { reason: 'none' | 'expired' | 'signed-out' }) {
  const { t } = useI18n()
  const { signIn } = useAuth()
  const location = useLocation()
  useDocumentTitle(undefined)
  return (
    <StandaloneLayout>
      <div className='space-y-2'>
        <h1 className='text-2xl font-bold tracking-tight'>{t('auth.signInTitle')}</h1>
        <p className='text-muted-foreground'>{t('auth.signInDesc')}</p>
      </div>
      {reason !== 'none' && (
        <Alert role='status'>
          <AlertDescription>
            {reason === 'expired' ? t('auth.expired') : t('auth.signedOut')}
          </AlertDescription>
        </Alert>
      )}
      <Button className='w-full' size='lg' onClick={() => signIn(location.pathname + location.search)}>
        <LogIn />
        {t('auth.signInButton')}
      </Button>
      <p className='text-xs text-muted-foreground'>{t('auth.signInNote')}</p>
    </StandaloneLayout>
  )
}

export function AuthErrorScreen({ error }: { error: unknown }) {
  const { t } = useI18n()
  const { retry } = useAuth()
  return (
    <StandaloneLayout>
      <ServerCrash className='size-8 text-muted-foreground' aria-hidden='true' />
      <div className='space-y-2' role='alert'>
        <h1 className='text-xl font-bold tracking-tight'>{t('auth.errorTitle')}</h1>
        <p className='text-muted-foreground'>{errorMessage(t, error)}</p>
      </div>
      <Button onClick={retry}>{t('common.retry')}</Button>
    </StandaloneLayout>
  )
}

export function DeletedScreen() {
  const { t } = useI18n()
  const { signIn } = useAuth()
  useDocumentTitle(t('deleted.title'))
  return (
    <StandaloneLayout>
      <Database className='size-8 text-muted-foreground' aria-hidden='true' />
      <div className='space-y-2'>
        <h1 className='text-xl font-bold tracking-tight'>{t('deleted.title')}</h1>
        <p className='text-muted-foreground'>{t('deleted.desc')}</p>
      </div>
      <div className='flex flex-wrap gap-2'>
        <Button asChild>
          <a href={IDENTITY_URL} target='_blank' rel='noopener noreferrer'>
            {t('data.idpButton')}
          </a>
        </Button>
        <Button variant='outline' onClick={() => signIn('/me')}>
          {t('deleted.signInAgain')}
        </Button>
      </div>
    </StandaloneLayout>
  )
}
