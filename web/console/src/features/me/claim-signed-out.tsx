import { useLocation } from 'react-router'
import { LogIn } from 'lucide-react'
import { useAuth } from '@/context/auth'
import { useI18n } from '@/i18n'
import { Button } from '@/components/ui/button'
import { StandaloneLayout } from '@/features/auth/screens'

/** A claim link opened without a session: say so, and offer sign-in. */
export function ClaimSignedOut() {
  const { t } = useI18n()
  const { signIn } = useAuth()
  const location = useLocation()
  return (
    <StandaloneLayout>
      <h1 className='text-xl font-bold tracking-tight'>{t('claimVerify.title')}</h1>
      <p role='status'>{t('claimVerify.notYours')}</p>
      <p className='text-sm text-muted-foreground'>{t('claimVerify.signInHint')}</p>
      <Button variant='outline' onClick={() => signIn(location.pathname + location.search)}>
        <LogIn />
        {t('auth.signInButton')}
      </Button>
    </StandaloneLayout>
  )
}
