import type { UseFetchOptions, UseFetchReturn } from '@vueuse/core'
import { createFetch } from '@vueuse/core'
import type { MaybeRefOrGetter } from 'vue'
import { useAuthStore } from '~/stores/auth'

// The one way the dashboard talks to the API: VueUse's fetch, with the signed-in user's
// Firebase ID token on every request and the API's error body turned into one error the pages
// can show. Reads are reactive (useApiGet re-runs when its URL changes); writes are promises
// (apiRequest), so a dialog can await one and show what went wrong.

/** What the API said when it refused: its message is written to be shown as it is. */
export class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly code = '', readonly details: { field?: string, issue?: string }[] = []) {
    super(message)
  }

  /** The first field-level problem, for a form to put under the field. */
  fieldIssue(field: string): string | undefined {
    return this.details.find(detail => detail.field === field)?.issue
  }
}

interface ErrorBody {
  error?: { message?: string, code?: string, details?: { field?: string, issue?: string }[] }
}

function errorOf(status: number, body: unknown, fallback: string): ApiError {
  const parsed = typeof body === 'string' ? safeJson(body) : body as ErrorBody | null
  const error = parsed?.error
  let message = error?.message || fallback
  // A validation failure's useful words are in its details.
  if (error?.code === 'validation_error' && error.details?.length) {
    message = error.details.map(detail => detail.issue ? `${detail.field ? `${detail.field}: ` : ''}${detail.issue}` : '').filter(Boolean).join('; ') || message
  }
  return new ApiError(message, status, error?.code ?? '', error?.details ?? [])
}

function safeJson(text: string): ErrorBody | null {
  try {
    return JSON.parse(text) as ErrorBody
  } catch {
    return null
  }
}

let fetcher: ReturnType<typeof createFetch> | null = null

function client() {
  if (fetcher) return fetcher
  const base = useRuntimeConfig().public.apiBase.replace(/\/$/, '')
  const auth = useAuthStore()
  // Held from here: the error hook runs long after the setup that made the client.
  const router = useRouter()
  fetcher = createFetch({
    baseUrl: base,
    fetchOptions: { mode: 'cors' },
    options: {
      async beforeFetch({ url, options, cancel }) {
        // A read whose address is not known yet (no project chosen) is not made.
        if (url === base || url.endsWith('/null')) {
          cancel()
          return
        }
        const token = await auth.token()
        // A plain object, keeping VueUse's own spelling: it merges these over its headers by
        // key, and a Headers object would lowercase Content-Type into a second key, which the
        // browser then sends as one malformed "application/json, application/json".
        const headers = { ...(options.headers as Record<string, string> | undefined) }
        if (token) headers.Authorization = `Bearer ${token}`
        return { options: { ...options, headers } }
      },
      onFetchError(ctx) {
        const status = ctx.response?.status ?? 0
        if (status === 0) {
          ctx.error = new ApiError('Could not reach PlayerSense. Check the connection and try again.', 0)
          return ctx
        }
        if (status === 401) {
          // The session ended somewhere else: sign in again, and come back here.
          const next = router.currentRoute.value.fullPath
          void auth.signOut().then(() => router.push({ path: '/login', query: { next } }))
        }
        ctx.error = errorOf(status, ctx.data, ctx.response?.statusText || 'The request failed')
        return ctx
      }
    }
  })
  return fetcher
}

/**
 * A GET the page reads: fetched now, and again whenever the URL changes. A null URL waits.
 * `data` is typed as the API's response.
 */
export function useApiGet<T>(url: MaybeRefOrGetter<string | null | undefined>, options: UseFetchOptions = {}): UseFetchReturn<T> {
  const target = computed(() => toValue(url) ?? 'null')
  return client()(target, { refetch: true, ...options }).get().json<T>()
}

/** A request that changes something, or a read done once: resolves with the body, or throws ApiError. */
export async function apiRequest<T = unknown>(method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE', url: string, body?: unknown): Promise<T> {
  const request = client()(url, { immediate: false })
  const verb = method === 'GET' ? request.get() : method === 'POST' ? request.post(body) : method === 'PUT' ? request.put(body) : method === 'PATCH' ? request.patch(body) : request.delete(body)
  const { data, error, execute } = verb.json<T>()
  await execute()
  if (error.value) throw error.value instanceof ApiError ? error.value : new ApiError(String(error.value?.message ?? error.value), 0)
  return data.value as T
}

/** A URL with its query string, leaving out empty values. */
export function withQuery(path: string, query: Record<string, string | number | boolean | undefined | null | readonly string[]>): string {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '' || (Array.isArray(value) && value.length === 0)) continue
    params.set(key, Array.isArray(value) ? value.join(',') : String(value))
  }
  const text = params.toString()
  return text ? `${path}?${text}` : path
}

/**
 * Downloads a file the API streams (an export, a report's table): fetched with the user's token,
 * which a plain link cannot send, and saved under the name the API gave it.
 */
export async function apiDownload(url: string, fallbackName = 'download'): Promise<void> {
  const base = useRuntimeConfig().public.apiBase.replace(/\/$/, '')
  const token = await useAuthStore().token()
  const response = await fetch(base + url, { headers: token ? { Authorization: `Bearer ${token}` } : {} })
  if (!response.ok) {
    throw errorOf(response.status, await response.text(), response.statusText || 'The download failed')
  }
  const disposition = response.headers.get('Content-Disposition') ?? ''
  const name = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(disposition)?.[1] ?? fallbackName
  const href = URL.createObjectURL(await response.blob())
  const link = document.createElement('a')
  link.href = href
  link.download = decodeURIComponent(name)
  link.click()
  setTimeout(() => URL.revokeObjectURL(href), 1000)
}
