import type { ComponentType } from 'react'
import { Navigate, type RouteObject } from 'react-router'
import { Root } from '@/root'
import { CheckingScreen } from '@/features/auth/screens'
import { NotFound, RouteError } from '@/features/errors'
import type { RouteHandle } from '@/hooks/use-title'

type PageModule = { default: ComponentType }

// Every page is its own chunk, loaded when the route is first visited.
function page(
  title: RouteHandle['title'],
  load: () => Promise<PageModule>
): { handle: RouteHandle; lazy: () => Promise<{ Component: ComponentType }> } {
  return {
    handle: { title } satisfies RouteHandle,
    lazy: async () => ({ Component: (await load()).default }),
  }
}

export const routes: RouteObject[] = [
  {
    path: '/',
    element: <Root />,
    errorElement: <RouteError />,
    hydrateFallbackElement: <CheckingScreen />,
    children: [
      { index: true, element: <Navigate to='/me' replace /> },
      {
        errorElement: <RouteError />,
        children: [
          {
            path: 'me',
            children: [
              { index: true, ...page('nav.myComments', () => import('@/features/me/comments')) },
              { path: 'notifications', ...page('nav.notifications', () => import('@/features/me/notifications')) },
              { path: 'profile', ...page('nav.profile', () => import('@/features/me/profile')) },
              { path: 'sessions', ...page('nav.sessions', () => import('@/features/me/sessions')) },
              { path: 'data', ...page('nav.data', () => import('@/features/me/data')) },
              { path: 'claims', ...page('nav.claims', () => import('@/features/me/claims')) },
              { path: 'claims/:claimId', ...page('nav.claims', () => import('@/features/me/claim-verify')) },
            ],
          },
          {
            path: 'console',
            children: [
              { index: true, ...page('nav.sites', () => import('@/features/console/sites')) },
              { path: 'new', ...page('nav.newSite', () => import('@/features/console/new-site')) },
              { path: 'admin', ...page('nav.adminSites', () => import('@/features/admin/sites')) },
              {
                path: 'sites/:siteId',
                lazy: async () => ({
                  Component: (await import('@/features/console/site-layout')).default,
                }),
                children: [
                  { index: true, ...page('nav.overview', () => import('@/features/console/overview')) },
                  { path: 'verify', ...page('nav.verify', () => import('@/features/console/verify')) },
                  { path: 'comments', ...page('nav.queue', () => import('@/features/console/queue')) },
                  { path: 'reports', ...page('nav.reports', () => import('@/features/console/reports')) },
                  { path: 'threads', ...page('nav.threads', () => import('@/features/console/threads')) },
                  { path: 'bans', ...page('nav.bans', () => import('@/features/console/bans')) },
                  { path: 'events', ...page('nav.events', () => import('@/features/console/events')) },
                  { path: 'settings', ...page('nav.settings', () => import('@/features/console/settings')) },
                  { path: 'data', ...page('nav.siteData', () => import('@/features/console/site-data')) },
                ],
              },
            ],
          },
          { path: '*', element: <NotFound />, handle: { title: 'error.notFoundTitle' } satisfies RouteHandle },
        ],
      },
    ],
  },
]
