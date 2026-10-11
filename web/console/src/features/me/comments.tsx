import { useState } from 'react'
import { useQueryClient, type InfiniteData } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router'
import { MessageSquareText, Pin, Reply, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { MyComment, Page, SiteRef } from '@/api'
import { useI18n } from '@/i18n'
import { FEATURES } from '@/lib/policy'
import { permalink } from '@/lib/permalink'
import { keys, useApiMutation, useCursorList } from '@/hooks/use-api'
import { ConfirmDialog } from '@/components/common/confirm-dialog'
import {
  CommentBody,
  CommentStatusBadge,
  CursorList,
  EmptyState,
  ExternalLink,
  PageHeader,
  TimeAgo,
} from '@/components/common'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

const ALL = '__all__'

function StatusHint({ comment }: { comment: MyComment }) {
  const { t } = useI18n()
  if (comment.status === 'published') return null
  return <p className='text-xs text-muted-foreground'>{t(`myComments.hint.${comment.status}`)}</p>
}

function CommentItem({ comment, onDelete }: { comment: MyComment; onDelete: (c: MyComment) => void }) {
  const { t } = useI18n()
  return (
    <li className='space-y-3 rounded-lg border p-4'>
      <div className='flex flex-wrap items-center gap-x-2 gap-y-1 text-sm'>
        <span className='font-medium'>{comment.site.name}</span>
        <span className='text-muted-foreground' aria-hidden='true'>
          {'\u00b7'}
        </span>
        <ExternalLink href={comment.thread.url} className='min-w-0 truncate text-muted-foreground'>
          {comment.thread.title || comment.thread.url}
        </ExternalLink>
      </div>
      <CommentBody html={comment.body_html} />
      <div className='flex flex-wrap items-center gap-2 text-xs text-muted-foreground'>
        <CommentStatusBadge status={comment.status} />
        {comment.root_id && (
          <Badge variant='secondary'>
            <Reply />
            {t('myComments.reply')}
          </Badge>
        )}
        {comment.pinned && (
          <Badge variant='secondary'>
            <Pin />
            {t('myComments.pinned')}
          </Badge>
        )}
        <TimeAgo iso={comment.created_at} />
        {comment.edited_at && <span>{t('myComments.edited')}</span>}
        {comment.status !== 'deleted' && (
          <>
            <ExternalLink href={comment.url ?? permalink(comment.thread.url, comment.id)} className='ms-auto text-foreground'>
              {t('myComments.view')}
            </ExternalLink>
            <Button
              variant='ghost'
              size='sm'
              onClick={() => onDelete(comment)}
              aria-label={t('myComments.deleteLabel', { site: comment.site.name })}
            >
              <Trash2 />
              {t('myComments.delete')}
            </Button>
          </>
        )}
      </div>
      <StatusHint comment={comment} />
    </li>
  )
}

export default function MyCommentsPage() {
  const { t } = useI18n()
  const [params, setParams] = useSearchParams()
  const site = params.get('site') ?? undefined
  const { query, items } = useCursorList(keys.myComments(site), (cursor) =>
    api.me.comments({ site, cursor })
  )
  const [target, setTarget] = useState<MyComment | null>(null)
  const remove = useApiMutation((c: MyComment) => api.me.deleteComment(c.id), {
    invalidate: [['me', 'comments']],
    onSuccess: () => {
      setTarget(null)
      toast.success(t('myComments.deleted'))
    },
  })

  // The API has no "sites I commented on" list, so the filter offers the
  // sites seen in the unfiltered list loaded during this visit.
  const queryClient = useQueryClient()
  const unfiltered = queryClient.getQueryData<InfiniteData<Page<MyComment>>>(keys.myComments(undefined))
  const known = new Map<string, SiteRef>()
  for (const c of [...(unfiltered?.pages.flatMap((p) => p.data) ?? []), ...items]) known.set(c.site.id, c.site)

  return (
    <>
      <PageHeader title={t('myComments.title')} description={t('myComments.desc')} />
      <div className='mb-4 flex flex-wrap items-center gap-2'>
        <Label htmlFor='site-filter' className='text-sm text-muted-foreground'>
          {t('myComments.filterSite')}
        </Label>
        <Select
          value={site ?? ALL}
          onValueChange={(value) =>
            setParams(value === ALL ? {} : { site: value }, { replace: true })
          }
        >
          <SelectTrigger id='site-filter' className='w-56'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>{t('myComments.allSites')}</SelectItem>
            {[...known.values()].map((s) => (
              <SelectItem key={s.id} value={s.id}>
                {s.name}
              </SelectItem>
            ))}
            {site && !known.has(site) && (
              <SelectItem value={site}>{site}</SelectItem>
            )}
          </SelectContent>
        </Select>
      </div>
      <CursorList
        query={query}
        items={items}
        empty={
          <EmptyState icon={MessageSquareText}>
            {site ? t('myComments.emptyFiltered') : t('myComments.empty')}
          </EmptyState>
        }
      >
        {(list) => (
          <ul className='space-y-3' aria-label={t('myComments.title')}>
            {list.map((comment) => (
              <CommentItem key={comment.id} comment={comment} onDelete={setTarget} />
            ))}
          </ul>
        )}
      </CursorList>
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(open) => !open && setTarget(null)}
        title={t('myComments.deleteTitle')}
        description={t('myComments.deleteDesc')}
        confirmText={t('myComments.delete')}
        destructive
        isLoading={remove.isPending}
        onConfirm={() => target && remove.mutate(target)}
      />
      {FEATURES.claims && (
        <p className='mt-6 text-sm text-muted-foreground'>
          <Link to='/me/claims' className='underline underline-offset-4 hover:text-foreground'>
            {t('myComments.claimLink')}
          </Link>
        </p>
      )}
    </>
  )
}
