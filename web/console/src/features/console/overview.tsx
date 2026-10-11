import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { toast } from 'sonner'
import { ArrowRight, Clock, Flag, Inbox, RotateCcw, ShieldAlert, ShieldCheck } from 'lucide-react'
import * as api from '@/api'
import type { Site, SiteSummary } from '@/api'
import { useI18n } from '@/i18n'
import { purgeDate } from '@/lib/policy'
import { keys, useApiMutation } from '@/hooks/use-api'
import { CopyField, PageHeader, QueryError, SiteStatusBadge, Spinner } from '@/components/common'
import { Section } from '@/components/common/form'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useSite, useSiteDetail } from './site-context'

function serviceOrigin() {
  return window.location.origin
}

export function embedSnippet(siteId: string, extra = '', script?: { src: string; integrity: string }) {
  const tag = script
    ? `<script src="${script.src}" integrity="${script.integrity}" crossorigin="anonymous" async></script>`
    : `<script src="${serviceOrigin()}/embed.js" crossorigin="anonymous" async></script>`
  return `<kite-comments site="${siteId}"${extra}></kite-comments>\n${tag}`
}

function StatusPanel({ summary, site }: { summary: SiteSummary; site?: Site }) {
  const { t, formatDate } = useI18n()
  const base = `/console/sites/${summary.id}`
  const restore = useApiMutation(() => api.sites.restore(summary.id), {
    invalidate: [keys.sites, keys.site(summary.id)],
    onSuccess: () => toast.success(t('overview.restored')),
  })

  switch (summary.status) {
    case 'unverified':
      return (
        <Alert>
          <ShieldAlert />
          <AlertTitle>{t('overview.unverifiedTitle')}</AlertTitle>
          <AlertDescription>
            <p>{t('overview.unverifiedDesc')}</p>
            {summary.role === 'owner' && (
              <Button asChild size='sm' className='mt-2'>
                <Link to={`${base}/verify`}>
                  {t('overview.verifyNow')}
                  <ArrowRight />
                </Link>
              </Button>
            )}
          </AlertDescription>
        </Alert>
      )
    case 'pending_review':
      return (
        <Alert>
          <Clock />
          <AlertTitle>{t('overview.pendingTitle')}</AlertTitle>
          <AlertDescription>{t('overview.pendingDesc')}</AlertDescription>
        </Alert>
      )
    case 'suspended':
      return (
        <Alert variant='destructive'>
          <ShieldAlert />
          <AlertTitle>{t('overview.suspendedTitle')}</AlertTitle>
          <AlertDescription>
            <p>{t('overview.suspendedDesc')}</p>
            {site?.status_note && <p>{t('overview.statusNote', { note: site.status_note })}</p>}
          </AlertDescription>
        </Alert>
      )
    case 'deleted': {
      const until = summary.deleted_at ? purgeDate(summary.deleted_at, summary.purge_at) : null
      return (
        <Alert variant='destructive'>
          <ShieldAlert />
          <AlertTitle>{t('overview.deletedTitle')}</AlertTitle>
          <AlertDescription>
            <p>{t('overview.deletedDesc', { date: until ? formatDate(until) : '\u2014' })}</p>
            {summary.role === 'owner' && (
              <Button
                size='sm'
                variant='outline'
                className='mt-2'
                onClick={() => restore.mutate(undefined)}
                disabled={restore.isPending}
              >
                {restore.isPending ? <Spinner /> : <RotateCcw />}
                {t('overview.restore')}
              </Button>
            )}
          </AlertDescription>
        </Alert>
      )
    }
    default:
      return null
  }
}

function Stat({
  icon: Icon,
  label,
  value,
  to,
}: {
  icon: React.ElementType
  label: string
  value: number
  to: string
}) {
  return (
    <Link
      to={to}
      className='flex items-center gap-3 rounded-xl border bg-card p-4 transition-colors hover:bg-accent/50 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none'
    >
      <Icon className='size-5 text-muted-foreground' aria-hidden='true' />
      <span className='flex-1 text-sm text-muted-foreground'>{label}</span>
      <span className='text-2xl font-semibold tabular-nums'>{value}</span>
    </Link>
  )
}

function Origins({ site }: { site: Site }) {
  const { t } = useI18n()
  return (
    <ul className='divide-y rounded-lg border text-sm'>
      {site.origins.map((o) => (
        <li key={o.origin} className='flex flex-wrap items-center gap-2 px-4 py-2'>
          <span className='min-w-0 flex-1 truncate font-mono text-xs'>{o.origin}</span>
          {o.is_primary && <Badge variant='secondary'>{t('settings.primary')}</Badge>}
          {o.verified_at ? (
            o.failing_since ? (
              <Badge variant='outline' className='border-warning/40 text-warning'>
                {t('origin.failing')}
              </Badge>
            ) : (
              <Badge variant='outline' className='border-success/40 text-success'>
                <ShieldCheck />
                {t('origin.verified')}
              </Badge>
            )
          ) : (
            <Badge variant='outline' className='text-muted-foreground'>
              {t('origin.unverified')}
            </Badge>
          )}
        </li>
      ))}
    </ul>
  )
}

export default function OverviewPage() {
  const { t } = useI18n()
  const summary = useSite()
  const owner = summary.role === 'owner'
  const detail = useSiteDetail(summary.id, owner)
  const base = `/console/sites/${summary.id}`
  const embeddable = summary.status === 'active' || summary.status === 'pending_review'
  const embed = useQuery({
    queryKey: ['embed-info'],
    queryFn: api.embedInfo,
    staleTime: Infinity,
    enabled: embeddable,
  })

  return (
    <>
      <PageHeader
        title={summary.name}
        description={
          <span className='flex flex-wrap items-center gap-2'>
            <span>{summary.primary_origin}</span>
            <SiteStatusBadge status={summary.status} />
          </span>
        }
      />
      <div className='space-y-6'>
        <StatusPanel summary={summary} site={detail.data} />

        {summary.status !== 'deleted' && (
          <div className='grid gap-3 sm:grid-cols-2'>
            <Stat icon={Inbox} label={t('overview.statPending')} value={summary.pending_count ?? 0} to={`${base}/comments`} />
            <Stat icon={Flag} label={t('overview.statReports')} value={summary.open_reports ?? 0} to={`${base}/reports`} />
          </div>
        )}

        <Section title={t('overview.siteId')} description={t('overview.siteIdDesc')}>
          <CopyField label={t('overview.siteId')} value={summary.id} />
        </Section>

        {embeddable && (
          <Section title={t('overview.embedTitle')} description={t('overview.embedDesc')}>
            <Tabs defaultValue='generic'>
              <TabsList className='flex-wrap'>
                <TabsTrigger value='generic'>{t('overview.embedGeneric')}</TabsTrigger>
                <TabsTrigger value='kite'>Kite</TabsTrigger>
                <TabsTrigger value='spa'>{t('overview.embedSpa')}</TabsTrigger>
                {embed.data && <TabsTrigger value='pinned'>{t('overview.embedPinned')}</TabsTrigger>}
              </TabsList>
              <TabsContent value='generic' className='space-y-3 pt-2'>
                <p className='text-sm text-muted-foreground'>{t('overview.embedGenericDesc')}</p>
                <CopyField label={t('overview.embedCode')} value={embedSnippet(summary.id)} />
              </TabsContent>
              <TabsContent value='kite' className='space-y-3 pt-2'>
                <p className='text-sm text-muted-foreground'>{t('overview.embedKiteDesc')}</p>
                <CopyField label={t('overview.siteId')} value={summary.id} />
              </TabsContent>
              <TabsContent value='spa' className='space-y-3 pt-2'>
                <p className='text-sm text-muted-foreground'>{t('overview.embedSpaDesc')}</p>
                <CopyField
                  label={t('overview.embedCode')}
                  value={embedSnippet(summary.id, ' page-key="{{ post.id }}"')}
                 
                />
              </TabsContent>
              {embed.data && (
                <TabsContent value='pinned' className='space-y-3 pt-2'>
                  <p className='text-sm text-muted-foreground'>
                    {t('overview.embedPinnedDesc', { version: embed.data.version })}
                  </p>
                  <CopyField
                    label={t('overview.embedCode')}
                    value={embedSnippet(summary.id, '', {
                      src: embed.data.pinned_url,
                      integrity: embed.data.integrity,
                    })}
                  />
                </TabsContent>
              )}
            </Tabs>
          </Section>
        )}

        {owner && (
          <Section
            title={t('overview.origins')}
            footer={
              summary.status !== 'deleted' && (
                <Button asChild variant='outline' size='sm'>
                  <Link to={`${base}/settings#origins`}>{t('overview.manageOrigins')}</Link>
                </Button>
              )
            }
          >
            {detail.isPending ? (
              <Skeleton className='h-16 w-full' />
            ) : detail.isError ? (
              <QueryError error={detail.error} onRetry={() => void detail.refetch()} />
            ) : (
              <Origins site={detail.data} />
            )}
          </Section>
        )}
        {!owner && <p className='text-sm text-muted-foreground'>{t('overview.notOwner')}</p>}
      </div>
    </>
  )
}
