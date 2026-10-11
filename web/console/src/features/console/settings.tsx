import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router'
import { Plus, ShieldCheck, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { GuestPolicy, MemberPolicy, OwnerNotify, Site, SitePatch, SortOrder } from '@/api'
import { useI18n } from '@/i18n'
import { FEATURES, POLICY, parseOrigin } from '@/lib/policy'
import { keys, useApiMutation } from '@/hooks/use-api'
import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { ListSkeleton, PageHeader, QueryError, Spinner } from '@/components/common'
import { Field, Section, describedBy } from '@/components/common/form'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { NotFound } from '@/features/errors'
import { useSite, useSiteDetail } from './site-context'

type Draft = {
  name: string
  guest_policy: GuestPolicy
  member_policy: MemberPolicy
  sort_order: SortOrder
  auto_close: boolean
  close_after_days: string
  blocked_words: string
  query_keys: string
  allow_localhost: boolean
  owner_notify: OwnerNotify
}

function toDraft(site: Site): Draft {
  return {
    name: site.name,
    guest_policy: site.guest_policy,
    member_policy: site.member_policy,
    sort_order: site.sort_order,
    auto_close: site.close_after_days !== null,
    close_after_days: String(site.close_after_days ?? 365),
    blocked_words: site.blocked_words.join('\n'),
    query_keys: site.query_keys.join(', '),
    allow_localhost: site.allow_localhost,
    owner_notify: site.owner_notify,
  }
}

const splitLines = (s: string) => [...new Set(s.split('\n').map((w) => w.trim()).filter(Boolean))]
const splitKeys = (s: string) => [...new Set(s.split(/[\s,\uff0c]+/).map((w) => w.trim()).filter(Boolean))]
const same = (a: string[], b: string[]) => a.length === b.length && a.every((v, i) => v === b[i])

function diff(site: Site, d: Draft): SitePatch {
  const patch: SitePatch = {}
  if (d.name.trim() !== site.name) patch.name = d.name.trim()
  if (d.guest_policy !== site.guest_policy) patch.guest_policy = d.guest_policy
  if (d.member_policy !== site.member_policy) patch.member_policy = d.member_policy
  if (d.sort_order !== site.sort_order) patch.sort_order = d.sort_order
  const days = d.auto_close ? Number(d.close_after_days) : null
  if (days !== site.close_after_days) patch.close_after_days = days
  const words = splitLines(d.blocked_words)
  if (!same(words, site.blocked_words)) patch.blocked_words = words
  const qk = splitKeys(d.query_keys)
  if (!same(qk, site.query_keys)) patch.query_keys = qk
  if (d.allow_localhost !== site.allow_localhost) patch.allow_localhost = d.allow_localhost
  if (d.owner_notify !== site.owner_notify) patch.owner_notify = d.owner_notify
  return patch
}

function Choice<T extends string>({
  name,
  value,
  options,
  onChange,
  disabled,
  label,
}: {
  name: string
  value: T
  options: { value: T; label: string }[]
  onChange: (value: T) => void
  disabled?: boolean
  label: string
}) {
  return (
    <RadioGroup
      value={value}
      onValueChange={(v) => onChange(v as T)}
      disabled={disabled}
      aria-label={label}
      className='flex flex-wrap gap-x-6 gap-y-2'
    >
      {options.map((o) => (
        <div key={o.value} className='flex items-center gap-2'>
          <RadioGroupItem value={o.value} id={`${name}-${o.value}`} />
          <Label htmlFor={`${name}-${o.value}`} className='font-normal'>
            {o.label}
          </Label>
        </div>
      ))}
    </RadioGroup>
  )
}

function SettingsForm({ site }: { site: Site }) {
  const { t } = useI18n()
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState(() => toDraft(site))
  const [errors, setErrors] = useState<Partial<Record<keyof Draft, string>>>({})
  const patch = diff(site, draft)
  const dirty = Object.keys(patch).length > 0
  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((d) => ({ ...d, [key]: value }))

  const save = useApiMutation((p: SitePatch) => api.sites.update(site.id, p), {
    invalidate: [keys.sites],
    onSuccess: (updated) => {
      queryClient.setQueryData(keys.site(site.id), updated)
      setDraft(toDraft(updated))
      toast.success(t('settings.saved'))
    },
  })

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const next: typeof errors = {}
    if (!draft.name.trim()) next.name = t('validation.required')
    const days = Number(draft.close_after_days)
    if (draft.auto_close && (!Number.isInteger(days) || days < 1 || days > POLICY.closeAfterDaysMax))
      next.close_after_days = t('settings.autoCloseInvalid', { max: POLICY.closeAfterDaysMax })
    if (splitLines(draft.blocked_words).length > POLICY.blockedWords)
      next.blocked_words = t('settings.tooMany', { max: POLICY.blockedWords })
    if (splitKeys(draft.query_keys).length > POLICY.queryKeys)
      next.query_keys = t('settings.tooMany', { max: POLICY.queryKeys })
    setErrors(next)
    if (Object.keys(next).length) return
    save.mutate(patch)
  }

  const policyOptions = {
    guest: (['off', 'review', 'publish'] as const).map((v) => ({ value: v, label: t(`settings.guest.${v}`) })),
    member: (['publish', 'review'] as const).map((v) => ({ value: v, label: t(`settings.member.${v}`) })),
    sort: (['oldest', 'newest'] as const).map((v) => ({ value: v, label: t(`settings.sort.${v}`) })),
    notify: (['instant', 'daily', 'off'] as const).map((v) => ({ value: v, label: t(`settings.notify.${v}`) })),
  }

  return (
    <form onSubmit={submit} noValidate className='space-y-6'>
      <Section title={t('settings.general')}>
        <Field id='settings-name' label={t('newSite.name')} error={errors.name}>
          <Input
            {...describedBy('settings-name', errors.name)}
            value={draft.name}
            maxLength={POLICY.siteNameMax}
            onChange={(e) => set('name', e.target.value)}
          />
        </Field>
      </Section>

      <Section title={t('settings.commenting')}>
        <div className='space-y-2'>
          <p className='text-sm font-medium'>{t('settings.guestPolicy')}</p>
          <Choice
            name='guest'
            label={t('settings.guestPolicy')}
            value={draft.guest_policy}
            options={policyOptions.guest}
            onChange={(v) => set('guest_policy', v)}
            disabled={!FEATURES.guests}
          />
          {!FEATURES.guests && <p className='text-sm text-muted-foreground'>{t('settings.guestC2')}</p>}
        </div>
        <div className='space-y-2'>
          <p className='text-sm font-medium'>{t('settings.memberPolicy')}</p>
          <Choice
            name='member'
            label={t('settings.memberPolicy')}
            value={draft.member_policy}
            options={policyOptions.member}
            onChange={(v) => set('member_policy', v)}
          />
        </div>
        <div className='space-y-2'>
          <p className='text-sm font-medium'>{t('settings.sortOrder')}</p>
          <Choice
            name='sort'
            label={t('settings.sortOrder')}
            value={draft.sort_order}
            options={policyOptions.sort}
            onChange={(v) => set('sort_order', v)}
          />
        </div>
        <div className='space-y-3'>
          <div className='flex items-start justify-between gap-4'>
            <div className='space-y-1'>
              <Label htmlFor='auto-close'>{t('settings.autoClose')}</Label>
              <p id='auto-close-hint' className='text-sm text-muted-foreground'>
                {t('settings.autoCloseHint')}
              </p>
            </div>
            <Switch
              id='auto-close'
              aria-describedby='auto-close-hint'
              checked={draft.auto_close}
              onCheckedChange={(checked) => set('auto_close', checked)}
            />
          </div>
          {draft.auto_close && (
            <Field id='close-days' label={t('settings.autoCloseDays')} error={errors.close_after_days}>
              <div className='flex items-center gap-2'>
                <Input
                  {...describedBy('close-days', errors.close_after_days)}
                  type='number'
                  inputMode='numeric'
                  min={1}
                  max={POLICY.closeAfterDaysMax}
                  className='w-32'
                  value={draft.close_after_days}
                  onChange={(e) => set('close_after_days', e.target.value)}
                />
                <span className='text-sm text-muted-foreground'>{t('settings.days')}</span>
              </div>
            </Field>
          )}
        </div>
        <Field
          id='blocked-words'
          label={t('settings.blockedWords')}
          hint={t('settings.blockedWordsHint', { max: POLICY.blockedWords })}
          error={errors.blocked_words}
        >
          <Textarea
            {...describedBy('blocked-words', errors.blocked_words, true)}
            rows={4}
            value={draft.blocked_words}
            onChange={(e) => set('blocked_words', e.target.value)}
          />
        </Field>
        <Field
          id='query-keys'
          label={t('settings.queryKeys')}
          hint={t('settings.queryKeysHint', { max: POLICY.queryKeys })}
          error={errors.query_keys}
        >
          <Input
            {...describedBy('query-keys', errors.query_keys, true)}
            className='font-mono'
            placeholder='p'
            autoCapitalize='off'
            spellCheck={false}
            value={draft.query_keys}
            onChange={(e) => set('query_keys', e.target.value)}
          />
        </Field>
        <div className='flex items-start justify-between gap-4'>
          <div className='space-y-1'>
            <Label htmlFor='allow-localhost'>{t('settings.allowLocalhost')}</Label>
            <p id='allow-localhost-hint' className='text-sm text-muted-foreground'>
              {t('settings.allowLocalhostHint')}
            </p>
          </div>
          <Switch
            id='allow-localhost'
            aria-describedby='allow-localhost-hint'
            checked={draft.allow_localhost}
            onCheckedChange={(checked) => set('allow_localhost', checked)}
          />
        </div>
      </Section>

      <Section title={t('settings.ownerNotify')} description={t('settings.ownerNotifyHint')}>
        <Choice
          name='notify'
          label={t('settings.ownerNotify')}
          value={draft.owner_notify}
          options={policyOptions.notify}
          onChange={(v) => set('owner_notify', v)}
        />
      </Section>

      <div className='sticky bottom-4 z-10 flex items-center justify-end gap-2'>
        {dirty && (
          <>
            <span className='me-auto rounded-md bg-background/90 px-2 py-1 text-sm text-muted-foreground' role='status'>
              {t('settings.unsaved')}
            </span>
            <Button type='button' variant='outline' onClick={() => setDraft(toDraft(site))}>
              {t('common.reset')}
            </Button>
          </>
        )}
        <Button type='submit' disabled={!dirty || save.isPending}>
          {save.isPending && <Spinner />}
          {t('common.save')}
        </Button>
      </div>
    </form>
  )
}

function Origins({ site }: { site: Site }) {
  const { t } = useI18n()
  const queryClient = useQueryClient()
  const [value, setValue] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [removing, setRemoving] = useState<string | null>(null)
  const aliases = site.origins.filter((o) => !o.is_primary).length
  const refresh = [keys.site(site.id), keys.sites]

  const add = useApiMutation((origin: string) => api.sites.addOrigin(site.id, origin), {
    invalidate: refresh,
    onSuccess: (origin) => {
      queryClient.setQueryData<Site>(keys.site(site.id), (old) =>
        old ? { ...old, origins: [...old.origins, origin] } : old
      )
      setValue('')
      toast.success(t('settings.originAdded', { origin: origin.origin }))
    },
    onError: (err) => {
      if (api.isApiError(err) && err.status < 500 && err.status !== 401) {
        setError(err.message)
        return true
      }
    },
  })
  const remove = useApiMutation((origin: string) => api.sites.removeOrigin(site.id, origin), {
    invalidate: refresh,
    onSuccess: (_, origin) => {
      setRemoving(null)
      toast.success(t('settings.originRemoved', { origin }))
    },
  })

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const parsed = parseOrigin(value)
    if ('error' in parsed) {
      setError(parsed.error === 'https' ? t('validation.httpsOnly') : t('validation.originFormat'))
      return
    }
    if (site.origins.some((o) => o.origin === parsed.origin)) {
      setError(t('validation.duplicate'))
      return
    }
    setError(null)
    add.mutate(parsed.origin)
  }

  return (
    <Section
      title={<span id='origins'>{t('settings.origins')}</span>}
      description={t('settings.originsDesc')}
      footer={
        site.origins.some((o) => !o.verified_at) && (
          <Button asChild variant='outline' size='sm'>
            <Link to={`/console/sites/${site.id}/verify`}>
              <ShieldCheck />
              {t('overview.verifyNow')}
            </Link>
          </Button>
        )
      }
    >
      <ul className='divide-y rounded-lg border'>
        {site.origins.map((o) => (
          <li key={o.origin} className='flex flex-wrap items-center gap-2 px-4 py-2'>
            <span className='min-w-0 flex-1 truncate font-mono text-xs'>{o.origin}</span>
            {o.is_primary && <Badge variant='secondary'>{t('settings.primary')}</Badge>}
            <Badge
              variant='outline'
              className={o.verified_at ? 'border-success/40 text-success' : 'text-muted-foreground'}
            >
              {o.verified_at ? t('origin.verified') : t('origin.unverified')}
            </Badge>
            {!o.is_primary && (
              <Button
                size='icon'
                variant='ghost'
                aria-label={t('settings.removeOrigin', { origin: o.origin })}
                onClick={() => setRemoving(o.origin)}
              >
                <Trash2 />
              </Button>
            )}
          </li>
        ))}
      </ul>
      {aliases < POLICY.aliasOrigins ? (
        <form onSubmit={submit} noValidate className='space-y-2'>
          <Label htmlFor='new-origin'>{t('settings.addOrigin')}</Label>
          <div className='flex gap-2'>
            <Input
              {...describedBy('new-origin', error)}
              type='url'
              inputMode='url'
              placeholder='https://www.example.com'
              autoCapitalize='off'
              spellCheck={false}
              value={value}
              onChange={(e) => setValue(e.target.value)}
            />
            <Button type='submit' variant='outline' disabled={add.isPending}>
              {add.isPending ? <Spinner /> : <Plus />}
              {t('common.add')}
            </Button>
          </div>
          {error && (
            <p id='new-origin-error' role='alert' className='text-sm text-destructive'>
              {error}
            </p>
          )}
        </form>
      ) : (
        <p className='text-sm text-muted-foreground'>{t('settings.aliasLimit', { max: POLICY.aliasOrigins })}</p>
      )}
      <ConfirmDialog
        open={!!removing}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={t('settings.removeOriginTitle', { origin: removing ?? '' })}
        description={t('settings.removeOriginDesc')}
        confirmText={t('common.remove')}
        destructive
        isLoading={remove.isPending}
        onConfirm={() => removing && remove.mutate(removing)}
      />
    </Section>
  )
}

export default function SettingsPage() {
  const { t } = useI18n()
  const summary = useSite()
  const detail = useSiteDetail(summary.id, summary.role === 'owner')
  if (summary.role !== 'owner' || summary.status === 'deleted') return <NotFound />
  return (
    <>
      <PageHeader title={t('settings.title')} description={t('settings.desc')} />
      {detail.isPending ? (
        <ListSkeleton rows={3} />
      ) : detail.isError ? (
        <QueryError error={detail.error} onRetry={() => void detail.refetch()} />
      ) : (
        <div className='space-y-6'>
          <SettingsForm key={detail.data.updated_at} site={detail.data} />
          <Origins site={detail.data} />
        </div>
      )}
    </>
  )
}
