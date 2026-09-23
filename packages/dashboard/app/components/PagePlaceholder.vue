<script setup lang="ts">
// A screen that exists in the navigation but not yet in the product: its header, and an
// honest statement of why there is nothing under it.
withDefaults(defineProps<{
  title: string
  description?: string
  /** Set when the backend doesn't support this yet — cite the reason. Leave unset once real work starts. */
  blocked?: string
}>(), {
  description: undefined,
  blocked: undefined
})
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader :description="description" :title="title" />

    <LayerCard :padded="false">
      <EmptyState
        v-if="blocked"
        :description="blocked"
        icon="material-symbols:construction"
        title="Blocked on backend"
      />
      <EmptyState
        v-else
        description="This page is scaffolded but not yet connected to the API. See todos.md."
        icon="material-symbols:build-outline"
        title="Not wired up yet"
      />
    </LayerCard>
  </div>
</template>
