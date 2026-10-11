import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import * as api from '@/api'
import type { AvatarKind, Locale, Me, MePatch } from '@/api'
import { ME_KEY, useMe } from '@/context/auth'
import { useI18n } from '@/i18n'
import { POLICY } from '@/lib/policy'
import { useApiMutation } from '@/hooks/use-api'
import { PageHeader, Spinner, UserAvatar } from '@/components/common'
import { Field, Section, describedBy } from '@/components/common/form'
import { LANGS } from '@/components/layout/preferences'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

type Draft = {
  display_name: string
  homepage_url: string
  avatar_kind: AvatarKind
  notify_replies: boolean
  locale: Locale
}

function toDraft(me: Me): Draft {
  return {
    display_name: me.display_name,
    homepage_url: me.homepage_url ?? '',
    avatar_kind: me.avatar_kind,
    notify_replies: me.notify_replies,
    locale: me.locale,
  }
}

function diff(me: Me, draft: Draft): MePatch {
  const patch: MePatch = {}
  const name = draft.display_name.trim()
  if (name !== me.display_name) patch.display_name = name
  const homepage = draft.homepage_url.trim() || null
  if (homepage !== me.homepage_url) patch.homepage_url = homepage
  if (draft.avatar_kind !== me.avatar_kind) patch.avatar_kind = draft.avatar_kind
  if (draft.notify_replies !== me.notify_replies) patch.notify_replies = draft.notify_replies
  if (draft.locale !== me.locale) patch.locale = draft.locale
  return patch
}

/** Keeps the CSRF token when a PATCH response leaves it out. */
export function useUpdateMe(onSaved?: () => void) {
  const queryClient = useQueryClient()
  return useApiMutation((patch: MePatch) => api.me.update(patch), {
    onSuccess: (updated) => {
      queryClient.setQueryData<Me>(ME_KEY, (old) =>
        old ? { ...old, ...updated, csrf_token: updated.csrf_token ?? old.csrf_token } : updated
      )
      onSaved?.()
    },
  })
}

export default function ProfilePage() {
  const { t, formatDate } = useI18n()
  const me = useMe()
  const [draft, setDraft] = useState(() => toDraft(me))
  const [errors, setErrors] = useState<{ name?: string; homepage?: string }>({})
  const save = useUpdateMe(() => toast.success(t('profile.saved')))
  const patch = diff(me, draft)
  const dirty = Object.keys(patch).length > 0

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) =>
    setDraft((d) => ({ ...d, [key]: value }))

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const next: typeof errors = {}
    const name = draft.display_name.trim()
    if (!name) next.name = t('validation.required')
    else if (name.length > POLICY.displayNameMax)
      next.name = t('validation.tooLong', { max: POLICY.displayNameMax })
    const homepage = draft.homepage_url.trim()
    if (homepage && !/^https?:\/\/[^\s]+$/i.test(homepage)) next.homepage = t('profile.homepageInvalid')
    else if (homepage.length > POLICY.homepageMax)
      next.homepage = t('validation.tooLong', { max: POLICY.homepageMax })
    setErrors(next)
    if (Object.keys(next).length) return
    save.mutate(patch)
  }

  const nameHint = t('profile.displayNameHint')
  const homepageHint = t('profile.homepageHint')

  return (
    <>
      <PageHeader title={t('profile.title')} description={t('profile.desc')} />
      <form onSubmit={submit} className='space-y-6' noValidate>
        <Section title={t('profile.sectionPublic')}>
          <div className='flex items-center gap-3'>
            <UserAvatar
              name={draft.display_name || me.display_name}
              seed={me.id}
              src={draft.avatar_kind === 'idp' ? me.avatar_url : null}
              className='size-12 text-lg'
            />
            <div className='text-sm text-muted-foreground'>
              <p>{t('profile.memberSince', { date: formatDate(me.created_at) })}</p>
            </div>
          </div>
          <Field id='display-name' label={t('profile.displayName')} hint={nameHint} error={errors.name}>
            <Input
              {...describedBy('display-name', errors.name, nameHint)}
              value={draft.display_name}
              maxLength={POLICY.displayNameMax}
              autoComplete='nickname'
              onChange={(e) => set('display_name', e.target.value)}
            />
          </Field>
          <Field id='homepage' label={t('profile.homepage')} hint={homepageHint} error={errors.homepage}>
            <Input
              {...describedBy('homepage', errors.homepage, homepageHint)}
              type='url'
              inputMode='url'
              placeholder='https://'
              value={draft.homepage_url}
              autoComplete='url'
              onChange={(e) => set('homepage_url', e.target.value)}
            />
          </Field>
          <fieldset className='space-y-3'>
            <legend className='text-sm font-medium'>{t('profile.avatar')}</legend>
            <RadioGroup
              value={draft.avatar_kind}
              onValueChange={(value) => set('avatar_kind', value as AvatarKind)}
              className='gap-3'
            >
              {(['generated', 'idp'] as const).map((kind) => (
                <div key={kind} className='flex items-start gap-3'>
                  <RadioGroupItem
                    value={kind}
                    id={`avatar-${kind}`}
                    aria-describedby={`avatar-${kind}-hint`}
                    className='mt-0.5'
                  />
                  <div className='space-y-1'>
                    <Label htmlFor={`avatar-${kind}`}>{t(`profile.avatar.${kind}`)}</Label>
                    <p id={`avatar-${kind}-hint`} className='text-sm text-muted-foreground'>
                      {t(`profile.avatar.${kind}Hint`)}
                    </p>
                  </div>
                </div>
              ))}
            </RadioGroup>
          </fieldset>
        </Section>

        <Section title={t('profile.sectionNotify')}>
          <div className='space-y-1'>
            <p className='text-sm font-medium'>{t('profile.email')}</p>
            <p className='text-sm'>{me.email ?? '\u2014'}</p>
            <p className='text-sm text-muted-foreground'>
              {me.email ? t('profile.emailHint') : t('profile.emailNone')}
            </p>
          </div>
          <div className='flex items-start justify-between gap-4'>
            <div className='space-y-1'>
              <Label htmlFor='notify-replies'>{t('profile.notifyReplies')}</Label>
              <p id='notify-replies-hint' className='text-sm text-muted-foreground'>
                {t('profile.notifyRepliesHint')}
              </p>
            </div>
            <Switch
              id='notify-replies'
              aria-describedby='notify-replies-hint'
              checked={draft.notify_replies}
              onCheckedChange={(checked) => set('notify_replies', checked)}
            />
          </div>
          <Field id='locale' label={t('profile.locale')} hint={t('profile.localeHint')}>
            <Select value={draft.locale} onValueChange={(value) => set('locale', value as Locale)}>
              <SelectTrigger id='locale' aria-describedby='locale-hint' className='w-56'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {LANGS.map((item) => (
                  <SelectItem key={item.value} value={item.value} lang={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </Section>

        <div className='sticky bottom-4 flex justify-end gap-2'>
          {dirty && (
            <Button type='button' variant='outline' onClick={() => setDraft(toDraft(me))}>
              {t('common.reset')}
            </Button>
          )}
          <Button type='submit' disabled={!dirty || save.isPending}>
            {save.isPending && <Spinner />}
            {t('common.save')}
          </Button>
        </div>
      </form>
    </>
  )
}
