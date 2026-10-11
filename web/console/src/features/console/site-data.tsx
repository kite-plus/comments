import { useState } from 'react'
import { Download, RotateCcw } from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { ExportFormat } from '@/api'
import { useI18n } from '@/i18n'
import { saveDownload } from '@/lib/download'
import { POLICY, purgeDate } from '@/lib/policy'
import { errorMessage, keys, useApiMutation } from '@/hooks/use-api'
import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { PageHeader, Spinner } from '@/components/common'
import { Section } from '@/components/common/form'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { NotFound } from '@/features/errors'
import { useSite } from './site-context'

export default function SiteDataPage() {
  const { t, formatDate } = useI18n()
  const site = useSite()
  const [format, setFormat] = useState<ExportFormat>('json')
  const [exporting, setExporting] = useState(false)
  const [confirming, setConfirming] = useState(false)

  const remove = useApiMutation(() => api.sites.remove(site.id), {
    invalidate: [keys.sites, keys.site(site.id)],
    onSuccess: () => {
      setConfirming(false)
      toast.success(t('siteData.deleted', { days: POLICY.siteRestoreDays }))
    },
  })
  const restore = useApiMutation(() => api.sites.restore(site.id), {
    invalidate: [keys.sites, keys.site(site.id)],
    onSuccess: () => toast.success(t('overview.restored')),
  })

  if (site.role !== 'owner') return <NotFound />

  const exportSite = async () => {
    setExporting(true)
    try {
      saveDownload(await api.sites.export(site.id, format))
      toast.success(t('siteData.exportStarted'))
    } catch (error) {
      if (!api.isApiError(error, 'unauthorized')) toast.error(errorMessage(t, error))
    } finally {
      setExporting(false)
    }
  }

  return (
    <>
      <PageHeader title={t('siteData.title')} description={site.name} />
      <div className='space-y-6'>
        <Section
          title={t('siteData.exportTitle')}
          description={t('siteData.exportDesc')}
          footer={
            <Button onClick={exportSite} disabled={exporting}>
              {exporting ? <Spinner /> : <Download />}
              {t('siteData.export')}
            </Button>
          }
        >
          <RadioGroup value={format} onValueChange={(v) => setFormat(v as ExportFormat)} className='gap-3'>
            {(['json', 'artrans'] as const).map((value) => (
              <div key={value} className='flex items-start gap-3'>
                <RadioGroupItem value={value} id={`format-${value}`} className='mt-0.5' aria-describedby={`format-${value}-hint`} />
                <div className='space-y-1'>
                  <Label htmlFor={`format-${value}`}>{t(`siteData.format.${value}`)}</Label>
                  <p id={`format-${value}-hint`} className='text-sm text-muted-foreground'>
                    {t(`siteData.format.${value}Hint`)}
                  </p>
                </div>
              </div>
            ))}
          </RadioGroup>
        </Section>

        {site.status === 'deleted' ? (
          <Section
            tone='danger'
            title={t('overview.deletedTitle')}
            description={t('overview.deletedDesc', {
              date: site.deleted_at ? formatDate(purgeDate(site.deleted_at, site.purge_at)) : '\u2014',
            })}
            footer={
              <Button variant='outline' onClick={() => restore.mutate(undefined)} disabled={restore.isPending}>
                {restore.isPending ? <Spinner /> : <RotateCcw />}
                {t('overview.restore')}
              </Button>
            }
          />
        ) : (
          <Section
            tone='danger'
            title={t('siteData.deleteTitle')}
            description={t('siteData.deleteDesc', { days: POLICY.siteRestoreDays })}
            footer={
              <Button variant='destructive' onClick={() => setConfirming(true)}>
                {t('siteData.deleteButton')}
              </Button>
            }
          />
        )}
      </div>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('siteData.deleteConfirmTitle', { name: site.name })}
        description={
          <>
            <p>{t('siteData.deleteDesc', { days: POLICY.siteRestoreDays })}</p>
            <p>{t('siteData.deleteExportFirst')}</p>
          </>
        }
        confirmWord={site.name}
        confirmText={t('siteData.deleteConfirmButton')}
        destructive
        isLoading={remove.isPending}
        onConfirm={() => remove.mutate(undefined)}
      />
    </>
  )
}
