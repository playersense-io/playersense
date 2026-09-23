<script setup lang="ts">
// Where the screen is, as a path from the app's root scope down. A group is a place in
// the tree and not a screen, so it is written but not linked unless it has a page of its
// own; the last crumb is where the person is and is not linked either. A page about one
// record adds its own last crumb through `usePageCrumb`.
//
// One weight throughout: the current crumb is told apart by its ink, not by being bolder,
// so the trail never changes width as it changes colour. The first crumb of the way down
// the tree carries the drawer's glyph for it, which ties the bar to the row lit below.
// On a phone the middle crumbs go and the root and the screen stay, so which organization
// is being read survives the narrowest bar.
const props = withDefaults(defineProps<{
  root?: Crumb
}>(), {
  root: undefined
})

const route = useRoute()
const navigation = useNavigation()
const own = usePageCrumbValue()

const crumbs = computed<Crumb[]>(() => [
  ...(props.root ? [props.root] : []),
  ...navigation.crumbsOf(route.path),
  ...(own.value === '' ? [] : [{ title: own.value }])
])

const last = computed(() => crumbs.value.length - 1)

const NuxtLink = resolveComponent('NuxtLink')

/** Where a crumb links, or nothing: the last crumb, a group, and the current page are text. */
function hrefOf(crumb: Crumb, index: number): string | undefined {
  return crumb.to && index < last.value && crumb.to !== route.path ? crumb.to : undefined
}

function initial(title: string): string {
  return title.trim().charAt(0).toUpperCase()
}
</script>

<template>
  <nav aria-label="Breadcrumb" class="min-w-0 flex-1">
    <!-- Preflight is off and Vuetify's reset stops short of lists, so the list's own
         margin, padding and marker are removed here. -->
    <ol class="m-0 flex min-w-0 list-none items-center gap-0.5 p-0 text-[13px] font-normal">
      <li
        v-for="(crumb, index) in crumbs"
        :key="`${index}-${crumb.title}`"
        :class="index > 0 && index < last ? `
          hidden
          sm:flex
        ` : 'flex'"
        class="min-w-0 items-center gap-0.5"
      >
        <Icon
          v-if="index > 0"
          aria-hidden="true"
          class="flex-none text-base text-ink/25"
          name="material-symbols:chevron-right"
        />

        <!-- A crumb leading where the person already is stays text, or it would claim
             `aria-current` alongside the last one. -->
        <component
          :is="hrefOf(crumb, index) ? NuxtLink : 'span'"
          :aria-current="index === last ? 'page' : undefined"
          :class="[
            index === last ? 'text-ink' : 'text-ink/55',
            hrefOf(crumb, index) ? `
              hover:bg-ink/5 hover:text-ink
              focus-visible:outline-2 focus-visible:outline-offset-1
              focus-visible:outline-primary
            ` : ''
          ]"
          :to="hrefOf(crumb, index)"
          class="
            group flex h-7 min-w-0 items-center gap-1.5 rounded-(--ps-radius-small) px-1.5
            no-underline transition-colors duration-150
          "
        >
          <Icon
            v-if="crumb.icon"
            :name="crumb.icon"
            aria-hidden="true"
            class="
              flex-none text-[15px] text-ink/45 transition-colors
              group-hover:text-ink/80
            "
          />
          <!-- The root is the organization: its initial in the switcher's tile, smaller. -->
          <span
            v-else-if="root && index === 0"
            aria-hidden="true"
            class="
              flex size-4 flex-none items-center justify-center rounded-[4px] bg-primary
              text-[9px] leading-none text-on-primary
            "
          >
            {{ initial(crumb.title) }}
          </span>
          <span class="truncate">{{ crumb.title }}</span>
        </component>
      </li>
    </ol>
  </nav>
</template>
