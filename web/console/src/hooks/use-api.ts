import {
  type InfiniteData,
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  type QueryKey,
} from '@tanstack/react-query'
import { toast } from 'sonner'
import { isApiError, type Page } from '@/api'
import { useT, type T } from '@/i18n'

export const keys = {
  me: ['me'] as const,
  myComments: (site: string | undefined) => ['me', 'comments', site ?? ''] as const,
  notifications: ['me', 'notifications'] as const,
  sessions: ['me', 'sessions'] as const,
  sites: ['sites'] as const,
  site: (id: string) => ['site', id] as const,
  siteComments: (id: string, status: string, thread: string) =>
    ['site', id, 'comments', status, thread] as const,
  threads: (id: string) => ['site', id, 'threads'] as const,
  bans: (id: string) => ['site', id, 'bans'] as const,
  reports: (id: string, status: string) => ['site', id, 'reports', status] as const,
  events: (id: string) => ['site', id, 'events'] as const,
  adminSites: (status: string) => ['admin', 'sites', status] as const,
}

/** A readable message for any failed request. */
export function errorMessage(t: T, error: unknown): string {
  if (isApiError(error)) {
    if (error.code === 'rate_limited')
      return t('error.rateLimited', { seconds: error.retryAfter ?? 60 })
    if (error.message) return error.message
    return t('error.unknown')
  }
  // fetch rejects with a TypeError when the network is down.
  if (error instanceof TypeError) return t('error.network')
  return t('error.unknown')
}

export function useErrorToast() {
  const t = useT()
  return (error: unknown) => toast.error(errorMessage(t, error))
}

/** A cursor-paged list (api.md section 1) with "load more". */
export function useCursorList<P extends Page<unknown>>(
  queryKey: QueryKey,
  fetchPage: (cursor: string | null) => Promise<P>,
  options: { enabled?: boolean } = {}
) {
  const query = useInfiniteQuery<P, Error, InfiniteData<P, string | null>, QueryKey, string | null>({
    queryKey,
    queryFn: ({ pageParam }) => fetchPage(pageParam),
    initialPageParam: null,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: options.enabled,
  })
  const items = (query.data?.pages.flatMap((page) => page.data) ?? []) as P['data']
  return { query, items, firstPage: query.data?.pages[0] }
}

/**
 * A write that shows its error as a toast and refreshes the given queries
 * afterwards, whether it succeeded or not.
 */
export function useApiMutation<TInput, TResult>(
  mutationFn: (input: TInput) => Promise<TResult>,
  options: {
    invalidate?: QueryKey[]
    onSuccess?: (result: TResult, input: TInput) => unknown
    /** Return true when the caller showed the error itself. */
    onError?: (error: unknown, input: TInput) => boolean | void
  } = {}
) {
  const queryClient = useQueryClient()
  const showError = useErrorToast()
  return useMutation({
    mutationFn,
    onSuccess: options.onSuccess,
    onError: (error, input) => {
      if (options.onError?.(error, input)) return
      // A 401 already sent the reader to the sign-in screen.
      if (isApiError(error, 'unauthorized')) return
      showError(error)
    },
    onSettled: () =>
      Promise.all(
        (options.invalidate ?? []).map((queryKey) => queryClient.invalidateQueries({ queryKey }))
      ),
  })
}
