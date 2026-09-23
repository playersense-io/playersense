import type { Router } from 'vue-router'

/**
 * Writes to the URL's query, batched: every change made in one tick goes out as one
 * navigation. Navigation is asynchronous, so two writes that each copied the query they
 * saw — the countries a filter dialog applies, then its empty versions — would each copy
 * the query from before either, and the second would undo the first. A key written as
 * undefined is taken out.
 */
const pending = new WeakMap<Router, Record<string, string | undefined>>()

function patchQuery(router: Router, patch: Record<string, string | undefined>) {
  let batch = pending.get(router)
  if (!batch) {
    const fresh: Record<string, string | undefined> = {}
    batch = fresh
    pending.set(router, fresh)
    queueMicrotask(() => {
      pending.delete(router)
      const merged = { ...router.currentRoute.value.query, ...fresh }
      const query = Object.fromEntries(Object.entries(merged).filter(([, value]) => value !== undefined))
      void router.replace({ query })
    })
  }
  Object.assign(batch, patch)
}

/**
 * The writer the choices below use, for a page that keeps a value of its own in the URL
 * — a price typed in — so its write joins the same batch rather than racing it.
 */
export function useQueryPatch() {
  const router = useRouter()
  return (patch: Record<string, string | undefined>) => patchQuery(router, patch)
}

/**
 * One choice kept in the URL's query: a tab, a period, a basis. What is chosen survives a
 * reload and travels with a link, so the page a colleague opens is the page that was sent.
 *
 * Only an allowed value is read back — anything else in the URL, hand-typed or left over
 * from a choice that no longer exists, reads as the fallback — and the fallback itself is
 * written as no parameter at all, so an untouched page keeps a clean URL. `allowed` may
 * be a getter, for choices that change with the page: the tabs of whichever report is
 * showing. Writes replace the history entry rather than push one, so Back leaves the
 * page instead of stepping through every tab.
 */
export function useQueryChoice<T extends string>(
  key: string,
  allowed: MaybeRefOrGetter<readonly T[]>,
  fallback: MaybeRefOrGetter<T>
) {
  const route = useRoute()
  const patch = useQueryPatch()

  return computed<T>({
    get: () => {
      const value = route.query[key]
      return typeof value === 'string' && toValue(allowed).includes(value as T) ? value as T : toValue(fallback)
    },
    set: value => patch({ [key]: value === toValue(fallback) ? undefined : value })
  })
}

/**
 * Several values kept in one query parameter, comma-separated: the platforms a page is
 * filtered to. Values not in `allowed` are dropped on the way in, and an empty list is
 * no parameter at all.
 */
export function useQueryList(key: string, allowed: MaybeRefOrGetter<readonly string[]>) {
  const route = useRoute()
  const patch = useQueryPatch()

  return computed<string[]>({
    get: () => {
      const value = route.query[key]
      if (typeof value !== 'string' || value === '') return []
      const known = toValue(allowed)
      return [...new Set(value.split(','))].filter(entry => known.includes(entry))
    },
    set: values => patch({ [key]: values.length ? values.join(',') : undefined })
  })
}
