import { useState } from 'react'
import { useSearchParams } from 'react-router'
import { Ban, Check, EyeOff, Flag, X } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { ConsoleComment, Report, ReportStatus } from '@/api'
import { useI18n } from '@/i18n'
import { keys, useApiMutation, useCursorList } from '@/hooks/use-api'
import { CursorList, EmptyState, PageHeader, TimeAgo } from '@/components/common'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { NotFound } from '@/features/errors'
import { BanDialog } from './ban-dialog'
import { ConsoleCommentCard } from './comment-card'
import { useSite } from './site-context'

const TABS: ReportStatus[] = ['open', 'resolved', 'dismissed']

type Decision = { report: Report; outcome: 'hide' | 'resolved' | 'dismissed' }

export default function ReportsPage() {
  const { t } = useI18n()
  const site = useSite()
  const [params, setParams] = useSearchParams()
  const tab = (TABS as string[]).includes(params.get('status') ?? '')
    ? (params.get('status') as ReportStatus)
    : 'open'
  const { query, items } = useCursorList(keys.reports(site.id, tab), (cursor) =>
    api.sites.reports(site.id, tab, cursor)
  )
  const [banning, setBanning] = useState<ConsoleComment | null>(null)

  const decide = useApiMutation(
    ({ report, outcome }: Decision) =>
      outcome === 'hide'
        ? api.sites.updateReport(site.id, report.id, 'resolved', 'hide')
        : api.sites.updateReport(site.id, report.id, outcome),
    {
      invalidate: [keys.site(site.id), keys.sites],
      onSuccess: (_, { outcome }) => toast.success(t(`reports.done.${outcome}`)),
    }
  )

  if (site.status === 'deleted') return <NotFound />

  return (
    <>
      <PageHeader title={t('reports.title')} description={t('reports.desc')} />
      <Tabs
        value={tab}
        onValueChange={(value) => setParams({ status: value }, { replace: true })}
        className='mb-4'
      >
        <TabsList>
          {TABS.map((value) => (
            <TabsTrigger key={value} value={value}>
              {t(`reports.tab.${value}`)}
              {value === 'open' && !!site.open_reports && (
                <Badge variant='secondary' className='ms-1 rounded-full px-1.5'>
                  {site.open_reports}
                </Badge>
              )}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <CursorList
        query={query}
        items={items}
        empty={<EmptyState icon={Flag}>{t('reports.empty')}</EmptyState>}
      >
        {(list) => (
          <ul className='space-y-4' aria-label={t('reports.title')}>
            {list.map((report) => (
              <li key={report.id} className='space-y-2'>
                <div className='flex flex-wrap items-center gap-2 text-sm'>
                  <Badge
                    variant='outline'
                    className={
                      report.reason === 'illegal' || report.reason === 'privacy'
                        ? 'border-destructive/40 text-destructive'
                        : undefined
                    }
                  >
                    {t(`reports.reason.${report.reason}`)}
                  </Badge>
                  {report.note && <span className='break-words italic'>{report.note}</span>}
                  <span className='text-xs text-muted-foreground'>
                    {t('reports.reportedAt')} <TimeAgo iso={report.created_at} />
                  </span>
                  {report.resolved_at && (
                    <span className='text-xs text-muted-foreground'>
                      {'\u00b7'} {t(`reports.tab.${report.status}`)} <TimeAgo iso={report.resolved_at} />
                    </span>
                  )}
                </div>
                <ConsoleCommentCard
                  comment={report.comment}
                  siteId={site.id}
                  actions={
                    report.status === 'open' && (
                      <>
                        {report.comment.status !== 'hidden' && report.comment.status !== 'deleted' && (
                          <Button
                            size='sm'
                            disabled={decide.isPending}
                            onClick={() => decide.mutate({ report, outcome: 'hide' })}
                          >
                            <EyeOff />
                            {t('reports.hideResolve')}
                          </Button>
                        )}
                        <Button
                          size='sm'
                          variant='outline'
                          disabled={decide.isPending}
                          onClick={() => decide.mutate({ report, outcome: 'resolved' })}
                        >
                          <Check />
                          {t('reports.resolve')}
                        </Button>
                        <Button
                          size='sm'
                          variant='outline'
                          disabled={decide.isPending}
                          onClick={() => decide.mutate({ report, outcome: 'dismissed' })}
                        >
                          <X />
                          {t('reports.dismiss')}
                        </Button>
                        {report.comment.author.kind !== 'former_user' && (
                          <Button size='sm' variant='ghost' onClick={() => setBanning(report.comment)}>
                            <Ban />
                            {t('queue.ban')}
                          </Button>
                        )}
                      </>
                    )
                  }
                />
              </li>
            ))}
          </ul>
        )}
      </CursorList>
      <BanDialog siteId={site.id} comment={banning} onOpenChange={(open) => !open && setBanning(null)} />
    </>
  )
}
