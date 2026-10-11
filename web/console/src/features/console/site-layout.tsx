import { useQuery } from '@tanstack/react-query'
import { Outlet, useParams } from 'react-router'
import * as api from '@/api'
import { keys } from '@/hooks/use-api'
import { ListSkeleton, QueryError } from '@/components/common'
import { NotFound } from '@/features/errors'
import { SiteContext } from './site-context'

export default function SiteLayout() {
  const { siteId = '' } = useParams()
  const sites = useQuery({ queryKey: keys.sites, queryFn: () => api.sites.list() })
  if (sites.isPending) return <ListSkeleton rows={2} />
  if (sites.isError) return <QueryError error={sites.error} onRetry={() => void sites.refetch()} />
  const site = sites.data.data.find((s) => s.id === siteId)
  if (!site && sites.isFetching) return <ListSkeleton rows={2} />
  if (!site) return <NotFound />
  return (
    <SiteContext value={site}>
      <Outlet />
    </SiteContext>
  )
}
