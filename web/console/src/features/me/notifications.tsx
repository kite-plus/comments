import { Bell, BellOff, BellRing, CheckCheck, MoreHorizontal } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { Notification } from '@/api'
import { useI18n } from '@/i18n'
import { permalink } from '@/lib/permalink'
import { cn } from '@/lib/utils'
import { keys, useApiMutation, useCursorList } from '@/hooks/use-api'
import {
  CursorList,
  EmptyState,
  ExternalLink,
  PageHeader,
  Spinner,
  TimeAgo,
  UserAvatar,
} from '@/components/common'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

function NotificationItem({
  item,
  onRead,
  onMute,
}: {
  item: Notification
  onRead: (id: string) => void
  onMute: (item: Notification) => void
}) {
  const { t } = useI18n()
  const unread = !item.read_at
  const title = item.thread.title || t('notifications.untitled')
  return (
    <li
      className={cn(
        'flex gap-3 rounded-lg border p-4',
        unread && 'border-brand/40 bg-brand/5'
      )}
    >
      <UserAvatar name={item.comment.author.name} seed={item.comment.author.name} src={item.comment.author.avatar} />
      <div className='min-w-0 flex-1 space-y-1.5'>
        <p className='text-sm'>
          {unread && (
            <Badge className='me-2 bg-brand text-white'>{t('notifications.unread')}</Badge>
          )}
          <span className='font-medium'>
            {t('notifications.replied', { name: item.comment.author.name })}
          </span>
        </p>
        <p className='text-sm text-muted-foreground'>
          {item.site.name} {'\u00b7'} {title}
        </p>
        <blockquote className='border-s-2 ps-3 text-sm break-words'>{item.comment.excerpt}</blockquote>
        <div className='flex flex-wrap items-center gap-3 text-xs text-muted-foreground'>
          <TimeAgo iso={item.created_at} />
          {item.thread.muted && (
            <span className='inline-flex items-center gap-1'>
              <BellOff className='size-3.5' aria-hidden='true' />
              {t('notifications.muted')}
            </span>
          )}
          <ExternalLink
            href={item.comment.url ?? permalink(item.thread.url, item.comment.id)}
            className='text-foreground'
            onClick={() => unread && onRead(item.id)}
          >
            {t('notifications.view')}
          </ExternalLink>
        </div>
      </div>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant='ghost' size='icon' className='shrink-0' aria-label={t('common.actions')}>
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end'>
          {unread && (
            <DropdownMenuItem onSelect={() => onRead(item.id)}>
              <CheckCheck />
              {t('notifications.markRead')}
            </DropdownMenuItem>
          )}
          <DropdownMenuItem onSelect={() => onMute(item)}>
            {item.thread.muted ? <BellRing /> : <BellOff />}
            {item.thread.muted ? t('notifications.unmute') : t('notifications.mute')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </li>
  )
}

export default function NotificationsPage() {
  const { t } = useI18n()
  const { query, items, firstPage } = useCursorList(keys.notifications, (cursor) =>
    api.me.notifications(cursor)
  )
  const unread = firstPage?.unread_count ?? items.filter((n) => !n.read_at).length

  const markRead = useApiMutation(api.me.markRead, { invalidate: [keys.notifications] })
  const markAll = useApiMutation(() => api.me.markRead({ all: true }), {
    invalidate: [keys.notifications],
    onSuccess: () => toast.success(t('notifications.allRead')),
  })
  const mute = useApiMutation(
    (item: Notification) =>
      item.thread.muted ? api.me.unmute(item.thread.id) : api.me.mute(item.thread.id),
    {
      invalidate: [keys.notifications],
      onSuccess: (_, item) =>
        toast.success(
          t(item.thread.muted ? 'notifications.unmutedToast' : 'notifications.mutedToast', {
            title: item.thread.title || t('notifications.untitled'),
          })
        ),
    }
  )

  return (
    <>
      <PageHeader title={t('notifications.title')} description={t('notifications.desc')}>
        <Button
          variant='outline'
          onClick={() => markAll.mutate(undefined)}
          disabled={!unread || markAll.isPending}
        >
          {markAll.isPending ? <Spinner /> : <CheckCheck />}
          {t('notifications.markAllRead')}
        </Button>
      </PageHeader>
      <CursorList
        query={query}
        items={items}
        empty={<EmptyState icon={Bell}>{t('notifications.empty')}</EmptyState>}
      >
        {(list) => (
          <ul className='space-y-3' aria-label={t('notifications.title')}>
            {list.map((item) => (
              <NotificationItem
                key={item.id}
                item={item}
                onRead={(id) => markRead.mutate({ ids: [id] })}
                onMute={(n) => mute.mutate(n)}
              />
            ))}
          </ul>
        )}
      </CursorList>
    </>
  )
}
