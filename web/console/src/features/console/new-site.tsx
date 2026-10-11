import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { Plus, X } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { GuestPolicy, SiteCreated } from '@/api'
import { useI18n } from '@/i18n'
import { FEATURES, POLICY, parseOrigin } from '@/lib/policy'
import { keys, useApiMutation } from '@/hooks/use-api'
import { PageHeader, Spinner } from '@/components/common'
import { Field, Section, describedBy } from '@/components/common/form'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'

export type VerifyState = { verification?: SiteCreated['verification'] }

type Errors = { name?: string; origin?: string; aliases: (string | undefined)[]; guests?: string }

export default function NewSitePage() {
  const { t } = useI18n()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [origin, setOrigin] = useState('')
  const [aliases, setAliases] = useState<string[]>([])
  const [guests, setGuests] = useState<GuestPolicy | null>(FEATURES.guests ? null : 'off')
  const [errors, setErrors] = useState<Errors>({ aliases: [] })

  const create = useApiMutation(api.sites.create, {
    invalidate: [keys.sites],
    onSuccess: async (site) => {
      queryClient.setQueryData(keys.site(site.id), site)
      toast.success(t('newSite.created'))
      // The site layout looks the new site up in the list, so refresh it first.
      await queryClient.invalidateQueries({ queryKey: keys.sites })
      navigate(`/console/sites/${site.id}/verify`, {
        state: { verification: site.verification } satisfies VerifyState,
      })
    },
    onError: (error) => {
      if (api.isApiError(error) && ['conflict', 'origin_not_allowed'].includes(error.code)) {
        setErrors((e) => ({ ...e, origin: error.message }))
        return true
      }
    },
  })

  const originError = (value: string) => {
    const parsed = parseOrigin(value)
    if ('error' in parsed) return parsed.error === 'https' ? t('validation.httpsOnly') : t('validation.originFormat')
    return undefined
  }

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const next: Errors = { aliases: [] }
    const trimmed = name.trim()
    if (!trimmed) next.name = t('validation.required')
    else if (trimmed.length > POLICY.siteNameMax) next.name = t('validation.tooLong', { max: POLICY.siteNameMax })
    next.origin = originError(origin)
    const seen = new Set<string>()
    const primary = parseOrigin(origin)
    if ('origin' in primary) seen.add(primary.origin)
    const cleanAliases: string[] = []
    aliases.forEach((value, i) => {
      if (!value.trim()) return
      const err = originError(value)
      if (err) {
        next.aliases[i] = err
        return
      }
      const parsed = parseOrigin(value) as { origin: string }
      if (seen.has(parsed.origin)) next.aliases[i] = t('validation.duplicate')
      seen.add(parsed.origin)
      cleanAliases.push(parsed.origin)
    })
    if (!guests) next.guests = t('validation.choose')
    setErrors(next)
    if (next.name || next.origin || next.aliases.some(Boolean) || next.guests) return
    create.mutate({
      name: trimmed,
      origin: (primary as { origin: string }).origin,
      aliases: cleanAliases,
      guest_policy: guests!,
    })
  }

  const originHint = t('newSite.originHint')

  return (
    <>
      <PageHeader title={t('newSite.title')} description={t('newSite.desc')} />
      <form onSubmit={submit} noValidate className='space-y-6'>
        <Section title={t('newSite.sectionSite')}>
          <Field id='site-name' label={t('newSite.name')} hint={t('newSite.nameHint')} error={errors.name}>
            <Input
              {...describedBy('site-name', errors.name, true)}
              value={name}
              maxLength={POLICY.siteNameMax}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field id='site-origin' label={t('newSite.origin')} hint={originHint} error={errors.origin}>
            <Input
              {...describedBy('site-origin', errors.origin, originHint)}
              type='url'
              inputMode='url'
              placeholder='https://blog.example.com'
              autoCapitalize='off'
              spellCheck={false}
              value={origin}
              onChange={(e) => setOrigin(e.target.value)}
            />
          </Field>
          <fieldset className='space-y-3'>
            <legend className='text-sm font-medium'>{t('newSite.aliases')}</legend>
            <p className='text-sm text-muted-foreground'>
              {t('newSite.aliasesHint', { max: POLICY.aliasOrigins })}
            </p>
            {aliases.map((value, i) => {
              const id = `alias-${i}`
              return (
                <div key={i} className='space-y-1'>
                  <div className='flex gap-2'>
                    <Input
                      {...describedBy(id, errors.aliases[i])}
                      aria-label={t('newSite.aliasLabel', { n: i + 1 })}
                      type='url'
                      inputMode='url'
                      placeholder='https://www.blog.example.com'
                      autoCapitalize='off'
                      spellCheck={false}
                      value={value}
                      onChange={(e) =>
                        setAliases((list) => list.map((v, j) => (j === i ? e.target.value : v)))
                      }
                    />
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon'
                      aria-label={t('newSite.removeAlias', { n: i + 1 })}
                      onClick={() => setAliases((list) => list.filter((_, j) => j !== i))}
                    >
                      <X />
                    </Button>
                  </div>
                  {errors.aliases[i] && (
                    <p id={`${id}-error`} role='alert' className='text-sm text-destructive'>
                      {errors.aliases[i]}
                    </p>
                  )}
                </div>
              )
            })}
            {aliases.length < POLICY.aliasOrigins && (
              <Button type='button' variant='outline' size='sm' onClick={() => setAliases((l) => [...l, ''])}>
                <Plus />
                {t('newSite.addAlias')}
              </Button>
            )}
          </fieldset>
        </Section>

        <Section title={t('settings.guestPolicy')} description={FEATURES.guests ? t('newSite.guestsChoose') : t('newSite.guestsC2')}>
          {FEATURES.guests && (
            <RadioGroup
              value={guests ?? ''}
              onValueChange={(v) => setGuests(v as GuestPolicy)}
              aria-invalid={errors.guests ? true : undefined}
            >
              {(['off', 'review', 'publish'] as const).map((value) => (
                <div key={value} className='flex items-center gap-3'>
                  <RadioGroupItem value={value} id={`guest-${value}`} />
                  <Label htmlFor={`guest-${value}`}>{t(`settings.guest.${value}`)}</Label>
                </div>
              ))}
            </RadioGroup>
          )}
          {errors.guests && <p role='alert' className='text-sm text-destructive'>{errors.guests}</p>}
        </Section>

        <div className='flex justify-end'>
          <Button type='submit' disabled={create.isPending}>
            {create.isPending && <Spinner />}
            {t('newSite.submit')}
          </Button>
        </div>
      </form>
    </>
  )
}
