<script setup lang="ts">
// One event's settings, on a page of their own: the same form a new event is made with, filled
// from what is saved, with the key fixed. Saving goes back to the event.
const route = useRoute()

const { eventByKey, loading, error, reload } = useLiveOps()

const key = computed(() => String(route.params.key ?? ''))
const event = computed(() => eventByKey(key.value))

usePageCrumb(() => (event.value ? `Edit ${event.value.name}` : 'Edit event'))

/* Reset draws the form again from what is saved. */
const formKey = ref(0)
</script>

<template>
  <div v-if="(loading || error) && !event" class="flex flex-col gap-8">
    <PageHeader title="Edit event" />
    <LoadState
      :error="error"
      :loading="loading"
      what="the event"
      @retry="reload"
    />
  </div>

  <div v-else-if="!event" class="flex flex-col gap-8">
    <PageHeader title="Event not found" />
    <LayerCard>
      <EmptyState description="There is no Live Ops event with this key in the project." icon="material-symbols:event-busy-outline" title="No such event">
        <template #actions>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            size="small"
            to="/growth/liveops"
            variant="flat"
          >
            All events
          </v-btn>
        </template>
      </EmptyState>
    </LayerCard>
  </div>

  <div v-else class="flex flex-col gap-8">
    <PageHeader :title="`Edit ${event.name}`">
      <template #badge>
        <StatusBadge :label="LIVEOPS_STATE[stateOf(event, Date.now()).state].label" :tone="LIVEOPS_STATE[stateOf(event, Date.now()).state].tone" />
      </template>
      <template #description>
        Changes are saved to the draft. Players get them once it is published, and a window already on changes for them then.
      </template>
    </PageHeader>

    <LiveOpsEventForm
      :key="formKey"
      :event="event"
      @cancel="navigateTo(`/growth/liveops/${event.key}`)"
      @reset="formKey += 1"
      @saved="navigateTo(`/growth/liveops/${$event}`)"
    />
  </div>
</template>
