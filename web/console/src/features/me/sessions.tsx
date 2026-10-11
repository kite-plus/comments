import { useState } from 'react'
import { LogOut, MonitorSmartphone } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { SiteSession } from '@/api'
import { useI18n } from '@/i18n'
import { keys, useApiMutation, useCursorList } from '@/hooks/use-api'
import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { CursorList, EmptyState, PageHeader, TimeAgo } from '@/components/common'
import { Button } from '@/components/ui/button'

export default function SessionsPage() {
  const { t, formatDate } = useI18n()
  const { query, items } = useCursorList(keys.sessions, (cursor) => api.me.siteSessions(cursor))
  const [target, setTarget] = useState<SiteSession | 'all' | null>(null)

  const revoke = useApiMutation((s: SiteSession) => api.me.revokeSiteSession(s.id), {
    invalidate: [keys.sessions],
    onSuccess: (_, s) => {
      setTarget(null)
      toast.success(t('sessions.signedOut', { site: s.site.name }))
    },
  })
  const revokeAll = useApiMutation(() => api.me.revokeAllSiteSessions(), {
    invalidate: [keys.sessions],
    onSuccess: () => {
      setTarget(null)
      toast.success(t('sessions.signedOutAll'))
    },
  })

  return (
    <>
      <PageHeader title={t('sessions.title')} description={t('sessions.desc')}>
        <Button variant='outline' disabled={!items.length} onClick={() => setTarget('all')}>
          <LogOut />
          {t('sessions.signOutAll')}
        </Button>
      </PageHeader>
      <CursorList
        query={query}
        items={items}
        empty={<EmptyState icon={MonitorSmartphone}>{t('sessions.empty')}</EmptyState>}
      >
        {(list) => (
          <ul className='divide-y rounded-lg border' aria-label={t('sessions.title')}>
            {list.map((s) => (
              <li key={s.id} className='flex flex-wrap items-center gap-3 p-4'>
                <div className='min-w-0 flex-1 space-y-1'>
                  <p className='font-medium'>{s.site.name}</p>
                  <p className='text-sm text-muted-foreground'>
                    {s.site.origin && <span className='me-2'>{s.site.origin}</span>}
                    {s.client_label}
                  </p>
                  <p className='flex flex-wrap gap-x-3 text-xs text-muted-foreground'>
                    <span>
                      {t('sessions.lastUsed')} <TimeAgo iso={s.last_used_at} />
                    </span>
                    <span>{t(`sessions.via.${s.created_via}`)}</span>
                    <span>{t('sessions.expires', { date: formatDate(s.expires_at) })}</span>
                  </p>
                </div>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => setTarget(s)}
                  aria-label={t('sessions.signOutSite', { site: s.site.name })}
                >
                  {t('sessions.signOut')}
                </Button>
              </li>
            ))}
          </ul>
        )}
      </CursorList>
      <p className='mt-4 text-sm text-muted-foreground'>{t('sessions.note')}</p>

      <ConfirmDialog
        open={target !== null}
        onOpenChange={(open) => !open && setTarget(null)}
        title={
          target === 'all'
            ? t('sessions.signOutAllTitle')
            : t('sessions.signOutTitle', { site: target?.site.name ?? '' })
        }
        description={
          target === 'all'
            ? t('sessions.signOutAllDesc')
            : t('sessions.signOutDesc', { client: target?.client_label ?? '' })
        }
        confirmText={target === 'all' ? t('sessions.signOutAll') : t('sessions.signOut')}
        destructive
        isLoading={revoke.isPending || revokeAll.isPending}
        onConfirm={() => {
          if (target === 'all') revokeAll.mutate(undefined)
          else if (target) revoke.mutate(target)
        }}
      />
    </>
  )
}
