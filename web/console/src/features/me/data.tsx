import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { Download, ExternalLink as ExternalIcon, TriangleAlert } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { OnAccountDelete, SiteSummary } from '@/api'
import { useAuth, useMe } from '@/context/auth'
import { useI18n } from '@/i18n'
import { saveDownload } from '@/lib/download'
import { IDENTITY_URL } from '@/lib/policy'
import { keys, useApiMutation } from '@/hooks/use-api'
import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { PageHeader, Spinner } from '@/components/common'
import { Section } from '@/components/common/form'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Skeleton } from '@/components/ui/skeleton'
import { useUpdateMe } from './profile'

function ChoiceGroup({
  name,
  value,
  onChange,
  disabled,
}: {
  name: string
  value: OnAccountDelete
  onChange: (value: OnAccountDelete) => void
  disabled?: boolean
}) {
  const { t } = useI18n()
  return (
    <RadioGroup
      value={value}
      onValueChange={(v) => onChange(v as OnAccountDelete)}
      disabled={disabled}
      className='gap-3'
    >
      {(['remove', 'anonymize'] as const).map((choice) => (
        <div key={choice} className='flex items-start gap-3'>
          <RadioGroupItem
            value={choice}
            id={`${name}-${choice}`}
            aria-describedby={`${name}-${choice}-hint`}
            className='mt-0.5'
          />
          <div className='space-y-1'>
            <Label htmlFor={`${name}-${choice}`}>{t(`data.choice.${choice}`)}</Label>
            <p id={`${name}-${choice}-hint`} className='text-sm text-muted-foreground'>
              {t(`data.choice.${choice}Hint`)}
            </p>
          </div>
        </div>
      ))}
    </RadioGroup>
  )
}

function SoleOwnerBlock({ sites }: { sites: SiteSummary[] }) {
  const { t } = useI18n()
  return (
    <Alert variant='destructive'>
      <TriangleAlert />
      <AlertTitle>{t('data.soleOwnerTitle')}</AlertTitle>
      <AlertDescription>
        <p>{t('data.soleOwnerDesc')}</p>
        <ul className='mt-2 list-disc ps-5'>
          {sites.map((s) => (
            <li key={s.id}>
              <Link to={`/console/sites/${s.id}/data`} className='underline underline-offset-4'>
                {s.name}
              </Link>
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  )
}

export default function DataPage() {
  const { t } = useI18n()
  const me = useMe()
  const { markDeleted } = useAuth()
  const sites = useQuery({ queryKey: keys.sites, queryFn: () => api.sites.list() })
  const owned = (sites.data?.data ?? []).filter((s) => s.role === 'owner' && s.status !== 'deleted')
  const [soleOwner, setSoleOwner] = useState(false)
  const [choice, setChoice] = useState<OnAccountDelete>(me.on_account_delete)
  const [confirming, setConfirming] = useState(false)
  const [exporting, setExporting] = useState(false)

  const saveDefault = useUpdateMe(() => toast.success(t('data.onDeleteSaved')))

  const remove = useApiMutation((comments: OnAccountDelete) => api.me.deleteData(comments), {
    onSuccess: () => {
      setConfirming(false)
      markDeleted()
    },
    onError: (error) => {
      if (api.isApiError(error, 'sole_owner')) {
        setConfirming(false)
        setSoleOwner(true)
        void sites.refetch()
        return true
      }
    },
  })

  const exportData = async () => {
    setExporting(true)
    try {
      saveDownload(await api.me.export())
      toast.success(t('data.exported'))
    } catch (error) {
      if (!api.isApiError(error, 'unauthorized')) toast.error(t('data.exportFailed'))
    } finally {
      setExporting(false)
    }
  }

  const blocked = owned.length > 0 || soleOwner
  const deleteWord = t('data.deleteWord')

  return (
    <>
      <PageHeader title={t('data.title')} description={t('data.desc')} />
      <div className='space-y-6'>
        <Section
          title={t('data.exportTitle')}
          description={t('data.exportDesc')}
          footer={
            <Button variant='outline' onClick={exportData} disabled={exporting}>
              {exporting ? <Spinner /> : <Download />}
              {t('data.exportButton')}
            </Button>
          }
        />

        <Section title={t('data.onDeleteTitle')} description={t('data.onDeleteDesc')}>
          <ChoiceGroup
            name='on-delete'
            value={me.on_account_delete}
            disabled={saveDefault.isPending}
            onChange={(value) => saveDefault.mutate({ on_account_delete: value })}
          />
        </Section>

        <Section tone='danger' title={t('data.deleteTitle')} description={t('data.deleteDesc')}
          footer={
            <Button
              variant='destructive'
              disabled={blocked || sites.isPending}
              onClick={() => setConfirming(true)}
            >
              {t('data.deleteButton')}
            </Button>
          }
        >
          {sites.isPending ? (
            <Skeleton className='h-16 w-full' />
          ) : blocked ? (
            <SoleOwnerBlock sites={owned} />
          ) : (
            <div className='space-y-3'>
              <p className='text-sm font-medium'>{t('data.deleteChoose')}</p>
              <ChoiceGroup name='delete-now' value={choice} onChange={setChoice} />
            </div>
          )}
        </Section>

        <Section
          title={t('data.idpTitle')}
          description={t('data.idpDesc')}
          footer={
            <Button variant='outline' asChild>
              <a href={IDENTITY_URL} target='_blank' rel='noopener noreferrer'>
                {t('data.idpButton')}
                <ExternalIcon />
              </a>
            </Button>
          }
        />
      </div>

      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('data.deleteConfirmTitle')}
        description={
          <>
            <p>{t('data.deleteConfirmDesc')}</p>
            <p className='font-medium text-foreground'>
              {t('data.deleteConfirmChoice', { choice: t(`data.choice.${choice}`) })}
            </p>
          </>
        }
        confirmWord={deleteWord}
        confirmText={t('data.deleteConfirmButton')}
        destructive
        isLoading={remove.isPending}
        onConfirm={() => remove.mutate(choice)}
      />
    </>
  )
}
