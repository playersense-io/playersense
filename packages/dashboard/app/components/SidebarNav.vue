<script setup lang="ts">
// The drawer's list: sections under a heading, groups that fold, and the footer entries
// under a rule. In the rail a heading has no room, so a rule stands in for it.
//
// Which groups are open is the drawer's state, not the route's: a person unfolds what they
// work in and it stays unfolded as they move about. The group holding the current screen
// is opened on arrival and never closed for them.
defineProps<{
  rail: boolean
}>()

const navigation = useNavigation()
const route = useRoute()

const opened = useLocalStorage<string[]>('playersense.nav.opened', [])

watch(() => route.path, (path) => {
  const group = navigation.groupOf(path)
  if (group && !group.to && !opened.value.includes(group.id)) {
    opened.value = [...opened.value, group.id]
  }
}, { immediate: true })
</script>

<template>
  <v-list v-model:opened="opened" nav>
    <template v-for="(section, index) in navigation.sections" :key="index">
      <template v-if="section.heading">
        <v-divider v-if="rail" class="my-2" />
        <v-list-subheader v-else>
          {{ section.heading }}
        </v-list-subheader>
      </template>

      <SidebarNavEntry
        v-for="entry in section.entries"
        :key="entry.title"
        :entry="entry"
        :rail="rail"
      />
    </template>

    <template v-if="navigation.footer?.length">
      <v-divider class="my-2" />
      <SidebarNavEntry
        v-for="entry in navigation.footer"
        :key="entry.title"
        :entry="entry"
        :rail="rail"
      />
    </template>
  </v-list>
</template>
