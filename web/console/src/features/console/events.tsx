import { useState } from 'react'
import { Bot, History, ShieldCheck, Undo2, UserRound } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { ModerationEvent } from '@/api'
import { useI18n, type MessageKey } from '@/i18n'
import { keys, useApiMutation, useCursorList } from '@/hooks/use-api'
import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { CursorList, EmptyState, PageHeader, TimeAgo } from '@/components/common'
import { Button } from '@/components/ui/button'
import { NotFound } from '@/features/errors'
import { useSite } from './site-context'

const KNOWN = new Set([
  'approve', 'hide', 'spam', 'restore', 'pin', 'unpin', 'lock', 'unlock', 'move_thread',
  'ban', 'unban', 'auto_spam', 'auto_pending', 'auto_close', 'report_threshold',
  'platform_hide', 'platform_suspend_site', 'platform_ban', 'author_edit', 'author_delete', 'claim',
])

function detailText(event: ModerationEvent, t: (key: MessageKey, vars?: Record<string, string | number>) => string) {
  const d = event.detail
  const parts: string[] = []
  if (event.comment_id) parts.push(t('events.comment', { id: event.comment_id }))
  if (typeof d.from === 'string' && typeof d.to === 'string') parts.push(`${d.from} \u2192 ${d.to}`)
  if (Array.isArray(d.comments)) parts.push(t('events.movedCount', { count: d.comments.length }))
  if (typeof d.thread_id === 'string') parts.push(t('events.thread', { id: d.thread_id }))
  if (typeof d.target === 'string') parts.push(t(`bans.target.${d.target as 'user' | 'email' | 'ip'}`))
  if (typeof d.rule === 'string') parts.push(t('events.rule', { rule: d.rule }))
  return parts.join(' \u00b7 ')
}

function ActorIcon({ event }: { event: ModerationEvent }) {
  const Icon = event.actor === null ? Bot : event.action.startsWith('platform_') ? ShieldCheck : UserRound
  return <Icon className='mt-0.5 size-4 shrink-0 text-muted-foreground' aria-hidden='true' />
}

export default function EventsPage() {
  const { t } = useI18n()
  const site = useSite()
  const { query, items } = useCursorList(keys.events(site.id), (cursor) => api.sites.events(site.id, cursor))
  const [undoing, setUndoing] = useState<ModerationEvent | null>(null)
  const undo = useApiMutation((event: ModerationEvent) => api.sites.undoEvent(site.id, event.id), {
    invalidate: [keys.site(site.id)],
    onSuccess: () => {
      setUndoing(null)
      toast.success(t('events.undone'))
    },
  })
  if (site.status === 'deleted') return <NotFound />
  return (
    <>
      <PageHeader title={t('events.title')} description={t('events.desc')} />
      <CursorList
        query={query}
        items={items}
        empty={<EmptyState icon={History}>{t('events.empty')}</EmptyState>}
      >
        {(list) => (
          <ol className='divide-y rounded-lg border' aria-label={t('events.title')}>
            {list.map((event) => (
              <li key={event.id} className='flex gap-3 p-4 text-sm'>
                <ActorIcon event={event} />
                <div className='min-w-0 flex-1 space-y-1'>
                  <p>
                    <span className='font-medium'>{event.actor?.name ?? t('events.system')}</span>{' '}
                    {KNOWN.has(event.action)
                      ? t(`events.action.${event.action}` as MessageKey)
                      : event.action}
                  </p>
                  {detailText(event, t) && (
                    <p className='font-mono text-xs break-all text-muted-foreground'>{detailText(event, t)}</p>
                  )}
                </div>
                <div className='flex shrink-0 flex-col items-end gap-1'>
                  <TimeAgo iso={event.created_at} className='text-xs text-muted-foreground' />
                  {event.action === 'move_thread' && site.role === 'owner' && (
                    <Button size='sm' variant='outline' onClick={() => setUndoing(event)}>
                      <Undo2 />
                      {t('events.undo')}
                    </Button>
                  )}
                </div>
              </li>
            ))}
          </ol>
        )}
      </CursorList>
      <ConfirmDialog
        open={!!undoing}
        onOpenChange={(open) => !open && setUndoing(null)}
        title={t('events.undoTitle')}
        description={t('events.undoDesc')}
        confirmText={t('events.undo')}
        isLoading={undo.isPending}
        onConfirm={() => undoing && undo.mutate(undoing)}
      />
    </>
  )
}
