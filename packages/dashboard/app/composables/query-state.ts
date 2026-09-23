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
  const router = useRouter()

  return computed<T>({
    get: () => {
      const value = route.query[key]
      return typeof value === 'string' && toValue(allowed).includes(value as T) ? value as T : toValue(fallback)
    },
    set: (value) => {
      const { [key]: _, ...rest } = route.query
      void router.replace({ query: value === toValue(fallback) ? rest : { ...rest, [key]: value } })
    }
  })
}

/**
 * Several values kept in one query parameter, comma-separated: the platforms a page is
 * filtered to. Values not in `allowed` are dropped on the way in, and an empty list is
 * no parameter at all.
 */
export function useQueryList(key: string, allowed: MaybeRefOrGetter<readonly string[]>) {
  const route = useRoute()
  const router = useRouter()

  return computed<string[]>({
    get: () => {
      const value = route.query[key]
      if (typeof value !== 'string' || value === '') return []
      const known = toValue(allowed)
      return [...new Set(value.split(','))].filter(entry => known.includes(entry))
    },
    set: (values) => {
      const { [key]: _, ...rest } = route.query
      void router.replace({ query: values.length ? { ...rest, [key]: values.join(',') } : rest })
    }
  })
}
