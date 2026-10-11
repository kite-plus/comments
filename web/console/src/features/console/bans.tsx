import { useState } from 'react'
import { Ban as BanIcon, Globe2, Plus } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { Ban } from '@/api'
import { useI18n } from '@/i18n'
import { keys, useApiMutation, useCursorList } from '@/hooks/use-api'
import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { CursorList, EmptyState, PageHeader, Spinner, TimeAgo } from '@/components/common'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { NotFound } from '@/features/errors'
import { DurationSelect, banDays, type BanDuration } from './ban-dialog'
import { useSite } from './site-context'

function EmailBanDialog({ siteId, open, onOpenChange }: { siteId: string; open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useI18n()
  const [email, setEmail] = useState('')
  const [duration, setDuration] = useState<BanDuration>('forever')
  const [reason, setReason] = useState('')
  const [error, setError] = useState<string | null>(null)
  const close = () => {
    setEmail('')
    setReason('')
    setDuration('forever')
    setError(null)
    onOpenChange(false)
  }
  const ban = useApiMutation(
    () =>
      api.sites.ban(siteId, {
        kind: 'email',
        email: email.trim(),
        reason: reason.trim() || undefined,
        days: banDays(duration),
      }),
    {
      invalidate: [keys.bans(siteId), keys.events(siteId)],
      onSuccess: () => {
        toast.success(t('ban.banned'))
        close()
      },
    }
  )
  return (
    <Dialog open={open} onOpenChange={(next) => (next ? onOpenChange(true) : close())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('bans.addTitle')}</DialogTitle>
          <DialogDescription>{t('bans.addDesc')}</DialogDescription>
        </DialogHeader>
        <form
          id='email-ban-form'
          className='space-y-4'
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email.trim())) {
              setError(t('validation.email'))
              return
            }
            setError(null)
            ban.mutate(undefined)
          }}
        >
          <Field id='ban-email' label={t('bans.email')} error={error}>
            <Input
              {...describedBy('ban-email', error)}
              type='email'
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </Field>
          <div className='space-y-2'>
            <Label htmlFor='email-ban-duration'>{t('ban.durationLabel')}</Label>
            <DurationSelect id='email-ban-duration' value={duration} onChange={setDuration} />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='email-ban-reason'>{t('ban.reason')}</Label>
            <Textarea id='email-ban-reason' maxLength={500} value={reason} onChange={(e) => setReason(e.target.value)} />
          </div>
        </form>
        <DialogFooter>
          <Button variant='outline' onClick={close}>
            {t('common.cancel')}
          </Button>
          <Button type='submit' form='email-ban-form' variant='destructive' disabled={ban.isPending}>
            {ban.isPending && <Spinner />}
            {t('ban.submit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export default function BansPage() {
  const { t, formatDate } = useI18n()
  const site = useSite()
  const { query, items } = useCursorList(keys.bans(site.id), (cursor) => api.sites.bans(site.id, cursor))
  const [adding, setAdding] = useState(false)
  const [lifting, setLifting] = useState<Ban | null>(null)
  const unban = useApiMutation((ban: Ban) => api.sites.unban(site.id, ban.id), {
    invalidate: [keys.bans(site.id), keys.events(site.id)],
    onSuccess: () => {
      setLifting(null)
      toast.success(t('bans.unbanned'))
    },
  })

  if (site.status === 'deleted') return <NotFound />

  return (
    <>
      <PageHeader title={t('bans.title')} description={t('bans.desc')}>
        <Button variant='outline' onClick={() => setAdding(true)}>
          <Plus />
          {t('bans.add')}
        </Button>
      </PageHeader>
      <CursorList
        query={query}
        items={items}
        empty={<EmptyState icon={BanIcon}>{t('bans.empty')}</EmptyState>}
      >
        {(list) => (
          <ul className='divide-y rounded-lg border' aria-label={t('bans.title')}>
            {list.map((ban) => (
              <li key={ban.id} className='flex flex-wrap items-center gap-3 p-4'>
                <div className='min-w-0 flex-1 space-y-1'>
                  <div className='flex flex-wrap items-center gap-2'>
                    <Badge variant='outline'>{t(`bans.target.${ban.target}`)}</Badge>
                    <span className='font-medium break-all'>{ban.label}</span>
                    {ban.site_id === null && (
                      <Badge variant='secondary' title={t('bans.platformHint')}>
                        <Globe2 />
                        {t('bans.platform')}
                      </Badge>
                    )}
                  </div>
                  {ban.reason && <p className='text-sm text-muted-foreground break-words'>{ban.reason}</p>}
                  <p className='flex flex-wrap gap-x-3 text-xs text-muted-foreground'>
                    <span>
                      {t('bans.created')} <TimeAgo iso={ban.created_at} />
                    </span>
                    <span>
                      {ban.expires_at ? t('bans.expires', { date: formatDate(ban.expires_at) }) : t('bans.forever')}
                    </span>
                  </p>
                </div>
                {ban.site_id !== null && (
                  <Button size='sm' variant='outline' onClick={() => setLifting(ban)}>
                    {t('bans.unban')}
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </CursorList>
      <EmailBanDialog siteId={site.id} open={adding} onOpenChange={setAdding} />
      <ConfirmDialog
        open={!!lifting}
        onOpenChange={(open) => !open && setLifting(null)}
        title={t('bans.unbanTitle', { label: lifting?.label ?? '' })}
        description={t('bans.unbanDesc')}
        confirmText={t('bans.unban')}
        isLoading={unban.isPending}
        onConfirm={() => lifting && unban.mutate(lifting)}
      />
    </>
  )
}
