import { useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'
import { CircleCheck } from 'lucide-react'
import * as api from '@/api'
import type { ClaimPreview } from '@/api'
import { useI18n } from '@/i18n'
import { useApiMutation } from '@/hooks/use-api'
import { PageHeader, Spinner } from '@/components/common'
import { Section } from '@/components/common/form'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

/**
 * The page a claim email links to. Opening it changes nothing; only the
 * button POSTs, so mail scanners that follow links cannot verify a claim.
 */
export default function ClaimVerifyPage() {
  const { t } = useI18n()
  const { claimId = '' } = useParams()
  const [params] = useSearchParams()
  const token = params.get('t') ?? ''
  const [preview, setPreview] = useState<ClaimPreview | null>(null)
  const [notYours, setNotYours] = useState(false)
  const [claimed, setClaimed] = useState<number | null>(null)

  const verify = useApiMutation(() => api.me.verifyClaim(claimId, token), {
    onSuccess: setPreview,
    onError: (error) => {
      if (api.isApiError(error) && (error.status === 404 || error.status === 403)) {
        setNotYours(true)
        return true
      }
    },
  })
  const confirm = useApiMutation(() => api.me.confirmClaim(claimId), {
    onSuccess: (result) => setClaimed(result.claimed),
  })

  return (
    <>
      <PageHeader title={t('claimVerify.title')} description={t('claimVerify.desc')} />
      <Section title={t('claims.title')}>
        {!token || notYours ? (
          <Alert role='status'>
            <AlertDescription>
              {!token ? t('claimVerify.missingToken') : t('claimVerify.notYours')}
            </AlertDescription>
          </Alert>
        ) : claimed !== null ? (
          <Alert role='status'>
            <CircleCheck />
            <AlertDescription>
              <p>{t('claimVerify.claimed', { count: claimed })}</p>
              <Link to='/me' className='underline underline-offset-4'>
                {t('nav.myComments')}
              </Link>
            </AlertDescription>
          </Alert>
        ) : preview ? (
          preview.count === 0 ? (
            <p className='text-sm'>{t('claimVerify.none')}</p>
          ) : (
            <div className='space-y-4'>
              <p className='text-sm'>{t('claimVerify.found', { count: preview.count })}</p>
              <ul className='divide-y rounded-lg border text-sm'>
                {preview.sites.map((s) => (
                  <li key={s.id} className='flex justify-between gap-3 px-4 py-2'>
                    <span>{s.name}</span>
                    <span className='text-muted-foreground'>
                      {t('claimVerify.siteCount', { count: s.count })}
                    </span>
                  </li>
                ))}
              </ul>
              <Button onClick={() => confirm.mutate(undefined)} disabled={confirm.isPending}>
                {confirm.isPending && <Spinner />}
                {t('claimVerify.confirm')}
              </Button>
            </div>
          )
        ) : (
          <div className='space-y-4'>
            <p className='text-sm text-muted-foreground'>{t('claimVerify.prompt')}</p>
            <Button onClick={() => verify.mutate(undefined)} disabled={verify.isPending}>
              {verify.isPending && <Spinner />}
              {t('claimVerify.button')}
            </Button>
          </div>
        )}
      </Section>
    </>
  )
}
