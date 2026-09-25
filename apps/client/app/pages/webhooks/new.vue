<script setup lang="ts">
// A new destination, on a page of its own: where events go, which ones, whose, and how.
usePageCrumb('New destination')

const { destinations } = useWebhooks()
const limit = useWebhookLimit()
const full = computed(() => limit.value !== null && destinations.value.length >= limit.value)

/* Reset draws the form again from nothing, which is what a new form was. */
const formKey = ref(0)
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader description="A URL on your own server, and the events it should hear. Every event is POSTed to it as signed JSON the moment it happens, and retried until it answers." title="New destination" />

    <LayerCard v-if="full">
      <EmptyState :description="`The plan allows ${limit} destinations and the project has them all. Delete one you no longer use, or add these events to one you have.`" icon="material-symbols:webhook" title="No room for another destination">
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

    <WebhookForm
      v-else
      :key="formKey"
      @cancel="navigateTo('/webhooks')"
      @reset="formKey += 1"
      @saved="navigateTo(`/webhooks/${$event}?tab=signing`)"
    />
  </div>
</template>
