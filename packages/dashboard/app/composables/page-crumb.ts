// The last breadcrumb, where the navigation tree cannot supply one. The tree names
// `/players/abc` after its nearest link ("Players"); a page about one record names itself
// here, and the breadcrumb and the tab title put it last. Cleared with the page's scope,
// so a name never trails behind the next screen.
export function usePageCrumb(title: MaybeRefOrGetter<string>): void {
  const crumb = usePageCrumbValue()
  watchEffect(() => {
    crumb.value = toValue(title)
  })
  onScopeDispose(() => {
    crumb.value = ''
  })
}

/** What the breadcrumb reads. Empty for every page that names nothing. */
export function usePageCrumbValue(): Ref<string> {
  return useState<string>('page-crumb', () => '')
}
