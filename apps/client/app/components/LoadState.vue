<script setup lang="ts">
// What a page shows while its data is on the way, or when the API refused it: a spinner, or the
// API's own words and a way to ask again. The page's content goes in the slot, shown once there
// is something to show.
withDefaults(defineProps<{
  loading: boolean
  error?: Error | null
  /** What failed to load, for the heading: "the project", "the webhooks". */
  what?: string
}>(), { error: null, what: 'this page' })

defineEmits<{ retry: [] }>()
</script>

<template>
  <div v-if="loading" class="flex justify-center py-16">
    <v-progress-circular
      color="primary"
      indeterminate
      size="28"
      width="3"
    />
  </div>
  <LayerCard v-else-if="error" :padded="false">
    <EmptyState
      :description="error.message"
      :title="`Could not load ${what}`"
      icon="material-symbols:cloud-off-outline"
    >
      <template #actions>
        <v-btn
          class="text-xs font-medium"
          color="primary"
          size="small"
          variant="flat"
          @click="$emit('retry')"
        >
          Try again
        </v-btn>
      </template>
    </EmptyState>
  </LayerCard>
  <slot v-else />
</template>
