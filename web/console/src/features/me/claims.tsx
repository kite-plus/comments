import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { CircleCheck, MailCheck } from 'lucide-react'
import * as api from '@/api'
import { useMe } from '@/context/auth'
import { useI18n } from '@/i18n'
import { useApiMutation } from '@/hooks/use-api'
import { PageHeader, Spinner } from '@/components/common'
import { Field, Section, describedBy } from '@/components/common/form'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

/** sites.md section 6: start a claim; the server always answers 202. */
/** sites.md section 6, step 6: comments left with the account's own address. */
function OwnEmailSuggestion() {
  const { t } = useI18n()
  const me = useMe()
  const suggestion = useQuery({
    queryKey: ['me', 'claims', 'suggestion'],
    queryFn: api.me.claimSuggestion,
    enabled: !!me.email,
  })
  const [claimed, setClaimed] = useState<number | null>(null)
  const claim = useApiMutation(
    async () => {
      const started = await api.me.startClaim(me.email!)
      if (!started.verified || !started.id) return null
      return (await api.me.confirmClaim(started.id)).claimed
    },
    { invalidate: [['me']], onSuccess: (count) => setClaimed(count) }
  )
  const data = suggestion.data
  if (claimed !== null)
    return (
      <Alert role='status'>
        <CircleCheck />
        <AlertDescription>{t('claimVerify.claimed', { count: claimed })}</AlertDescription>
      </Alert>
    )
  if (!data || data.count === 0) return null
  return (
    <Section title={t('claims.ownTitle')} description={t('claims.ownDesc', { count: data.count, email: me.email ?? '' })}>
      <ul className='divide-y rounded-lg border text-sm'>
        {data.sites.map((s) => (
          <li key={s.id} className='flex justify-between gap-3 px-4 py-2'>
            <span>{s.name}</span>
            <span className='text-muted-foreground'>{t('claimVerify.siteCount', { count: s.count })}</span>
          </li>
        ))}
      </ul>
      <Button onClick={() => claim.mutate(undefined)} disabled={claim.isPending}>
        {claim.isPending && <Spinner />}
        {t('claimVerify.confirm')}
      </Button>
    </Section>
  )
}

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
      <div className='mb-6'>
        <OwnEmailSuggestion />
      </div>
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
