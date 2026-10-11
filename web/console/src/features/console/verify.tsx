import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link, useLocation } from 'react-router'
import { CircleCheck, CircleX, Clock, KeyRound, RefreshCw, ShieldCheck } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { Site, VerificationCheck, VerificationToken } from '@/api'
import { useI18n } from '@/i18n'
import { keys, useApiMutation } from '@/hooks/use-api'
import { CopyField, ListSkeleton, PageHeader, QueryError, Spinner } from '@/components/common'
import { Section } from '@/components/common/form'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { NotFound } from '@/features/errors'
import type { VerifyState } from './new-site'
import { useSite, useSiteDetail } from './site-context'

function hostOf(origin: string) {
  return new URL(origin).hostname
}

function Instructions({ token, origins }: { token: string; origins: string[] }) {
  const { t } = useI18n()
  return (
    <Tabs defaultValue='dns'>
      <TabsList className='flex-wrap'>
        <TabsTrigger value='dns'>{t('verify.method.dns')}</TabsTrigger>
        <TabsTrigger value='meta'>{t('verify.method.meta')}</TabsTrigger>
        <TabsTrigger value='file'>{t('verify.method.file')}</TabsTrigger>
      </TabsList>
      <TabsContent value='dns' className='space-y-4 pt-2'>
        <p className='text-sm text-muted-foreground'>{t('verify.method.dnsDesc')}</p>
        {origins.map((origin) => (
          <fieldset key={origin} className='space-y-3 rounded-lg border p-3'>
            <legend className='px-1 font-mono text-xs'>{origin}</legend>
            <CopyField label={t('verify.dnsType')} value='TXT' />
            <CopyField label={t('verify.dnsName')} value={`_kite-comments.${hostOf(origin)}`} />
            <CopyField label={t('verify.dnsValue')} value={`kite-comments-site=${token}`} />
          </fieldset>
        ))}
      </TabsContent>
      <TabsContent value='meta' className='space-y-4 pt-2'>
        <p className='text-sm text-muted-foreground'>{t('verify.method.metaDesc')}</p>
        <CopyField
          label={t('verify.metaTag')}
          value={`<meta name="kite-comments-site" content="${token}">`}
         
        />
      </TabsContent>
      <TabsContent value='file' className='space-y-4 pt-2'>
        <p className='text-sm text-muted-foreground'>{t('verify.method.fileDesc')}</p>
        {origins.map((origin) => (
          <fieldset key={origin} className='space-y-3 rounded-lg border p-3'>
            <legend className='px-1 font-mono text-xs'>{origin}</legend>
            <CopyField label={t('verify.fileUrl')} value={`${origin}/.well-known/kite-comments.txt`} />
            <CopyField label={t('verify.fileContent')} value={token} />
          </fieldset>
        ))}
      </TabsContent>
    </Tabs>
  )
}

function Results({ check }: { check: VerificationCheck }) {
  const { t } = useI18n()
  if (!check.results.length) return <p className='text-sm'>{t('verify.nothingToCheck')}</p>
  return (
    <ul className='space-y-2' aria-live='polite'>
      {check.results.map((r) => (
        <li key={r.origin} className='flex gap-2 rounded-lg border p-3 text-sm'>
          {r.verified ? (
            <CircleCheck className='mt-0.5 size-4 shrink-0 text-success' aria-hidden='true' />
          ) : (
            <CircleX className='mt-0.5 size-4 shrink-0 text-destructive' aria-hidden='true' />
          )}
          <div className='min-w-0 space-y-1'>
            <p className='font-medium break-all'>
              {r.verified
                ? t('verify.resultOk', {
                    origin: r.origin,
                    method: r.method ? t(`verify.method.${r.method}`) : '',
                  })
                : t('verify.resultFail', { origin: r.origin })}
            </p>
            {r.detail && <p className='text-muted-foreground'>{r.detail}</p>}
          </div>
        </li>
      ))}
    </ul>
  )
}

function VerifyBody({ site }: { site: Site }) {
  const { t, formatDateTime } = useI18n()
  const location = useLocation()
  const queryClient = useQueryClient()
  const initial = (location.state as VerifyState | null)?.verification
  const [token, setToken] = useState<VerificationToken | undefined>(initial)
  const [check, setCheck] = useState<VerificationCheck | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const pending = site.origins.filter((o) => !o.verified_at).map((o) => o.origin)

  const generate = useApiMutation(() => api.sites.newVerification(site.id), {
    invalidate: [keys.site(site.id)],
    onSuccess: (result) => {
      setToken(result)
      toast.success(t('verify.generated'))
    },
  })
  const run = useApiMutation(() => api.sites.checkVerification(site.id), {
    invalidate: [keys.sites],
    onSuccess: (result) => {
      setFailure(null)
      setCheck(result)
      queryClient.setQueryData(keys.site(site.id), result.site)
    },
    onError: (error) => {
      if (api.isApiError(error, 'verification_failed')) {
        setCheck(null)
        setFailure(error.message)
        return true
      }
    },
  })

  const knownExpiry = token?.expires_at ?? site.verification_expires_at

  if (!pending.length) {
    return (
      <Alert>
        <ShieldCheck />
        <AlertTitle>{t('verify.allVerified')}</AlertTitle>
        <AlertDescription>
          <p>{t('verify.keepRecord')}</p>
          {site.status === 'pending_review' && <p>{t('overview.pendingDesc')}</p>}
          {check && <Results check={check} />}
          <Link to={`/console/sites/${site.id}`} className='underline underline-offset-4'>
            {t('nav.overview')}
          </Link>
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <div className='space-y-6'>
      <Section
        title={t('verify.tokenTitle')}
        description={
          knownExpiry ? t('verify.tokenExpires', { date: formatDateTime(knownExpiry) }) : undefined
        }
        footer={
          <Button
            variant={token ? 'outline' : 'default'}
            onClick={() => generate.mutate(undefined)}
            disabled={generate.isPending}
          >
            {generate.isPending ? <Spinner /> : token ? <RefreshCw /> : <KeyRound />}
            {token ? t('verify.regenerate') : t('verify.generate')}
          </Button>
        }
      >
        {token ? (
          <CopyField label={t('verify.tokenTitle')} value={token.token} />
        ) : (
          <p className='text-sm text-muted-foreground'>{t('verify.tokenHidden')}</p>
        )}
      </Section>

      {token && (
        <Section title={t('verify.methodsTitle')} description={t('verify.methodsDesc')}>
          <Instructions token={token.token} origins={pending} />
        </Section>
      )}

      <Section
        title={t('verify.checkTitle')}
        description={t('verify.checkHint')}
        footer={
          <Button onClick={() => run.mutate(undefined)} disabled={run.isPending}>
            {run.isPending ? <Spinner /> : <ShieldCheck />}
            {run.isPending ? t('verify.checking') : t('verify.check')}
          </Button>
        }
      >
        {failure && (
          <Alert variant='destructive'>
            <CircleX />
            <AlertTitle>{t('verify.failedTitle')}</AlertTitle>
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        {check && <Results check={check} />}
        {check && site.status === 'pending_review' && (
          <Alert>
            <Clock />
            <AlertTitle>{t('overview.pendingTitle')}</AlertTitle>
            <AlertDescription>{t('overview.pendingDesc')}</AlertDescription>
          </Alert>
        )}
        <p className='text-sm text-muted-foreground'>{t('verify.keepRecord')}</p>
      </Section>
    </div>
  )
}

export default function VerifyPage() {
  const { t } = useI18n()
  const summary = useSite()
  const detail = useSiteDetail(summary.id, summary.role === 'owner')
  if (summary.role !== 'owner' || summary.status === 'deleted') return <NotFound />
  return (
    <>
      <PageHeader title={t('verify.title')} description={t('verify.desc')} />
      {detail.isPending ? (
        <ListSkeleton rows={2} />
      ) : detail.isError ? (
        <QueryError error={detail.error} onRetry={() => void detail.refetch()} />
      ) : (
        <VerifyBody site={detail.data} />
      )}
    </>
  )
}
