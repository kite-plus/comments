import { useState } from 'react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { BanTarget, ConsoleComment } from '@/api'
import { useI18n } from '@/i18n'
import { keys, useApiMutation } from '@/hooks/use-api'
import { Spinner } from '@/components/common'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'

export const BAN_DURATIONS = ['forever', '1', '7', '30', '365'] as const
export type BanDuration = (typeof BAN_DURATIONS)[number]

export function expiresAt(duration: BanDuration): string | null {
  if (duration === 'forever') return null
  return new Date(Date.now() + Number(duration) * 86_400_000).toISOString()
}

export function DurationSelect({
  id,
  value,
  onChange,
}: {
  id: string
  value: BanDuration
  onChange: (value: BanDuration) => void
}) {
  const { t } = useI18n()
  return (
    <Select value={value} onValueChange={(v) => onChange(v as BanDuration)}>
      <SelectTrigger id={id} className='w-40'>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {BAN_DURATIONS.map((d) => (
          <SelectItem key={d} value={d}>
            {t(`ban.duration.${d}`)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

/** Targets moderation.md 4.1 allows for this comment's author. */
function targetsFor(comment: ConsoleComment): BanTarget[] {
  const targets: BanTarget[] = []
  if (comment.author.kind === 'user') targets.push('user')
  if (comment.author.kind === 'guest' && comment.author.email) targets.push('email')
  targets.push('ip')
  return targets
}

export function BanDialog({
  siteId,
  comment,
  onOpenChange,
}: {
  siteId: string
  comment: ConsoleComment | null
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useI18n()
  const targets = comment ? targetsFor(comment) : []
  const [target, setTarget] = useState<BanTarget | null>(null)
  const [duration, setDuration] = useState<BanDuration>('forever')
  const [reason, setReason] = useState('')
  const [alsoHide, setAlsoHide] = useState(true)
  const chosen = target && targets.includes(target) ? target : targets[0]

  const reset = () => {
    setTarget(null)
    setDuration('forever')
    setReason('')
    setAlsoHide(true)
  }

  const ban = useApiMutation(
    async (c: ConsoleComment) => {
      await api.sites.ban(siteId, {
        target: chosen,
        comment_id: c.id,
        reason: reason.trim() || undefined,
        expires_at: expiresAt(duration),
      })
      if (alsoHide && c.status !== 'hidden' && c.status !== 'spam' && c.status !== 'deleted') {
        await api.sites.moderate(siteId, [c.id], 'hide')
      }
    },
    {
      invalidate: [keys.site(siteId), keys.sites],
      onSuccess: () => {
        toast.success(t('ban.banned'))
        reset()
        onOpenChange(false)
      },
    }
  )

  const labelFor = (value: BanTarget) =>
    value === 'email'
      ? t('ban.target.email', { email: comment?.author.email ?? '' })
      : t(`ban.target.${value}`, { name: comment?.author.name ?? '' })

  return (
    <Dialog
      open={!!comment}
      onOpenChange={(open) => {
        if (!open) reset()
        onOpenChange(open)
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('ban.title')}</DialogTitle>
          <DialogDescription>{t('ban.desc')}</DialogDescription>
        </DialogHeader>
        {comment && (
          <form
            id='ban-form'
            className='space-y-5'
            onSubmit={(event) => {
              event.preventDefault()
              ban.mutate(comment)
            }}
          >
            <fieldset className='space-y-3'>
              <legend className='text-sm font-medium'>{t('ban.targetLabel')}</legend>
              <RadioGroup value={chosen} onValueChange={(v) => setTarget(v as BanTarget)}>
                {targets.map((value) => (
                  <div key={value} className='flex items-start gap-3'>
                    <RadioGroupItem value={value} id={`ban-${value}`} className='mt-0.5' />
                    <div className='space-y-1'>
                      <Label htmlFor={`ban-${value}`}>{labelFor(value)}</Label>
                      {value === 'ip' && (
                        <p className='text-sm text-muted-foreground'>{t('ban.ipHint')}</p>
                      )}
                    </div>
                  </div>
                ))}
              </RadioGroup>
            </fieldset>
            <div className='space-y-2'>
              <Label htmlFor='ban-duration'>{t('ban.durationLabel')}</Label>
              <DurationSelect id='ban-duration' value={duration} onChange={setDuration} />
            </div>
            <div className='space-y-2'>
              <Label htmlFor='ban-reason'>{t('ban.reason')}</Label>
              <Textarea
                id='ban-reason'
                aria-describedby='ban-reason-hint'
                maxLength={500}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
              <p id='ban-reason-hint' className='text-sm text-muted-foreground'>
                {t('ban.reasonHint')}
              </p>
            </div>
            {comment.status !== 'hidden' && comment.status !== 'spam' && (
              <div className='flex items-center gap-2'>
                <Checkbox
                  id='ban-hide'
                  checked={alsoHide}
                  onCheckedChange={(checked) => setAlsoHide(checked === true)}
                />
                <Label htmlFor='ban-hide'>{t('ban.alsoHide')}</Label>
              </div>
            )}
          </form>
        )}
        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)} disabled={ban.isPending}>
            {t('common.cancel')}
          </Button>
          <Button type='submit' form='ban-form' variant='destructive' disabled={ban.isPending}>
            {ban.isPending && <Spinner />}
            {t('ban.submit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
