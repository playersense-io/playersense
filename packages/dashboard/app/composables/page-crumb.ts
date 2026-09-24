// The last breadcrumb, where the navigation tree cannot supply one. The tree names
// `/players/abc` after its nearest link ("Players"); a page about one record names itself
// here, and the breadcrumb and the tab title put it last. Cleared with the page's scope,
// so a name never trails behind the next screen.
//
// Cleared only if it is still this page's. Going from one named page to another (an event to
// its edit page), the next page names itself before the last one's scope goes, and a clear
// that did not check wiped the new name and left the trail a crumb short.
export function usePageCrumb(title: MaybeRefOrGetter<string>): void {
  const crumb = usePageCrumbValue()
  let written = ''
  watchEffect(() => {
    written = toValue(title)
    crumb.value = written
  })
  onScopeDispose(() => {
    if (crumb.value === written) crumb.value = ''
  })
}

/** What the breadcrumb reads. Empty for every page that names nothing. */
export function usePageCrumbValue(): Ref<string> {
  return useState<string>('page-crumb', () => '')
}
