<script setup lang="ts">
// One event from the catalogue, chosen from a menu: the event's name as the button, and in
// the menu every event grouped by category with its description, narrowed by a search that
// is ready to type into once the menu opens.
import type { EventDefinition } from '~/utils/events-sample'

const props = defineProps<{
  catalogue: readonly EventDefinition[]
  /** What the choice is, for a screen reader: "Step 2 event". */
  label: string
}>()

const event = defineModel<string>({ required: true })

const search = ref<string | null>('')

const byCategory = computed(() => {
  const needle = (search.value ?? '').trim().toLowerCase()
  const groups = new Map<string, EventDefinition[]>()
  for (const entry of props.catalogue) {
    if (needle && !entry.name.includes(needle) && !entry.description.toLowerCase().includes(needle)) continue
    const label = EVENT_CATEGORY_LABELS[entry.category]
    groups.set(label, [...(groups.get(label) ?? []), entry])
  }
  return [...groups.entries()]
})

/** The search, ready to type into once the menu is open: the overlay takes focus on entering. */
function focusSearch() {
  document.querySelector<HTMLInputElement>('.v-overlay--active .ps-event-picker__search input')?.focus()
}
</script>

<template>
  <v-menu
    :offset="4"
    location="bottom start"
    @after-enter="focusSearch"
    @update:model-value="search = ''"
  >
    <template #activator="{ props: activator }">
      <button
        v-bind="activator"
        :aria-label="`${label}: ${event}`"
        class="inline-flex h-7 items-center gap-1 rounded-(--ps-radius-x-small) px-2 font-mono text-xs text-ink transition-colors duration-150 hover:bg-ink/5 focus-visible:outline-2 focus-visible:outline-primary"
        type="button"
      >
        {{ event }}
        <Icon aria-hidden="true" class="text-sm text-ink/45" name="material-symbols:unfold-more" />
      </button>
    </template>

    <v-card class="w-80" flat>
      <div class="border-b border-line p-2">
        <v-text-field
          v-model="search"
          aria-label="Find an event"
          class="ps-event-picker__search"
          clearable
          density="comfortable"
          hide-details
          placeholder="Find an event"
          prepend-inner-icon="material-symbols:search"
          variant="filled"
        />
      </div>
      <div class="max-h-80 overflow-y-auto p-1">
        <template v-for="[category, events] in byCategory" :key="category">
          <p class="m-0 px-2 pt-2 pb-1 text-[11px] text-ink/45">
            {{ category }}
          </p>
          <button
            v-for="entry in events"
            :key="entry.name"
            :class="entry.name === event ? 'bg-primary/8' : 'hover:bg-ink/4'"
            class="flex w-full flex-col items-start gap-0.5 rounded-(--ps-radius-x-small) px-2 py-1.5 text-left transition-colors duration-150"
            type="button"
            @click="event = entry.name"
          >
            <span class="font-mono text-xs text-ink">{{ entry.name }}</span>
            <span class="line-clamp-1 text-[11px] text-ink/55">{{ entry.description }}</span>
          </button>
        </template>
        <p v-if="!byCategory.length" class="m-0 px-2 py-3 text-xs text-ink/55">
          No event matches.
        </p>
      </div>
    </v-card>
  </v-menu>
</template>
