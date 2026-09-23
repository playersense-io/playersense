<script setup lang="ts">
// Where the screen is: the brand mark, bare, as the way home, a dot, then the path down the
// navigation tree. A group is a place in the tree and not a screen, so it is written but
// not linked unless it has a page of its own; the last crumb is where the person is and is
// not linked either. A page about one record adds its own last crumb through `usePageCrumb`.
//
// One weight throughout: the current crumb is told apart by its ink, not by being bolder,
// so the trail never changes width as it changes colour. The first crumb carries the
// drawer's glyph for it, which ties the bar to the row lit below. On a phone the middle
// crumbs go and the mark and the screen stay.
//
// `LogoMark` is packages/ui's, which every app extending this layer extends too.

const route = useRoute()
const navigation = useNavigation()
const own = usePageCrumbValue()

const crumbs = computed<Crumb[]>(() => [
  ...navigation.crumbsOf(route.path),
  ...(own.value === '' ? [] : [{ title: own.value }])
])

const last = computed(() => crumbs.value.length - 1)

const NuxtLink = resolveComponent('NuxtLink')

/** Where a crumb links, or nothing: the last crumb, a group, and the current page are text. */
function hrefOf(crumb: Crumb, index: number): string | undefined {
  return crumb.to && index < last.value && crumb.to !== route.path ? crumb.to : undefined
}
</script>

<template>
  <nav aria-label="Breadcrumb" class="min-w-0 flex-1">
    <!-- Preflight is off and Vuetify's reset stops short of lists, so the list's own
         margin, padding and marker are removed here. -->
    <ol class="m-0 flex min-w-0 list-none items-center gap-0.5 p-0 text-[13px] font-normal">
      <li class="flex flex-none items-center gap-0.5">
        <NuxtLink
          aria-label="Overview"
          class="
            flex size-7 items-center justify-center rounded-(--ps-radius-small) text-ink
            no-underline transition-colors duration-150
            [&>svg]:text-ink
            hover:bg-ink/5
            focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary
          "
          to="/"
        >
          <LogoMark :size="16" />
        </NuxtLink>
        <Icon aria-hidden="true" class="flex-none text-lg text-ink/30" name="lucide:dot" />
      </li>

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
          <span class="truncate">{{ crumb.title }}</span>
        </component>
      </li>
    </ol>
  </nav>
</template>
