/**
 * A sample answer that arrives the way the API's will: a moment after the question
 * changes, with the page showing it is waiting in between. Stands in for the fetch until
 * the API client is wired in, so every loading state a page has is exercised now rather
 * than discovered then.
 *
 * The question is compared by its JSON, so an edit that asks the same thing again does
 * not reload. The answer on screen stays the last one until the next arrives — drawn as
 * loading, never blank — and a question asked while another is on its way replaces it.
 */
export function useSampleQuery<Question, Answer>(
  question: () => Question,
  answer: (question: Question) => Answer,
  latency = 450
) {
  const data = shallowRef<Answer>(answer(question()))
  const pending = ref(true)
  let timer: ReturnType<typeof setTimeout> | undefined

  const key = computed(() => JSON.stringify(question()))

  watch(key, () => {
    pending.value = true
    clearTimeout(timer)
    const asked = question()
    timer = setTimeout(() => {
      data.value = answer(asked)
      pending.value = false
    }, latency)
  }, { immediate: true })

  onScopeDispose(() => clearTimeout(timer))

  return { data, pending }
}
