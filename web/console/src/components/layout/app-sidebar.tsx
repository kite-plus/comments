import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useLocation, useMatch } from 'react-router'
import {
  Ban,
  Bell,
  ChevronsUpDown,
  Database,
  Download,
  FileText,
  Flag,
  Gauge,
  History,
  Inbox,
  LayoutGrid,
  LogOut,
  MessageSquareText,
  MonitorSmartphone,
  Plus,
  Settings,
  ShieldCheck,
  UserRound,
} from 'lucide-react'
import { toast } from 'sonner'
import * as api from '@/api'
import type { SiteSummary } from '@/api'
import { useAuth, useMe } from '@/context/auth'
import { useI18n, type MessageKey } from '@/i18n'
import { errorMessage, keys } from '@/hooks/use-api'
import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { SiteStatusBadge, UserAvatar } from '@/components/common'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from '@/components/ui/sidebar'

type NavItem = {
  title: MessageKey
  url: string
  icon: React.ElementType
  badge?: number
  /** Active only on this exact path, not its children. */
  end?: boolean
}

function isActive(pathname: string, item: NavItem) {
  if (pathname === item.url) return true
  return !item.end && pathname.startsWith(`${item.url}/`)
}

function NavGroup({ label, items }: { label: React.ReactNode; items: NavItem[] }) {
  const { t } = useI18n()
  const { pathname } = useLocation()
  const { setOpenMobile } = useSidebar()
  return (
    <SidebarGroup>
      <SidebarGroupLabel>{label}</SidebarGroupLabel>
      <SidebarMenu>
        {items.map((item) => {
          const active = isActive(pathname, item)
          return (
            <SidebarMenuItem key={item.url}>
              <SidebarMenuButton asChild isActive={active} tooltip={t(item.title)}>
                <Link
                  to={item.url}
                  aria-current={active ? 'page' : undefined}
                  onClick={() => setOpenMobile(false)}
                >
                  <item.icon />
                  <span>{t(item.title)}</span>
                </Link>
              </SidebarMenuButton>
              {!!item.badge && (
                <SidebarMenuBadge
                  className='rounded-full bg-destructive/90 px-1.5 text-white peer-data-[active=true]/menu-button:text-white'
                  aria-label={t('nav.badge', { count: item.badge })}
                >
                  {item.badge > 99 ? '99+' : item.badge}
                </SidebarMenuBadge>
              )}
            </SidebarMenuItem>
          )
        })}
      </SidebarMenu>
    </SidebarGroup>
  )
}

function Brand() {
  const { t } = useI18n()
  const { setOpenMobile } = useSidebar()
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <SidebarMenuButton size='lg' asChild>
          <Link to='/me' onClick={() => setOpenMobile(false)}>
            <div className='flex aspect-square size-8 items-center justify-center rounded-lg border bg-background'>
              <img src={`${import.meta.env.BASE_URL}favicon.svg`} alt='' width={20} height={17} />
            </div>
            <div className='grid flex-1 text-start text-sm leading-tight'>
              <span className='truncate font-semibold'>Kite Plus</span>
              <span className='truncate text-xs'>{t('app.product')}</span>
            </div>
          </Link>
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}

function NavUser() {
  const { t } = useI18n()
  const me = useMe()
  const { signOut } = useAuth()
  const { isMobile, setOpenMobile } = useSidebar()
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)

  const doSignOut = () => {
    setBusy(true)
    signOut().catch((error: unknown) => {
      setBusy(false)
      toast.error(errorMessage(t, error))
    })
  }

  return (
    <>
      <SidebarMenu>
        <SidebarMenuItem>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <SidebarMenuButton
                size='lg'
                className='data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground'
              >
                <UserAvatar name={me.display_name} seed={me.id} src={me.avatar_url} />
                <div className='grid flex-1 text-start text-sm leading-tight'>
                  <span className='truncate font-medium'>{me.display_name}</span>
                  {me.email && (
                    <span className='truncate text-xs text-muted-foreground'>{me.email}</span>
                  )}
                </div>
                <ChevronsUpDown className='ms-auto size-4 text-muted-foreground' />
              </SidebarMenuButton>
            </DropdownMenuTrigger>
            <DropdownMenuContent
              className='w-(--radix-dropdown-menu-trigger-width) min-w-56 rounded-lg'
              side={isMobile ? 'bottom' : 'right'}
              align='end'
              sideOffset={4}
            >
              <DropdownMenuLabel className='font-normal'>
                <div className='grid gap-0.5'>
                  <span className='truncate text-sm font-semibold'>{me.display_name}</span>
                  {me.is_platform_admin && (
                    <span className='text-xs text-muted-foreground'>{t('nav.platformAdmin')}</span>
                  )}
                </div>
              </DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                <DropdownMenuItem asChild>
                  <Link to='/me/profile' onClick={() => setOpenMobile(false)}>
                    <UserRound />
                    {t('nav.profile')}
                  </Link>
                </DropdownMenuItem>
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
              <DropdownMenuItem variant='destructive' onSelect={() => setConfirming(true)}>
                <LogOut />
                {t('auth.signOut')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </SidebarMenuItem>
      </SidebarMenu>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('auth.signOutTitle')}
        description={t('auth.signOutDesc')}
        confirmText={t('auth.signOut')}
        isLoading={busy}
        onConfirm={doSignOut}
      />
    </>
  )
}

function siteNav(site: SiteSummary): NavItem[] {
  const base = `/console/sites/${site.id}`
  if (site.status === 'deleted') {
    return [
      { title: 'nav.overview', url: base, icon: Gauge, end: true },
      { title: 'nav.siteData', url: `${base}/data`, icon: Download },
    ]
  }
  const items: NavItem[] = [
    { title: 'nav.overview', url: base, icon: Gauge, end: true },
    { title: 'nav.queue', url: `${base}/comments`, icon: Inbox, badge: site.pending_count },
    { title: 'nav.reports', url: `${base}/reports`, icon: Flag, badge: site.open_reports },
    { title: 'nav.threads', url: `${base}/threads`, icon: FileText },
    { title: 'nav.bans', url: `${base}/bans`, icon: Ban },
    { title: 'nav.events', url: `${base}/events`, icon: History },
  ]
  if (site.role === 'owner') {
    items.push(
      { title: 'nav.settings', url: `${base}/settings`, icon: Settings },
      { title: 'nav.siteData', url: `${base}/data`, icon: Download }
    )
  }
  return items
}

export function AppSidebar() {
  const { t } = useI18n()
  const me = useMe()
  const siteMatch = useMatch('/console/sites/:siteId/*')
  const siteId = siteMatch?.params.siteId

  const sites = useQuery({ queryKey: keys.sites, queryFn: () => api.sites.list() })
  const badge = useQuery({
    queryKey: [...keys.notifications, 'badge'],
    queryFn: () => api.me.notifications(null),
    refetchInterval: 60_000,
  })
  const unread =
    badge.data?.unread_count ?? badge.data?.data.filter((n) => !n.read_at).length ?? 0
  const siteList = sites.data?.data ?? []
  const pendingTotal = siteList.reduce((sum, s) => sum + (s.pending_count ?? 0), 0)
  const current = siteList.find((s) => s.id === siteId)

  const reader: NavItem[] = [
    { title: 'nav.myComments', url: '/me', icon: MessageSquareText, end: true },
    { title: 'nav.notifications', url: '/me/notifications', icon: Bell, badge: unread },
    { title: 'nav.profile', url: '/me/profile', icon: UserRound },
    { title: 'nav.sessions', url: '/me/sessions', icon: MonitorSmartphone },
    { title: 'nav.data', url: '/me/data', icon: Database },
  ]
  const consoleItems: NavItem[] = [
    { title: 'nav.sites', url: '/console', icon: LayoutGrid, badge: pendingTotal, end: true },
    { title: 'nav.newSite', url: '/console/new', icon: Plus },
  ]

  return (
    <Sidebar collapsible='icon' variant='inset'>
      <SidebarHeader>
        <Brand />
      </SidebarHeader>
      <SidebarContent>
        <NavGroup label={t('nav.reader')} items={reader} />
        <NavGroup label={t('nav.console')} items={consoleItems} />
        {current && (
          <NavGroup
            label={
              <span className='flex min-w-0 items-center gap-2'>
                <span className='truncate'>{current.name}</span>
                {current.status !== 'active' && <SiteStatusBadge status={current.status} />}
              </span>
            }
            items={siteNav(current)}
          />
        )}
        {me.is_platform_admin && (
          <NavGroup
            label={t('nav.platform')}
            items={[{ title: 'nav.adminSites', url: '/console/admin', icon: ShieldCheck }]}
          />
        )}
      </SidebarContent>
      <SidebarFooter>
        <NavUser />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
