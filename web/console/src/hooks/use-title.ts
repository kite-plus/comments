import { useEffect } from 'react'
import { useMatches } from 'react-router'
import { useI18n, type MessageKey } from '@/i18n'

export type RouteHandle = { title?: MessageKey }

/** The title of the deepest route that names one. */
export function useRouteTitle(): MessageKey | undefined {
  const matches = useMatches()
  for (let i = matches.length - 1; i >= 0; i--) {
    const handle = matches[i].handle as RouteHandle | undefined
    if (handle?.title) return handle.title
  }
  return undefined
}

export function useDocumentTitle(title: string | undefined) {
  const { t } = useI18n()
  useEffect(() => {
    const previous = document.title
    document.title = title ? `${title} \u00b7 ${t('app.name')}` : t('app.name')
    return () => {
      document.title = previous
    }
  }, [title, t])
}
