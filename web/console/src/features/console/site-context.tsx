import { createContext, useContext } from 'react'
import { useQuery } from '@tanstack/react-query'
import * as api from '@/api'
import type { SiteSummary } from '@/api'
import { keys } from '@/hooks/use-api'

export const SiteContext = createContext<SiteSummary | null>(null)

/** The site the current console page is about, from the sites list. */
export function useSite(): SiteSummary {
  const site = useContext(SiteContext)
  if (!site) throw new Error('useSite must be used inside a site route')
  return site
}

/** Full settings; only owners may read them (api.md section 7). */
export function useSiteDetail(id: string, enabled = true) {
  return useQuery({ queryKey: keys.site(id), queryFn: () => api.sites.get(id), enabled })
}
