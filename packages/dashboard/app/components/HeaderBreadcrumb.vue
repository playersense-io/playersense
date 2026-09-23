<script setup lang="ts">
// Where the screen is, as a path from the app's root scope down. A group is a place in
// the tree and not a screen, so it is written but not linked unless it has a page of its
// own; the last crumb is where the person is and is not linked either. A page about one
// record adds its own last crumb through `usePageCrumb`.
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
</script>

<template>
  <nav aria-label="Breadcrumb" class="min-w-0 flex-1">
    <!-- Preflight is off and Vuetify's reset stops short of lists, so the list's own
         margin, padding and marker are removed here. -->
    <ol class="m-0 flex min-w-0 list-none items-center gap-2 p-0 text-[13px]">
      <li
        v-for="(crumb, index) in crumbs"
        :key="`${index}-${crumb.title}`"
        class="flex min-w-0 items-center gap-2"
      >
        <span v-if="index > 0" aria-hidden="true" class="text-ink/25 select-none">/</span>

        <!-- A crumb leading where the person already is stays text, or it would claim
             `aria-current` alongside the last one. -->
        <NuxtLink
          v-if="crumb.to && index < last && crumb.to !== route.path"
          :to="crumb.to"
          class="
            truncate font-medium text-ink/55 no-underline transition-colors
            hover:text-ink
          "
        >
          {{ crumb.title }}
        </NuxtLink>
        <span
          v-else
          :aria-current="index === last ? 'page' : undefined"
          :class="index === last ? 'font-semibold text-ink' : 'font-medium text-ink/55'"
          class="truncate"
        >
          {{ crumb.title }}
        </span>
      </li>
    </ol>
  </nav>
</template>
