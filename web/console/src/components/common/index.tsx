import { useEffect, useState } from 'react'
import type { InfiniteData, UseInfiniteQueryResult } from '@tanstack/react-query'
import { Check, Copy, ExternalLink as ExternalIcon, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import type { CommentStatus, Page, SiteStatus } from '@/api'
import { useI18n, useT } from '@/i18n'
import { avatarColor, initial } from '@/lib/avatar'
import { cn } from '@/lib/utils'
import { errorMessage } from '@/hooks/use-api'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

/** A page's heading row, as each shadcn-admin page writes it. */
export function PageHeader({
  title,
  description,
  children,
}: {
  title: string
  description?: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <div className='mb-6 flex flex-wrap items-end justify-between gap-3'>
      <div className='min-w-0 space-y-1'>
        <h1 className='text-2xl font-bold tracking-tight'>{title}</h1>
        {description && <p className='max-w-2xl text-muted-foreground'>{description}</p>}
      </div>
      {children && <div className='flex flex-wrap gap-2'>{children}</div>}
    </div>
  )
}

/** Shown in place of a list or page when its data fails to load. */
export function QueryError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const t = useT()
  return (
    <div
      role='alert'
      className='flex flex-col items-center gap-3 rounded-lg border border-dashed p-10 text-center'
    >
      <p className='font-medium'>{t('common.loadError')}</p>
      <p className='text-sm text-muted-foreground'>{errorMessage(t, error)}</p>
      <Button variant='outline' size='sm' onClick={onRetry}>
        {t('common.retry')}
      </Button>
    </div>
  )
}

export function EmptyState({
  icon: Icon,
  children,
  action,
}: {
  icon?: React.ElementType
  children: React.ReactNode
  action?: React.ReactNode
}) {
  return (
    <div className='flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-12 text-center text-sm text-muted-foreground'>
      {Icon && <Icon className='size-8 text-muted-foreground/60' aria-hidden='true' />}
      <div className='max-w-md'>{children}</div>
      {action}
    </div>
  )
}

export function ListSkeleton({ rows = 3 }: { rows?: number }) {
  const t = useT()
  return (
    <div className='space-y-3' role='status' aria-label={t('common.loading')}>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className='space-y-2 rounded-lg border p-4'>
          <Skeleton className='h-4 w-1/3' />
          <Skeleton className='h-4 w-full' />
          <Skeleton className='h-4 w-2/3' />
        </div>
      ))}
    </div>
  )
}

export function Spinner({ className }: { className?: string }) {
  return <Loader2 className={cn('size-4 animate-spin', className)} aria-hidden='true' />
}

/**
 * Loading, error and empty states for a cursor-paged list, then the items
 * and a "load more" button while the server has more.
 */
export function CursorList<T>({
  query,
  items,
  empty,
  children,
  skeletonRows,
}: {
  query: UseInfiniteQueryResult<InfiniteData<Page<T>, string | null>, Error>
  items: T[]
  empty: React.ReactNode
  children: (items: T[]) => React.ReactNode
  skeletonRows?: number
}) {
  const t = useT()
  if (query.isPending) return <ListSkeleton rows={skeletonRows} />
  if (query.isError && !items.length)
    return <QueryError error={query.error} onRetry={() => void query.refetch()} />
  if (!items.length) return <>{empty}</>
  return (
    <>
      {children(items)}
      {query.hasNextPage && (
        <div className='mt-4 flex justify-center'>
          <Button
            variant='outline'
            onClick={() => void query.fetchNextPage()}
            disabled={query.isFetchingNextPage}
          >
            {query.isFetchingNextPage && <Spinner />}
            {t('common.loadMore')}
          </Button>
        </div>
      )}
      {query.isFetchNextPageError && (
        <p role='alert' className='mt-2 text-center text-sm text-destructive'>
          {errorMessage(t, query.error)}
        </p>
      )}
    </>
  )
}

/** Relative time that refreshes every minute; the title holds the exact time. */
export function TimeAgo({ iso, className }: { iso: string | null | undefined; className?: string }) {
  const { formatDateTime, formatRelative } = useI18n()
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60_000)
    return () => window.clearInterval(timer)
  }, [])
  if (!iso) return <span className={className}>{'\u2014'}</span>
  return (
    <time dateTime={iso} title={formatDateTime(iso)} className={className}>
      {formatRelative(iso, now)}
    </time>
  )
}

const COMMENT_STATUS_STYLE: Record<CommentStatus, string> = {
  pending: 'border-warning/40 bg-warning/10 text-warning',
  published: 'border-success/40 bg-success/10 text-success',
  spam: 'border-destructive/40 bg-destructive/10 text-destructive',
  hidden: 'border-border bg-muted text-muted-foreground',
  deleted: 'border-border bg-muted text-muted-foreground line-through',
}

export function CommentStatusBadge({ status }: { status: CommentStatus }) {
  const t = useT()
  return (
    <Badge variant='outline' className={COMMENT_STATUS_STYLE[status]}>
      {t(`status.comment.${status}`)}
    </Badge>
  )
}

const SITE_STATUS_STYLE: Record<SiteStatus, string> = {
  unverified: 'border-warning/40 bg-warning/10 text-warning',
  pending_review: 'border-info/40 bg-info/10 text-info',
  active: 'border-success/40 bg-success/10 text-success',
  suspended: 'border-destructive/40 bg-destructive/10 text-destructive',
  deleted: 'border-border bg-muted text-muted-foreground',
}

export function SiteStatusBadge({ status }: { status: SiteStatus }) {
  const t = useT()
  return (
    <Badge variant='outline' className={SITE_STATUS_STYLE[status]}>
      {t(`status.site.${status}`)}
    </Badge>
  )
}

/**
 * Comment HTML as the server rendered and sanitized it (goldmark plus
 * bluemonday, moderation.md section 5). The browser never renders Markdown.
 */
export function CommentBody({ html, className }: { html: string; className?: string }) {
  return (
    <div
      className={cn('comment-body text-sm', className)}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}

export function UserAvatar({
  name,
  seed,
  src,
  className,
}: {
  name: string
  seed: string
  src?: string | null
  className?: string
}) {
  return (
    <Avatar className={cn('size-8 text-sm', className)} aria-hidden='true'>
      {src && <AvatarImage src={src} alt='' />}
      <AvatarFallback className={cn('font-medium text-white', avatarColor(seed))}>
        {initial(name)}
      </AvatarFallback>
    </Avatar>
  )
}

export function ExternalLink({
  href,
  children,
  className,
  onClick,
}: {
  href: string
  children: React.ReactNode
  className?: string
  onClick?: () => void
}) {
  const t = useT()
  return (
    <a
      href={href}
      target='_blank'
      rel='noopener noreferrer'
      onClick={onClick}
      className={cn('inline-flex items-center gap-1 underline-offset-4 hover:underline', className)}
    >
      {children}
      <ExternalIcon className='size-3.5 shrink-0' aria-hidden='true' />
      <span className='sr-only'>{t('common.opensInNewTab')}</span>
    </a>
  )
}

async function writeClipboard(text: string) {
  if (navigator.clipboard?.writeText) return navigator.clipboard.writeText(text)
  throw new Error('Clipboard unavailable')
}

export function CopyButton({
  value,
  label,
  className,
}: {
  value: string
  label: string
  className?: string
}) {
  const t = useT()
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) return
    const timer = window.setTimeout(() => setCopied(false), 2000)
    return () => window.clearTimeout(timer)
  }, [copied])
  return (
    <Button
      type='button'
      variant='outline'
      size='sm'
      className={className}
      aria-label={t('common.copyLabel', { label })}
      onClick={() =>
        writeClipboard(value).then(
          () => {
            setCopied(true)
            toast.success(t('common.copied'))
          },
          () => toast.error(t('common.copyFailed'))
        )
      }
    >
      {copied ? <Check /> : <Copy />}
      <span aria-hidden='true'>{copied ? t('common.copied') : t('common.copy')}</span>
    </Button>
  )
}

/** A value to paste somewhere else, with its own copy button. */
export function CopyField({ label, value }: { label: string; value: string }) {
  return (
    <div className='space-y-1.5'>
      <div className='flex items-center justify-between gap-2'>
        <span className='text-xs font-medium text-muted-foreground'>{label}</span>
        <CopyButton value={value} label={label} />
      </div>
      <pre className='rounded-md border bg-muted/50 px-3 py-2 font-mono text-xs break-all whitespace-pre-wrap'>
        {value}
      </pre>
    </div>
  )
}
