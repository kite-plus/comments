import { useState } from 'react'
import { useSearchParams } from 'react-router'
import { Check, Pause, Play, ShieldCheck } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { AdminSite, AdminSiteAction, SiteStatus } from '@/api'
import { useMe } from '@/context/auth'
import { useI18n } from '@/i18n'
import { keys, useApiMutation, useCursorList } from '@/hooks/use-api'
import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { CursorList, EmptyState, PageHeader, SiteStatusBadge, TimeAgo } from '@/components/common'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { NotFound } from '@/features/errors'

const TABS = ['pending_review', 'active', 'suspended', 'all'] as const
type Tab = (typeof TABS)[number]

const DONE = {
  approve: 'admin.approved',
  suspend: 'admin.suspended',
  unsuspend: 'admin.unsuspended',
} as const

/** api.md section 8, minimal: approve beta sites and suspend abusive ones. */
export default function AdminSitesPage() {
  const { t } = useI18n()
  const me = useMe()
  const [params, setParams] = useSearchParams()
  const tab: Tab = (TABS as readonly string[]).includes(params.get('status') ?? '')
    ? (params.get('status') as Tab)
    : 'pending_review'
  const status: SiteStatus | undefined = tab === 'all' ? undefined : tab
  const { query, items } = useCursorList(
    keys.adminSites(tab),
    (cursor) => api.admin.sites(status, cursor),
    { enabled: me.is_platform_admin }
  )
  const [suspending, setSuspending] = useState<AdminSite | null>(null)
  const [note, setNote] = useState('')

  const update = useApiMutation(
    ({ site, action }: { site: AdminSite; action: AdminSiteAction }) =>
      api.admin.siteAction(site.id, action, action === 'suspend' ? note.trim() : undefined),
    {
      invalidate: [['admin', 'sites'], keys.sites],
      onSuccess: (_, { site, action }) => {
        setSuspending(null)
        setNote('')
        toast.success(t(DONE[action], { name: site.name }))
      },
    }
  )

  if (!me.is_platform_admin) return <NotFound />

  return (
    <>
      <PageHeader title={t('admin.title')} description={t('admin.desc')} />
      <Tabs value={tab} onValueChange={(v) => setParams({ status: v }, { replace: true })} className='mb-4'>
        <TabsList className='flex-wrap'>
          {TABS.map((value) => (
            <TabsTrigger key={value} value={value}>
              {value === 'all' ? t('queue.tab.all') : t(`status.site.${value}`)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <CursorList
        query={query}
        items={items}
        empty={<EmptyState icon={ShieldCheck}>{t('admin.empty')}</EmptyState>}
      >
        {(list) => (
          <ul className='divide-y rounded-lg border' aria-label={t('admin.title')}>
            {list.map((site) => (
              <li key={site.id} className='flex flex-wrap items-start gap-3 p-4'>
                <div className='min-w-0 flex-1 space-y-1'>
                  <div className='flex flex-wrap items-center gap-2'>
                    <span className='font-medium'>{site.name}</span>
                    <SiteStatusBadge status={site.status} />
                  </div>
                  <ul className='flex flex-wrap gap-1'>
                    {site.origins.map((o) => (
                      <li key={o.origin}>
                        <Badge variant='outline' className={o.verified_at ? 'font-mono text-success' : 'font-mono text-muted-foreground'}>
                          {o.origin}
                        </Badge>
                      </li>
                    ))}
                  </ul>
                  <p className='flex flex-wrap gap-x-3 text-xs text-muted-foreground'>
                    {site.owner && <span>{t('admin.owner', { name: site.owner.display_name })}</span>}
                    <span>
                      {t('admin.created')} <TimeAgo iso={site.created_at} />
                    </span>
                  </p>
                  {site.status_note && <p className='text-sm text-muted-foreground'>{site.status_note}</p>}
                </div>
                <div className='flex gap-2'>
                  {site.status === 'pending_review' && (
                    <Button size='sm' disabled={update.isPending} onClick={() => update.mutate({ site, action: 'approve' })}>
                      <Check />
                      {t('admin.approve')}
                    </Button>
                  )}
                  {(site.status === 'active' || site.status === 'pending_review') && (
                    <Button size='sm' variant='outline' onClick={() => setSuspending(site)}>
                      <Pause />
                      {t('admin.suspend')}
                    </Button>
                  )}
                  {site.status === 'suspended' && (
                    <Button size='sm' variant='outline' disabled={update.isPending} onClick={() => update.mutate({ site, action: 'unsuspend' })}>
                      <Play />
                      {t('admin.unsuspend')}
                    </Button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </CursorList>
      <ConfirmDialog
        open={!!suspending}
        onOpenChange={(open) => !open && setSuspending(null)}
        title={t('admin.suspendTitle', { name: suspending?.name ?? '' })}
        description={t('admin.suspendDesc')}
        confirmText={t('admin.suspend')}
        destructive
        isLoading={update.isPending}
        onConfirm={() => suspending && update.mutate({ site: suspending, action: 'suspend' })}
      >
        <div className='space-y-2'>
          <Label htmlFor='suspend-note'>{t('admin.suspendNote')}</Label>
          <Textarea id='suspend-note' maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
        </div>
      </ConfirmDialog>
    </>
  )
}
