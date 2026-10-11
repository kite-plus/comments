import { Link, isRouteErrorResponse, useRouteError } from 'react-router'
import { FileQuestion, TriangleAlert } from 'lucide-react'
import { useI18n } from '@/i18n'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-title'

export function NotFound() {
  const { t } = useI18n()
  useDocumentTitle(t('error.notFoundTitle'))
  return (
    <div className='flex flex-col items-center gap-4 py-20 text-center'>
      <FileQuestion className='size-10 text-muted-foreground' aria-hidden='true' />
      <h1 className='text-2xl font-bold tracking-tight'>{t('error.notFoundTitle')}</h1>
      <p className='max-w-md text-muted-foreground'>{t('error.notFoundDesc')}</p>
      <Button asChild variant='outline'>
        <Link to='/me'>{t('error.goHome')}</Link>
      </Button>
    </div>
  )
}

/** Rendered by the router when a page throws while rendering or loading. */
export function RouteError() {
  const error = useRouteError()
  const { t } = useI18n()
  if (isRouteErrorResponse(error) && error.status === 404) return <NotFound />
  console.error(error)
  return (
    <div role='alert' className='flex flex-col items-center gap-4 py-20 text-center'>
      <TriangleAlert className='size-10 text-muted-foreground' aria-hidden='true' />
      <h1 className='text-2xl font-bold tracking-tight'>{t('error.pageTitle')}</h1>
      <p className='max-w-md text-muted-foreground'>{t('error.pageDesc')}</p>
      <Button variant='outline' onClick={() => window.location.reload()}>
        {t('error.reload')}
      </Button>
    </div>
  )
}
