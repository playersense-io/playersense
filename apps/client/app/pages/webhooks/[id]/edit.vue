<script setup lang="ts">
// One destination's settings, on a page of their own: the same form a new one is made with,
// filled from what is saved. Saving goes back to the destination.
const route = useRoute()

const { destinationById } = useWebhooks()

const id = computed(() => String(route.params.id ?? ''))
const destination = computed(() => destinationById(id.value))

usePageCrumb(() => (destination.value ? `Edit ${destination.value.name}` : 'Edit destination'))

/* Reset draws the form again from what is saved. */
const formKey = ref(0)

/* Opened at a section ("Change events" links to #section-what): scroll to it once drawn. */
onMounted(() => {
  if (route.hash) setTimeout(() => document.querySelector(route.hash)?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 150)
})
</script>

<template>
  <div v-if="!destination" class="flex flex-col gap-8">
    <PageHeader title="Destination not found" />
    <LayerCard>
      <EmptyState description="There is no webhook destination with this id in the project." icon="material-symbols:webhook" title="No such destination">
        <template #actions>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            size="small"
            to="/webhooks"
            variant="flat"
          >
            All destinations
          </v-btn>
        </template>
      </EmptyState>
    </LayerCard>
  </div>

  <div v-else class="flex flex-col gap-8">
    <PageHeader :title="`Edit ${destination.name}`">
      <template #badge>
        <StatusBadge :label="destination.status === 'enabled' ? 'Enabled' : 'Disabled'" :tone="destination.status === 'enabled' ? 'success' : 'neutral'" />
      </template>
      <template #description>
        Changes apply from the next event. The signing secret is not changed here: roll it from the destination's Signing tab.
      </template>
    </PageHeader>

    <WebhookForm
      :key="formKey"
      :destination="destination"
      @cancel="navigateTo(`/webhooks/${destination.id}`)"
      @reset="formKey += 1"
      @saved="navigateTo(`/webhooks/${$event}`)"
    />
  </div>
</template>
