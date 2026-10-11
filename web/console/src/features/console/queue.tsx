import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router'
import { Ban, Check, EyeOff, Inbox, Pin, PinOff, RotateCcw, ShieldX, X } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { CommentStatus, ConsoleComment, ModerationAction } from '@/api'
import { useI18n } from '@/i18n'
import { keys, useApiMutation, useCursorList } from '@/hooks/use-api'
import { CursorList, EmptyState, PageHeader, Spinner } from '@/components/common'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { NotFound } from '@/features/errors'
import { BanDialog } from './ban-dialog'
import { ConsoleCommentCard } from './comment-card'
import { useSite } from './site-context'

const TABS = ['pending', 'published', 'hidden', 'spam', 'all'] as const
type Tab = (typeof TABS)[number]

/** Actions moderation.md section 2 allows from each status. */
function actionsFor(c: ConsoleComment): ModerationAction[] {
  switch (c.status) {
    case 'pending':
      return ['approve', 'hide', 'spam']
    case 'published':
      return c.root_id ? ['hide', 'spam'] : [c.pinned ? 'unpin' : 'pin', 'hide', 'spam']
    case 'hidden':
    case 'spam':
      return ['restore']
    default:
      return []
  }
}

const ACTION_ICON: Record<ModerationAction, React.ElementType> = {
  approve: Check,
  hide: EyeOff,
  spam: ShieldX,
  restore: RotateCcw,
  pin: Pin,
  unpin: PinOff,
}

const BULK: ModerationAction[] = ['approve', 'hide', 'spam', 'restore']

export default function QueuePage() {
  const { t } = useI18n()
  const site = useSite()
  const [params, setParams] = useSearchParams()
  const tab: Tab = (TABS as readonly string[]).includes(params.get('status') ?? '')
    ? (params.get('status') as Tab)
    : 'pending'
  const thread = params.get('thread') ?? ''
  const status: CommentStatus | undefined = tab === 'all' ? undefined : tab
  const { query, items } = useCursorList(keys.siteComments(site.id, tab, thread), (cursor) =>
    api.sites.comments(site.id, { status, thread: thread || undefined }, cursor)
  )
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [banning, setBanning] = useState<ConsoleComment | null>(null)
  const visibleSelected = useMemo(
    () => items.filter((c) => selected.has(c.id)),
    [items, selected]
  )
  const threadTitle = thread ? items.find((c) => c.thread.id === thread)?.thread.title : undefined

  const moderate = useApiMutation(
    ({ ids, action }: { ids: string[]; action: ModerationAction }) =>
      api.sites.moderate(site.id, ids, action),
    {
      invalidate: [keys.site(site.id), keys.sites],
      onSuccess: (_, { ids, action }) => {
        setSelected((prev) => {
          const next = new Set(prev)
          ids.forEach((id) => next.delete(id))
          return next
        })
        toast.success(t(`queue.done.${action}`, { count: ids.length }))
      },
    }
  )

  if (site.status === 'deleted') return <NotFound />

  const setQuery = (next: { status?: Tab; thread?: string }) => {
    const p = new URLSearchParams(params)
    if (next.status !== undefined) p.set('status', next.status)
    if (next.thread !== undefined) {
      if (next.thread) p.set('thread', next.thread)
      else p.delete('thread')
    }
    setSelected(new Set())
    setParams(p, { replace: true })
  }

  const allChecked = items.length > 0 && visibleSelected.length === items.length
  const toggleAll = () =>
    setSelected(allChecked ? new Set() : new Set(items.map((c) => c.id)))

  return (
    <>
      <PageHeader title={t('queue.title')} description={t('queue.desc')} />
      <Tabs value={tab} onValueChange={(value) => setQuery({ status: value as Tab })} className='mb-4'>
        <TabsList className='flex-wrap'>
          {TABS.map((value) => (
            <TabsTrigger key={value} value={value}>
              {t(`queue.tab.${value}`)}
              {value === 'pending' && !!site.pending_count && (
                <Badge variant='secondary' className='ms-1 rounded-full px-1.5'>
                  {site.pending_count}
                </Badge>
              )}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      {thread && (
        <div className='mb-4 flex flex-wrap items-center gap-2 text-sm'>
          <span className='text-muted-foreground'>
            {t('queue.threadFilter', { title: threadTitle ?? `#${thread}` })}
          </span>
          <Button variant='ghost' size='sm' onClick={() => setQuery({ thread: '' })}>
            <X />
            {t('queue.clearThread')}
          </Button>
        </div>
      )}

      {items.length > 0 && (
        <div
          className='sticky top-14 z-30 mb-3 flex flex-wrap items-center gap-2 rounded-lg border bg-background/95 px-3 py-2 backdrop-blur'
          role='toolbar'
          aria-label={t('queue.bulkLabel')}
        >
          <Checkbox
            id='select-all'
            checked={allChecked ? true : visibleSelected.length ? 'indeterminate' : false}
            onCheckedChange={toggleAll}
          />
          <label htmlFor='select-all' className='text-sm'>
            {visibleSelected.length
              ? t('queue.selected', { count: visibleSelected.length })
              : t('queue.selectAll')}
          </label>
          {visibleSelected.length > 0 && (
            <div className='ms-auto flex flex-wrap gap-2'>
              {BULK.filter((action) =>
                visibleSelected.some((c) => actionsFor(c).includes(action))
              ).map((action) => {
                const Icon = ACTION_ICON[action]
                const ids = visibleSelected
                  .filter((c) => actionsFor(c).includes(action))
                  .map((c) => c.id)
                return (
                  <Button
                    key={action}
                    size='sm'
                    variant={action === 'approve' ? 'default' : 'outline'}
                    disabled={moderate.isPending}
                    onClick={() => moderate.mutate({ ids, action })}
                  >
                    <Icon />
                    {t(`queue.action.${action}`)}
                  </Button>
                )
              })}
            </div>
          )}
          {moderate.isPending && <Spinner className='ms-2' />}
        </div>
      )}

      <CursorList
        query={query}
        items={items}
        empty={
          <EmptyState icon={Inbox}>
            {tab === 'pending' ? t('queue.emptyPending') : t('queue.empty')}
          </EmptyState>
        }
      >
        {(list) => (
          <ul className='space-y-3' aria-label={t('queue.title')}>
            {list.map((c) => (
              <li key={c.id}>
                <ConsoleCommentCard
                  comment={c}
                  siteId={site.id}
                  leading={
                    <Checkbox
                      className='mt-2'
                      checked={selected.has(c.id)}
                      aria-label={t('queue.select', { id: c.id })}
                      onCheckedChange={(checked) =>
                        setSelected((prev) => {
                          const next = new Set(prev)
                          if (checked === true) next.add(c.id)
                          else next.delete(c.id)
                          return next
                        })
                      }
                    />
                  }
                  actions={
                    <>
                      {actionsFor(c).map((action) => {
                        const Icon = ACTION_ICON[action]
                        return (
                          <Button
                            key={action}
                            size='sm'
                            variant={action === 'approve' ? 'default' : 'outline'}
                            disabled={moderate.isPending}
                            onClick={() => moderate.mutate({ ids: [c.id], action })}
                          >
                            <Icon />
                            {t(`queue.action.${action}`)}
                          </Button>
                        )
                      })}
                      {c.author.kind !== 'former_user' && (
                        <Button size='sm' variant='ghost' onClick={() => setBanning(c)}>
                          <Ban />
                          {t('queue.ban')}
                        </Button>
                      )}
                    </>
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
