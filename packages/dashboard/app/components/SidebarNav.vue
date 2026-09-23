<script setup lang="ts">
// The drawer's list: sections under a heading, groups that fold, and the footer entries
// under a rule. In the rail a heading has no room, so a rule stands in for it.
//
// Every group starts open. Which ones are folded is the drawer's state, not the route's: a
// person folds what they don't work in and it stays folded as they move about. What is
// kept is the groups folded rather than the ones open, so a group added later arrives
// open too. The group holding the current screen is reopened on arrival.
defineProps<{
  rail: boolean
}>()

const navigation = useNavigation()
const route = useRoute()

const foldable = computed(() => navigation.groups.filter(group => !group.to).map(group => group.id))

const closed = useLocalStorage<string[]>('playersense.nav.closed', [])

const opened = computed({
  get: () => foldable.value.filter(id => !closed.value.includes(id)),
  set: (ids: string[]) => {
    closed.value = foldable.value.filter(id => !ids.includes(id))
  }
})

watch(() => route.path, (path) => {
  const group = navigation.groupOf(path)
  if (group && closed.value.includes(group.id)) {
    closed.value = closed.value.filter(id => id !== group.id)
  }
}, { immediate: true })
</script>

<template>
  <!-- `multiple`: Vuetify's default `list` strategy resets the open set to the ancestors of
       whichever item the route selects, which would fold every other group on navigation. -->
  <v-list v-model:opened="opened" nav open-strategy="multiple">
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
