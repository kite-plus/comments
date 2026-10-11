import { useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider, type DataRouter } from 'react-router'
import { isApiError } from '@/api'
import { AuthProvider } from '@/context/auth'
import { ThemeProvider } from '@/context/theme-provider'
import { I18nProvider, type Lang } from '@/i18n'
import { Toaster } from '@/components/ui/sonner'
import { TooltipProvider } from '@/components/ui/tooltip'

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Client errors will not change on retry; 401 has its own screen.
        retry: (count, error) => !(isApiError(error) && error.status < 500) && count < 2,
        staleTime: 15_000,
        refetchOnWindowFocus: true,
      },
    },
  })
}

export function App({
  router,
  queryClient,
  lang,
}: {
  router: DataRouter
  queryClient?: QueryClient
  lang?: Lang
}) {
  const [client] = useState(() => queryClient ?? createQueryClient())
  return (
    <QueryClientProvider client={client}>
      <I18nProvider initialLang={lang}>
        <ThemeProvider>
          <TooltipProvider delayDuration={300}>
            <AuthProvider>
              <Toaster duration={5000} />
              <RouterProvider router={router} />
            </AuthProvider>
          </TooltipProvider>
        </ThemeProvider>
      </I18nProvider>
    </QueryClientProvider>
  )
}
