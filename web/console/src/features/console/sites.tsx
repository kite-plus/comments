import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { Flag, Globe, Inbox, Plus } from 'lucide-react'
import * as api from '@/api'
import type { SiteSummary } from '@/api'
import { useI18n } from '@/i18n'
import { purgeDate } from '@/lib/policy'
import { keys } from '@/hooks/use-api'
import {
  EmptyState,
  ListSkeleton,
  PageHeader,
  QueryError,
  SiteStatusBadge,
} from '@/components/common'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

function SiteCard({ site }: { site: SiteSummary }) {
  const { t, formatDate } = useI18n()
  return (
    <li>
      <Link
        to={`/console/sites/${site.id}`}
        className='flex h-full flex-col gap-3 rounded-xl border bg-card p-4 transition-colors hover:bg-accent/50 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none'
      >
        <div className='flex items-start justify-between gap-2'>
          <span className='min-w-0'>
            <span className='block truncate font-semibold'>{site.name}</span>
            <span className='block truncate text-sm text-muted-foreground'>{site.primary_origin}</span>
          </span>
          <SiteStatusBadge status={site.status} />
        </div>
        <div className='mt-auto flex flex-wrap items-center gap-2 text-xs text-muted-foreground'>
          <Badge variant='secondary'>{t(`role.${site.role}`)}</Badge>
          {!!site.pending_count && (
            <span className='inline-flex items-center gap-1 text-warning'>
              <Inbox className='size-3.5' aria-hidden='true' />
              {t('sites.pending', { count: site.pending_count })}
            </span>
          )}
          {!!site.open_reports && (
            <span className='inline-flex items-center gap-1 text-destructive'>
              <Flag className='size-3.5' aria-hidden='true' />
              {t('sites.reports', { count: site.open_reports })}
            </span>
          )}
          {site.status === 'deleted' && site.deleted_at && (
            <span>
              {t('sites.restorableUntil', {
                date: formatDate(purgeDate(site.deleted_at, site.purge_at)),
              })}
            </span>
          )}
        </div>
      </Link>
    </li>
  )
}

export default function SitesPage() {
  const { t } = useI18n()
  const sites = useQuery({ queryKey: keys.sites, queryFn: () => api.sites.list() })
  const list = sites.data?.data ?? []
  const live = list.filter((s) => s.status !== 'deleted')
  const deleted = list.filter((s) => s.status === 'deleted')

  return (
    <>
      <PageHeader title={t('sites.title')} description={t('sites.desc')}>
        <Button asChild>
          <Link to='/console/new'>
            <Plus />
            {t('nav.newSite')}
          </Link>
        </Button>
      </PageHeader>
      {sites.isPending ? (
        <ListSkeleton rows={2} />
      ) : sites.isError ? (
        <QueryError error={sites.error} onRetry={() => void sites.refetch()} />
      ) : !list.length ? (
        <EmptyState
          icon={Globe}
          action={
            <Button asChild>
              <Link to='/console/new'>{t('nav.newSite')}</Link>
            </Button>
          }
        >
          {t('sites.empty')}
        </EmptyState>
      ) : (
        <div className='space-y-8'>
          <ul className='grid gap-3 sm:grid-cols-2' aria-label={t('sites.title')}>
            {live.map((site) => (
              <SiteCard key={site.id} site={site} />
            ))}
          </ul>
          {!!deleted.length && (
            <section className='space-y-3'>
              <h2 className='text-sm font-medium text-muted-foreground'>{t('sites.deletedHeading')}</h2>
              <ul className='grid gap-3 sm:grid-cols-2'>
                {deleted.map((site) => (
                  <SiteCard key={site.id} site={site} />
                ))}
              </ul>
            </section>
          )}
        </div>
      )}
    </>
  )
}
