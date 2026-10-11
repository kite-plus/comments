import { Link } from 'react-router'
import { Flag, Mail, Pin, Reply } from 'lucide-react'
import type { ConsoleComment } from '@/api'
import { useI18n } from '@/i18n'
import { permalink } from '@/lib/permalink'
import { cn } from '@/lib/utils'
import {
  CommentBody,
  CommentStatusBadge,
  ExternalLink,
  TimeAgo,
  UserAvatar,
} from '@/components/common'
import { Badge } from '@/components/ui/badge'

/** One comment as moderators see it, with room for a checkbox and actions. */
export function ConsoleCommentCard({
  comment,
  siteId,
  leading,
  actions,
  className,
}: {
  comment: ConsoleComment
  siteId: string
  leading?: React.ReactNode
  actions?: React.ReactNode
  className?: string
}) {
  const { t } = useI18n()
  const { author } = comment
  // Deleted comments come back as placeholders without an author.
  const name = author.kind === 'former_user' ? t('author.former') : author.name || '\u2014'
  return (
    <article
      className={cn('flex gap-3 rounded-lg border bg-card p-4', className)}
      aria-labelledby={`c-${comment.id}-author`}
    >
      {leading}
      <UserAvatar name={name} seed={author.id ?? author.email ?? name} src={author.avatar} />
      <div className='min-w-0 flex-1 space-y-2'>
        <div className='flex flex-wrap items-center gap-x-2 gap-y-1 text-sm'>
          <span id={`c-${comment.id}-author`} className='font-medium'>
            {author.url ? (
              <a
                href={author.url}
                target='_blank'
                rel='ugc nofollow noopener noreferrer'
                className='underline-offset-4 hover:underline'
              >
                {name}
              </a>
            ) : (
              name
            )}
          </span>
          {author.badge === 'owner' && <Badge variant='secondary'>{t('author.owner')}</Badge>}
          {author.kind === 'guest' && <Badge variant='outline'>{t('author.guest')}</Badge>}
          {author.kind === 'external' && author.name && <Badge variant='outline'>{t('author.external')}</Badge>}
          {author.email && (
            <span className='inline-flex items-center gap-1 text-xs text-muted-foreground'>
              <Mail className='size-3.5' aria-hidden='true' />
              <span className='sr-only'>{t('author.email')}</span>
              {author.email}
            </span>
          )}
          <TimeAgo iso={comment.created_at} className='text-xs text-muted-foreground' />
          {comment.edited_at && <span className='text-xs text-muted-foreground'>{t('myComments.edited')}</span>}
        </div>
        <div className='flex flex-wrap items-center gap-2 text-xs text-muted-foreground'>
          <CommentStatusBadge status={comment.status} />
          {comment.pinned && (
            <Badge variant='secondary'>
              <Pin />
              {t('myComments.pinned')}
            </Badge>
          )}
          {!!comment.report_count && (
            <Badge variant='outline' className='border-destructive/40 text-destructive'>
              <Flag />
              {t('queue.reportCount', { count: comment.report_count })}
            </Badge>
          )}
          {comment.root_id && (
            <span className='inline-flex items-center gap-1'>
              <Reply className='size-3.5' aria-hidden='true' />
              {comment.reply_to ? t('queue.replyTo', { name: comment.reply_to.name }) : t('myComments.reply')}
            </span>
          )}
          <Link
            to={`/console/sites/${siteId}/comments?thread=${encodeURIComponent(comment.thread.id)}&status=all`}
            className='min-w-0 truncate underline-offset-4 hover:underline'
            title={t('queue.filterByThread')}
          >
            {comment.thread.title || comment.thread.key}
          </Link>
        </div>
        <CommentBody html={comment.body_html} />
        <div className='flex flex-wrap items-center gap-2'>
          {actions}
          {comment.status === 'published' && (
            <ExternalLink
              href={comment.url ?? permalink(comment.thread.url, comment.id)}
              className='ms-auto text-xs text-muted-foreground'
            >
              {t('queue.viewOnPage')}
            </ExternalLink>
          )}
        </div>
      </div>
    </article>
  )
}
