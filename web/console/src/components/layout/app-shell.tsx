import { useEffect, useRef } from 'react'
import { Outlet, useLocation, useNavigation } from 'react-router'
import { useI18n } from '@/i18n'
import { useDocumentTitle, useRouteTitle } from '@/hooks/use-title'
import { cn } from '@/lib/utils'
import { Separator } from '@/components/ui/separator'
import { readSidebarOpen, SidebarInset, SidebarProvider, SidebarTrigger } from '@/components/ui/sidebar'
import { AppSidebar } from './app-sidebar'
import { LanguageSwitch, ThemeSwitch } from './preferences'

function SkipToMain() {
  const { t } = useI18n()
  return (
    <a
      className='fixed inset-s-4 top-0 z-[999] -translate-y-full rounded-b-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground shadow-sm transition focus:translate-y-0'
      href='#main'
    >
      {t('common.skipToMain')}
    </a>
  )
}

/** A thin bar while a lazy page loads. */
function NavigationProgress() {
  const navigation = useNavigation()
  const { t } = useI18n()
  const busy = navigation.state !== 'idle'
  return (
    <div
      role='progressbar'
      aria-label={t('common.loading')}
      aria-hidden={!busy}
      className={cn(
        'pointer-events-none fixed inset-x-0 top-0 z-[60] h-0.5 origin-left bg-brand transition-transform duration-500',
        busy ? 'scale-x-75' : 'scale-x-0'
      )}
    />
  )
}

/** After client-side navigation, screen readers start at the new page. */
function useFocusMainOnNavigate() {
  const { pathname } = useLocation()
  const first = useRef(true)
  useEffect(() => {
    if (first.current) {
      first.current = false
      return
    }
    document.getElementById('main')?.focus({ preventScroll: true })
  }, [pathname])
}

export function AppShell() {
  const { t } = useI18n()
  const title = useRouteTitle()
  useDocumentTitle(title ? t(title) : undefined)
  useFocusMainOnNavigate()

  return (
    <SidebarProvider defaultOpen={readSidebarOpen()}>
      <SkipToMain />
      <NavigationProgress />
      <AppSidebar />
      <SidebarInset className='min-w-0'>
        <header className='sticky top-0 z-40 flex h-14 shrink-0 items-center gap-2 rounded-t-xl border-b bg-background/90 px-4 backdrop-blur'>
          <SidebarTrigger variant='outline' className='max-md:scale-110' />
          <Separator orientation='vertical' className='mx-1 h-5' />
          <span className='min-w-0 flex-1 truncate text-sm font-medium text-muted-foreground'>
            {title ? t(title) : ''}
          </span>
          <LanguageSwitch />
          <ThemeSwitch />
        </header>
        <main id='main' tabIndex={-1} className='w-full min-w-0 flex-1 px-4 py-6 outline-none md:px-6 @container/content'>
          <div className='mx-auto w-full max-w-5xl'>
            <Outlet />
          </div>
        </main>
      </SidebarInset>
    </SidebarProvider>
  )
}
