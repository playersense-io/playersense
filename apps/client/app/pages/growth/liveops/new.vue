<script setup lang="ts">
// A new event, on a page of its own: the form was too much for a dialog, and a page leaves room
// for the summary beside it. Opened from a day on the calendar, `?day=` is that day, and the
// event starts on it.
const route = useRoute()

usePageCrumb('New event')

const startDay = computed(() => {
  const day = Number(route.query.day)
  return Number.isFinite(day) && day > 0 ? day : null
})

/* Reset draws the form again from nothing, which is what a new form was. */
const formKey = ref(0)
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader description="Set it up once: what it is, when it is on, who gets it, what the game receives and what it should move. A repeat makes one event of every window." title="New event" />

    <LiveOpsEventForm
      :key="formKey"
      :start-day="startDay"
      @cancel="navigateTo('/growth/liveops')"
      @reset="formKey += 1"
      @saved="navigateTo(`/growth/liveops/${$event}`)"
    />
  </div>
</template>
