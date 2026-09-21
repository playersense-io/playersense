<script setup lang="ts">
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
  <div>
    <UPageHeader :title="title" :description="description" />
    <UAlert
      v-if="blocked"
      color="warning"
      variant="subtle"
      icon="i-lucide-construction"
      title="Blocked on backend"
      :description="blocked"
      class="mt-6"
    />
    <UAlert
      v-else
      color="neutral"
      variant="subtle"
      icon="i-lucide-hammer"
      title="Not wired up yet"
      description="This page is scaffolded but not yet connected to the API. See todos.md."
      class="mt-6"
    />
  </div>
</template>
