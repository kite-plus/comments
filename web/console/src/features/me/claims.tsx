import { useState } from 'react'
import { MailCheck } from 'lucide-react'
import * as api from '@/api'
import { useI18n } from '@/i18n'
import { useApiMutation } from '@/hooks/use-api'
import { PageHeader, Spinner } from '@/components/common'
import { Field, Section, describedBy } from '@/components/common/form'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

/** sites.md section 6: start a claim; the server always answers 202. */
export default function ClaimsPage() {
  const { t } = useI18n()
  const [email, setEmail] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [sentTo, setSentTo] = useState<string | null>(null)
  const start = useApiMutation((address: string) => api.me.startClaim(address), {
    onSuccess: (_, address) => setSentTo(address),
  })

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const value = email.trim()
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(value)) {
      setError(t('validation.email'))
      return
    }
    setError(null)
    start.mutate(value)
  }

  return (
    <>
      <PageHeader title={t('claims.title')} description={t('claims.desc')} />
      <Section title={t('claims.formTitle')} description={t('claims.note')}>
        {sentTo ? (
          <Alert role='status'>
            <MailCheck />
            <AlertDescription>{t('claims.sent', { email: sentTo })}</AlertDescription>
          </Alert>
        ) : (
          <form onSubmit={submit} noValidate className='space-y-4'>
            <Field id='claim-email' label={t('claims.email')} error={error}>
              <Input
                {...describedBy('claim-email', error)}
                type='email'
                autoComplete='email'
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </Field>
            <Button type='submit' disabled={start.isPending}>
              {start.isPending && <Spinner />}
              {t('claims.submit')}
            </Button>
          </form>
        )}
      </Section>
    </>
  )
}
