<script setup lang="ts">
// Which events a destination hears: the catalog in its groups, each event with what it means,
// a box to take it, and its payload a click away. A group's own box takes or drops the whole
// group, so "every purchase event" is one click and one event out of it is one more.
//
// "Every event" is its own choice rather than every box ticked: it takes the events added
// later too, which ticking today's boxes does not, and a destination listening to everything
// is a choice somebody should make on purpose.
const props = defineProps<{
  /** The payload version the previews are written in. */
  version: string
  error?: boolean
}>()

const model = defineModel<string[]>({ required: true })

const query = ref('')
const everything = computed(() => model.value.includes(EVERY_EVENT))

const matches = (entry: WebhookEventType, needle: string) => !needle
  || entry.type.includes(needle)
  || entry.summary.toLowerCase().includes(needle)
  || WEBHOOK_GROUP[entry.group].label.toLowerCase().includes(needle)

const groups = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return WEBHOOK_GROUPS.map(group => ({
    group,
    all: WEBHOOK_EVENTS.filter(entry => entry.group === group.key),
    shown: WEBHOOK_EVENTS.filter(entry => entry.group === group.key && matches(entry, needle))
  })).filter(entry => entry.shown.length)
})

const chosen = computed(() => new Set(eventsOf(model.value)))

function toggle(type: string) {
  if (everything.value) return
  model.value = chosen.value.has(type) ? model.value.filter(entry => entry !== type) : [...model.value, type]
}

type GroupState = 'all' | 'some' | 'none'
const stateOf = (all: readonly WebhookEventType[]): GroupState => {
  const taken = all.filter(entry => chosen.value.has(entry.type)).length
  return taken === all.length ? 'all' : taken ? 'some' : 'none'
}

function toggleGroup(all: readonly WebhookEventType[]) {
  if (everything.value) return
  const types = all.map(entry => entry.type)
  model.value = stateOf(all) === 'all'
    ? model.value.filter(entry => !types.includes(entry))
    : [...new Set([...model.value, ...types])]
}

function setEverything(value: boolean) {
  model.value = value ? [EVERY_EVENT] : []
}

const previewing = ref<WebhookEventType | null>(null)
const previewOpen = computed({
  get: () => previewing.value !== null,
  set: (value) => {
    if (!value) previewing.value = null
  }
})

/* A group's box is filled for "every event" too, which takes the whole group. */
const groupBox = (all: readonly WebhookEventType[]) => BOX[everything.value ? 'all' : stateOf(all)]

const BOX = {
  all: 'border-primary bg-primary text-white',
  some: 'border-primary bg-primary text-white',
  none: 'border-ink/25'
} as const
</script>

<template>
  <div class="flex flex-col gap-3">
    <!-- Every event, or chosen ones. -->
    <div class="grid gap-1.5 sm:grid-cols-2" role="radiogroup" aria-label="Which events">
      <button
        :aria-checked="!everything"
        :class="!everything ? 'border-primary bg-primary/6' : 'border-line bg-surface hover:bg-ink/3'"
        class="flex cursor-pointer items-start gap-2.5 rounded-(--ps-radius-small) border px-3 py-2.5 text-left transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        role="radio"
        type="button"
        @click="setEverything(false)"
      >
        <span :class="!everything ? 'border-primary' : 'border-ink/25'" aria-hidden="true" class="mt-0.5 flex size-3.5 flex-none items-center justify-center rounded-full border-2">
          <span v-if="!everything" class="size-1.5 rounded-full bg-primary" />
        </span>
        <span class="flex min-w-0 flex-col gap-0.5">
          <span class="text-xs font-medium text-ink">Chosen events</span>
          <span class="text-[11px] leading-4 text-ink/55">Only the ones ticked below. Recommended: your server handles what it asked for, nothing else.</span>
        </span>
      </button>
      <button
        :aria-checked="everything"
        :class="everything ? 'border-primary bg-primary/6' : 'border-line bg-surface hover:bg-ink/3'"
        class="flex cursor-pointer items-start gap-2.5 rounded-(--ps-radius-small) border px-3 py-2.5 text-left transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        role="radio"
        type="button"
        @click="setEverything(true)"
      >
        <span :class="everything ? 'border-primary' : 'border-ink/25'" aria-hidden="true" class="mt-0.5 flex size-3.5 flex-none items-center justify-center rounded-full border-2">
          <span v-if="everything" class="size-1.5 rounded-full bg-primary" />
        </span>
        <span class="flex min-w-0 flex-col gap-0.5">
          <span class="text-xs font-medium text-ink">Every event</span>
          <span class="text-[11px] leading-4 text-ink/55">All {{ WEBHOOK_EVENTS.length }} today, and new ones as they are added. For a log or a data lake.</span>
        </span>
      </button>
    </div>

    <div :class="error ? 'border-error' : 'border-line'" class="flex flex-col overflow-hidden rounded-(--ps-radius-large) border">
      <div class="flex flex-wrap items-center gap-3 border-b border-line bg-ink/2 px-3 py-2">
        <SearchField
          v-model="query"
          class="w-64 max-w-full"
          label="Search events"
          placeholder="purchase, refund, export…"
        />
        <span class="ml-auto text-[11px] text-ink/55">
          <template v-if="everything">Every event</template>
          <template v-else>
            <span class="font-mono text-ink">{{ chosen.size }}</span> of {{ WEBHOOK_EVENTS.length }} chosen
          </template>
        </span>
        <v-btn
          v-if="!everything && chosen.size"
          class="text-xs"
          color="neutral"
          size="small"
          variant="text"
          @click="model = []"
        >
          Clear
        </v-btn>
      </div>

      <div :class="everything ? 'opacity-55' : ''" class="max-h-[34rem] overflow-y-auto">
        <section v-for="{ group, all, shown } in groups" :key="group.key" class="border-b border-line/70 last:border-b-0">
          <!-- The group: its own box takes or drops every event in it. -->
          <div class="sticky top-0 z-1 flex items-center gap-3 bg-surface/95 px-3 py-2.5 backdrop-blur-sm">
            <button
              :aria-checked="stateOf(all) === 'all' ? 'true' : stateOf(all) === 'some' ? 'mixed' : 'false'"
              :aria-label="`Every ${group.label} event`"
              :disabled="everything"
              class="flex cursor-pointer items-center gap-2.5 text-left disabled:cursor-default"
              role="checkbox"
              type="button"
              @click="toggleGroup(all)"
            >
              <span :class="groupBox(all)" aria-hidden="true" class="flex size-3.5 flex-none items-center justify-center rounded-(--ps-radius-x-small) border-2">
                <Icon v-if="everything || stateOf(all) === 'all'" class="text-[11px]" name="material-symbols:check" />
                <Icon v-else-if="stateOf(all) === 'some'" class="text-[11px]" name="material-symbols:remove" />
              </span>
              <Icon :name="group.icon" aria-hidden="true" class="flex-none text-base text-ink/55" />
              <span class="text-xs font-medium text-ink">{{ group.label }}</span>
            </button>
            <span class="min-w-0 flex-1 truncate text-[11px] text-ink/50">{{ group.note }}</span>
            <span class="flex-none font-mono text-[11px] text-ink/45">{{ everything ? all.length : all.filter(entry => chosen.has(entry.type)).length }}/{{ all.length }}</span>
          </div>

          <ul class="m-0 list-none p-0 pb-1.5">
            <li v-for="entry in shown" :key="entry.type" class="group/row flex items-center gap-1 pr-2 pl-3 hover:bg-ink/2">
              <button
                :aria-checked="chosen.has(entry.type)"
                :disabled="everything"
                class="flex min-w-0 flex-1 cursor-pointer items-start gap-2.5 py-2 pl-6 text-left disabled:cursor-default"
                role="checkbox"
                type="button"
                @click="toggle(entry.type)"
              >
                <span :class="chosen.has(entry.type) ? BOX.all : BOX.none" aria-hidden="true" class="mt-0.5 flex size-3.5 flex-none items-center justify-center rounded-(--ps-radius-x-small) border-2">
                  <Icon v-if="chosen.has(entry.type)" class="text-[11px]" name="material-symbols:check" />
                </span>
                <span class="flex min-w-0 flex-col gap-0.5">
                  <span class="font-mono text-xs text-ink">{{ entry.type }}</span>
                  <span class="text-[11px] leading-4 text-ink/55">{{ entry.summary }}</span>
                </span>
              </button>
              <v-btn
                :aria-label="`See the ${entry.type} payload`"
                class="flex-none text-[11px] opacity-60 group-hover/row:opacity-100"
                color="neutral"
                prepend-icon="material-symbols:data-object"
                size="x-small"
                variant="text"
                @click="previewing = entry"
              >
                Payload
              </v-btn>
            </li>
          </ul>
        </section>

        <p v-if="!groups.length" class="m-0 px-4 py-8 text-center text-xs text-ink/50">
          No event matches “{{ query.trim() }}”.
        </p>
      </div>
    </div>

    <WebhookEventDialog
      v-if="previewing"
      v-model="previewOpen"
      :event-type="previewing"
      :version="props.version"
    />
  </div>
</template>
