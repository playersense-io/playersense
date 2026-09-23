<script setup lang="ts">
// The dashboard shell: a drawer the full height of the window, an app bar beside it, and
// a centred content column under the bar. Each app's layouts/default.vue wraps its pages
// in this with its own navigation tree and the rows only it has (the switcher at the top
// of the drawer, the account menu at its foot, extra actions in the bar).
//
// The drawer is declared before the bar because Vuetify's layout gives each edge to the
// component that registered first: this way the drawer owns the left edge top to bottom
// and the bar starts where it ends.
const props = withDefaults(defineProps<{
  navigation: DashboardNavigation
  /** Suffix of every tab title: "Players · PlayerSense". */
  product: string
  /** The breadcrumb's first crumb, when the app has a scope above its screens. */
  root?: Crumb
}>(), {
  root: undefined
})

provide(NAVIGATION_KEY, props.navigation)

const { mdAndUp } = useDisplay()
const route = useRoute()

// The tab title, from the navigation tree so the tab and the drawer never disagree about a
// name; a page about one record names itself through `usePageCrumb`, and that wins.
const crumb = usePageCrumbValue()
const title = computed(() => crumb.value || props.navigation.titleOf(route.path) || '')

useHead({ title: () => (title.value ? `${title.value} · ${props.product}` : props.product) })

// Per browser, not per account: collapsing the rail is a preference about this screen.
// `useLocalStorage` rather than `useStorage`, which Nitro owns and @vueuse/nuxt therefore
// leaves out of the auto-imports.
const rail = useLocalStorage('playersense.nav.rail', false)

// Whether the drawer shows, which only means anything below `md`, where it is an overlay.
// Starts null so Vuetify settles it from the viewport: open on a desktop, closed on a phone.
const open = ref<boolean | null>(null)

// The rail is a desktop arrangement; an overlay drawer is either open or gone.
const collapsed = computed(() => rail.value && mdAndUp.value)

// The overlay drawer closes once it has taken somebody somewhere.
watch(() => route.path, () => {
  if (!mdAndUp.value) open.value = false
})
</script>

<template>
  <v-app>
    <v-navigation-drawer
      v-model="open"
      :permanent="mdAndUp"
      :rail="collapsed"
      color="shell"
      mobile-breakpoint="md"
      width="260"
    >
      <!-- The bar's height plus the bar's rule, so the line under this row and the line
           under the bar are one line across the drawer's edge. -->
      <template #prepend>
        <div class="border-b border-shell-divider">
          <div class="flex h-14 items-center px-(--ps-nav-inset)">
            <slot name="switcher" :rail="collapsed" />
          </div>
        </div>
      </template>

      <SidebarNav :rail="collapsed" />

      <template #append>
        <div v-if="$slots.account" class="border-t border-shell-divider p-(--ps-nav-inset)">
          <slot name="account" :rail="collapsed" />
        </div>

        <!-- Desktop only: an overlay drawer has nothing to collapse to. The glyph sits in a
             box the width of the rail, so it stays put when the label goes. -->
        <div v-if="mdAndUp" class="border-t border-shell-divider p-(--ps-nav-inset)">
          <button
            :aria-expanded="!collapsed"
            :aria-label="collapsed ? 'Expand navigation' : 'Collapse navigation'"
            :class="collapsed ? 'justify-center' : ''"
            class="
              group flex h-10 w-full items-center overflow-hidden rounded-(--ps-radius)
              bg-ink/4 text-ink/70 transition-[background-color,color,transform]
              hover:bg-ink/6 hover:text-ink
              focus-visible:outline-2 focus-visible:outline-offset-2
              focus-visible:outline-primary
              active:scale-[0.98]
            "
            type="button"
            @click="rail = !rail"
          >
            <span class="flex size-10 flex-none items-center justify-center">
              <Icon
                :name="collapsed ? 'material-symbols:chevron-right' : 'material-symbols:chevron-left'"
                aria-hidden="true"
                class="text-lg"
              />
            </span>
            <span v-if="!collapsed" class="truncate pr-3 text-[13px] font-medium">Collapse</span>
            <v-tooltip
              v-if="collapsed"
              activator="parent"
              location="end"
              text="Expand"
            />
          </button>
        </div>
      </template>
    </v-navigation-drawer>

    <v-app-bar color="shell" flat height="56">
      <!-- The bar's contents sit in the page's own column, so the breadcrumb starts where
           the content starts and the actions end where it ends. -->
      <div class="mx-auto flex w-full max-w-7xl items-center gap-4 px-4 md:px-6">
        <v-app-bar-nav-icon
          v-if="!mdAndUp"
          aria-label="Open navigation"
          class="-ml-2"
          @click="open = !open"
        />
        <HeaderBreadcrumb :root="root" />
        <!-- The app's own actions; the theme toggle alone when it has none. An app that
             fills the slot places the toggle itself. -->
        <div class="flex items-center gap-1">
          <slot name="actions">
            <ThemeToggle />
          </slot>
        </div>
        <NavSearch />
      </div>
    </v-app-bar>

    <v-main>
      <div class="mx-auto w-full max-w-7xl p-4 md:p-6">
        <slot />
      </div>
    </v-main>
  </v-app>
</template>
