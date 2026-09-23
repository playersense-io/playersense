<script setup lang="ts">
// One entry of the drawer's list, in either width.
//
// A link is a row. A group with its own `to` is a row too: the page moves between its
// children (settings tabs), so listing them here would offer the same destinations twice.
// A folding group unfolds in the full drawer; in the rail it is one row whose menu holds
// the children, because a group unfolding inside a 56px column shows rows with nothing in
// them.
const props = defineProps<{
  entry: NavEntry
  rail: boolean
}>()

const route = useRoute()
const navigation = useNavigation()

// The link the current screen belongs to, from the tree rather than the router: a detail
// page (/analytics/players/abc) is a sibling route of its list, not a child, so the router
// would light nothing. The tree's rule is the breadcrumb's — the deepest link it sits under.
const current = computed(() => navigation.linkOf(route.path))

// Whether the current screen is inside the group, so the rail row lights and the unfolded
// header takes the ink of an active row (theme.css): the fold is where the person is.
const holds = computed(() => isGroup(props.entry) && props.entry.children.some(child => child === current.value))
</script>

<template>
  <!-- The label is hidden in the rail, so the tooltip says where a row goes and the
       aria-label is what a screen reader says. -->
  <v-list-item
    v-if="!isGroup(entry) || entry.to"
    :active="isGroup(entry) ? holds : entry === current"
    :aria-label="rail ? entry.title : undefined"
    :prepend-icon="entry.icon"
    :title="entry.title"
    :to="entry.to"
  >
    <v-tooltip
      v-if="rail"
      :text="entry.title"
      activator="parent"
      location="end"
      open-delay="300"
    />
  </v-list-item>

  <v-list-item
    v-else-if="rail"
    :active="holds"
    :aria-label="entry.title"
    :prepend-icon="entry.icon"
    link
  >
    <v-menu :offset="8" activator="parent" location="end top">
      <v-list min-width="208" nav>
        <p class="truncate px-3 pt-1 pb-2 text-xs text-ink/60">
          {{ entry.title }}
        </p>
        <v-list-item
          v-for="child in entry.children"
          :key="child.to"
          :title="child.title"
          :active="child === current"
          :to="child.to"
        />
      </v-list>
    </v-menu>
  </v-list-item>

  <!-- One chevron for both states, turned by theme.css rather than swapped, so it moves
       rather than blinks. -->
  <v-list-group
    v-else
    :class="{ 'ps-nav-group--current': holds }"
    :value="entry.id"
    collapse-icon="material-symbols:chevron-right"
    expand-icon="material-symbols:chevron-right"
  >
    <template #activator="{ props: activator }">
      <v-list-item v-bind="activator" :prepend-icon="entry.icon" :title="entry.title" />
    </template>

    <v-list-item
      v-for="child in entry.children"
      :key="child.to"
      :title="child.title"
      :active="child === current"
      :to="child.to"
    />
  </v-list-group>
</template>
