import { useState } from 'react'
import { Link } from 'react-router'
import { ArrowRightLeft, FileText, Lock, LockOpen, MessageSquare, MoreHorizontal, Pencil } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { Thread, ThreadPatch } from '@/api'
import { useI18n } from '@/i18n'
import { keys, useApiMutation, useCursorList } from '@/hooks/use-api'
import { CursorList, EmptyState, ExternalLink, PageHeader, Spinner, TimeAgo } from '@/components/common'
import { Field, describedBy } from '@/components/common/form'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { NotFound } from '@/features/errors'
import { useSite } from './site-context'

/** datetime-local works in local time without a zone; the API wants UTC. */
function toLocalInput(iso: string | null) {
  if (!iso) return ''
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function EditDialog({
  siteId,
  thread,
  onClose,
}: {
  siteId: string
  thread: Thread
  onClose: () => void
}) {
  const { t } = useI18n()
  const [title, setTitle] = useState(thread.title)
  const [url, setUrl] = useState(thread.url)
  const [published, setPublished] = useState(toLocalInput(thread.page_published_at))
  const [error, setError] = useState<string | null>(null)
  const save = useApiMutation((patch: ThreadPatch) => api.sites.updateThread(siteId, thread.id, patch), {
    invalidate: [keys.threads(siteId), keys.site(siteId)],
    onSuccess: () => {
      toast.success(t('threads.saved'))
      onClose()
    },
  })

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (!/^https:\/\/\S+$/.test(url.trim())) {
      setError(t('threads.urlInvalid'))
      return
    }
    setError(null)
    const patch: ThreadPatch = {}
    if (title.trim() !== thread.title) patch.title = title.trim()
    if (url.trim() !== thread.url) patch.url = url.trim()
    const nextPublished = published ? new Date(published).toISOString() : null
    if (toLocalInput(nextPublished) !== toLocalInput(thread.page_published_at)) {
      if (nextPublished) patch.published_at = nextPublished
      else patch.clear_published_at = true
    }
    if (!Object.keys(patch).length) return onClose()
    save.mutate(patch)
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('threads.editTitle')}</DialogTitle>
          <DialogDescription>{t('threads.editDesc')}</DialogDescription>
        </DialogHeader>
        <form id='thread-form' onSubmit={submit} className='space-y-4' noValidate>
          <Field id='thread-title' label={t('threads.fieldTitle')}>
            <Input id='thread-title' value={title} maxLength={300} onChange={(e) => setTitle(e.target.value)} />
          </Field>
          <Field id='thread-url' label={t('threads.fieldUrl')} error={error}>
            <Input
              {...describedBy('thread-url', error)}
              type='url'
              value={url}
              maxLength={2000}
              onChange={(e) => setUrl(e.target.value)}
            />
          </Field>
          <Field id='thread-published' label={t('threads.fieldPublished')} hint={t('threads.publishedHint')}>
            <Input
              {...describedBy('thread-published', null, true)}
              type='datetime-local'
              value={published}
              onChange={(e) => setPublished(e.target.value)}
            />
          </Field>
        </form>
        <DialogFooter>
          <Button variant='outline' onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button type='submit' form='thread-form' disabled={save.isPending}>
            {save.isPending && <Spinner />}
            {t('common.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function MoveDialog({
  siteId,
  thread,
  onClose,
}: {
  siteId: string
  thread: Thread
  onClose: () => void
}) {
  const { t } = useI18n()
  const [toKey, setToKey] = useState('')
  const [error, setError] = useState<string | null>(null)
  const move = useApiMutation((key: string) => api.sites.moveThread(siteId, thread.id, key), {
    invalidate: [keys.threads(siteId), keys.site(siteId)],
    onSuccess: (result) => {
      toast.success(
        result.moved === null
          ? t('threads.movedDone')
          : result.merged
            ? t('threads.merged', { count: result.moved })
            : t('threads.moved', { count: result.moved })
      )
      onClose()
    },
    onError: (err) => {
      if (api.isApiError(err, 'invalid_request')) {
        setError(err.message)
        return true
      }
    },
  })

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const key = toKey.trim()
    if (!/^[kph]:\S/.test(key)) {
      setError(t('threads.keyInvalid'))
      return
    }
    if (key === thread.key) {
      setError(t('threads.keySame'))
      return
    }
    setError(null)
    move.mutate(key)
  }

  const hint = t('threads.moveToHint')
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('threads.moveTitle')}</DialogTitle>
          <DialogDescription>{t('threads.moveDesc')}</DialogDescription>
        </DialogHeader>
        <form id='move-form' onSubmit={submit} className='space-y-4' noValidate>
          <div className='space-y-1 text-sm'>
            <p className='font-medium'>{t('threads.moveFrom')}</p>
            <p className='font-mono text-xs break-all text-muted-foreground'>{thread.key}</p>
          </div>
          <Field id='move-to' label={t('threads.moveTo')} hint={hint} error={error}>
            <Input
              {...describedBy('move-to', error, hint)}
              value={toKey}
              placeholder='p:/posts/new-path'
              autoCapitalize='off'
              spellCheck={false}
              className='font-mono'
              onChange={(e) => setToKey(e.target.value)}
            />
          </Field>
        </form>
        <DialogFooter>
          <Button variant='outline' onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button type='submit' form='move-form' disabled={move.isPending}>
            {move.isPending && <Spinner />}
            {t('threads.moveButton')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export default function ThreadsPage() {
  const { t } = useI18n()
  const site = useSite()
  const { query, items } = useCursorList(keys.threads(site.id), (cursor) =>
    api.sites.threads(site.id, cursor)
  )
  const [editing, setEditing] = useState<Thread | null>(null)
  const [moving, setMoving] = useState<Thread | null>(null)

  const lock = useApiMutation(
    (thread: Thread) => api.sites.updateThread(site.id, thread.id, { locked: !thread.locked }),
    {
      invalidate: [keys.threads(site.id), keys.events(site.id)],
      onSuccess: (updated) =>
        toast.success(
          t(updated.locked ? 'threads.lockedToast' : 'threads.unlockedToast', {
            title: updated.title || updated.key,
          })
        ),
    }
  )

  if (site.status === 'deleted') return <NotFound />

  return (
    <>
      <PageHeader title={t('threads.title')} description={t('threads.desc')} />
      <CursorList
        query={query}
        items={items}
        empty={<EmptyState icon={FileText}>{t('threads.empty')}</EmptyState>}
      >
        {(list) => (
          <ul className='divide-y rounded-lg border' aria-label={t('threads.title')}>
            {list.map((thread) => (
              <li key={thread.id} className='flex flex-wrap items-start gap-3 p-4'>
                <div className='min-w-0 flex-1 space-y-1'>
                  <div className='flex flex-wrap items-center gap-2'>
                    <ExternalLink href={thread.url} className='min-w-0 font-medium break-words'>
                      {thread.title || thread.url}
                    </ExternalLink>
                    {thread.locked && (
                      <Badge variant='outline'>
                        <Lock />
                        {t('threads.locked')}
                      </Badge>
                    )}
                    {thread.auto_closed && !thread.locked && (
                      <Badge variant='outline'>{t('threads.autoClosed')}</Badge>
                    )}
                  </div>
                  <p className='font-mono text-xs break-all text-muted-foreground'>{thread.key}</p>
                  <p className='flex flex-wrap gap-x-3 text-xs text-muted-foreground'>
                    <span>{t('threads.comments', { count: thread.comment_count })}</span>
                    {thread.last_comment_at && (
                      <span>
                        {t('threads.lastComment')} <TimeAgo iso={thread.last_comment_at} />
                      </span>
                    )}
                  </p>
                </div>
                <div className='flex items-center gap-1'>
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={lock.isPending}
                    onClick={() => lock.mutate(thread)}
                  >
                    {thread.locked ? <LockOpen /> : <Lock />}
                    {thread.locked ? t('threads.unlock') : t('threads.lock')}
                  </Button>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        size='icon'
                        variant='ghost'
                        aria-label={t('threads.more', { title: thread.title || thread.key })}
                      >
                        <MoreHorizontal />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align='end'>
                      <DropdownMenuItem asChild>
                        <Link to={`/console/sites/${site.id}/comments?thread=${encodeURIComponent(thread.id)}&status=all`}>
                          <MessageSquare />
                          {t('threads.viewComments')}
                        </Link>
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={() => setEditing(thread)}>
                        <Pencil />
                        {t('threads.edit')}
                      </DropdownMenuItem>
                      {site.role === 'owner' && (
                        <>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem onSelect={() => setMoving(thread)}>
                            <ArrowRightLeft />
                            {t('threads.move')}
                          </DropdownMenuItem>
                        </>
                      )}
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              </li>
            ))}
          </ul>
        )}
      </CursorList>
      {editing && <EditDialog siteId={site.id} thread={editing} onClose={() => setEditing(null)} />}
      {moving && <MoveDialog siteId={site.id} thread={moving} onClose={() => setMoving(null)} />}
    </>
  )
}
