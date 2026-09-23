<script setup lang="ts">
// The steps a funnel is made of, edited in place: each one an event from the catalogue,
// narrowed by any number of property conditions, moved up or down, or taken out. A funnel
// keeps between two and ten steps, so the controls that would take it past either end are
// off rather than refused after the fact.
//
// Choosing a different event clears the step's conditions, since they name properties of
// the event it was.
import type { EventDefinition } from '~/utils/events-sample'

const props = defineProps<{
  catalogue: readonly EventDefinition[]
}>()

const steps = defineModel<FunnelStep[]>({ required: true })

const search = ref<string | null>('')

const byCategory = computed(() => {
  const needle = (search.value ?? '').trim().toLowerCase()
  const groups = new Map<string, EventDefinition[]>()
  for (const event of props.catalogue) {
    if (needle && !event.name.includes(needle) && !event.description.toLowerCase().includes(needle)) continue
    const label = EVENT_CATEGORY_LABELS[event.category]
    groups.set(label, [...(groups.get(label) ?? []), event])
  }
  return [...groups.entries()]
})

/** The search, ready to type into once the menu is open: the overlay takes focus on entering. */
function focusSearch() {
  document.querySelector<HTMLInputElement>('.v-overlay--active .ps-funnel-steps__search input')?.focus()
}

const eventOf = (name: string) => props.catalogue.find(event => event.name === name)

function update(index: number, change: Partial<FunnelStep>) {
  steps.value = steps.value.map((step, at) => (at === index ? { ...step, ...change } : step))
}

function choose(index: number, event: string) {
  if (steps.value[index]?.event !== event) update(index, { event, where: [] })
}

function move(index: number, direction: -1 | 1) {
  const next = [...steps.value]
  const target = index + direction
  if (target < 0 || target >= next.length) return
  ;[next[index], next[target]] = [next[target]!, next[index]!]
  steps.value = next
}

function remove(index: number) {
  if (steps.value.length <= MIN_STEPS) return
  steps.value = steps.value.filter((_, at) => at !== index)
}

function add() {
  if (steps.value.length >= MAX_STEPS) return
  const last = steps.value[steps.value.length - 1]
  steps.value = [...steps.value, { event: last?.event ?? props.catalogue[0]?.name ?? '', where: [] }]
}

// --- Conditions ---------------------------------------------------------------------------

/** The property a step's "where" menu is choosing a value for, when it has got that far. */
const picking = ref<{ step: number, property: string } | null>(null)

const freeProperties = (step: FunnelStep) => (eventOf(step.event)?.properties ?? []).filter(property => !step.where.some(condition => condition.property === property.key))

function addCondition(index: number, property: string, value: string) {
  const step = steps.value[index]
  if (!step) return
  update(index, { where: [...step.where, { property, value }] })
  picking.value = null
}

function removeCondition(index: number, property: string) {
  const step = steps.value[index]
  if (step) update(index, { where: step.where.filter(condition => condition.property !== property) })
}
</script>

<template>
  <ol class="m-0 list-none p-0">
    <li
      v-for="(step, index) in steps"
      :key="index"
      class="group flex min-h-12 flex-wrap items-center gap-x-2 gap-y-1.5 border-b border-line px-5 py-2"
    >
      <span class="flex size-5 flex-none items-center justify-center rounded-full bg-ink/6 font-mono text-[11px] text-ink/70">
        {{ index + 1 }}
      </span>

      <v-menu
        :offset="4"
        location="bottom start"
        @after-enter="focusSearch"
        @update:model-value="search = ''"
      >
        <template #activator="{ props: activator }">
          <button
            v-bind="activator"
            :aria-label="`Step ${index + 1} event: ${step.event}`"
            class="inline-flex h-7 items-center gap-1 rounded-(--ps-radius-x-small) px-2 font-mono text-xs text-ink transition-colors duration-150 hover:bg-ink/5 focus-visible:outline-2 focus-visible:outline-primary"
            type="button"
          >
            {{ step.event }}
            <Icon aria-hidden="true" class="text-sm text-ink/45" name="material-symbols:unfold-more" />
          </button>
        </template>

        <v-card class="w-80" flat>
          <div class="border-b border-line p-2">
            <v-text-field
              v-model="search"
              aria-label="Find an event"
              class="ps-funnel-steps__search"
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
                v-for="event in events"
                :key="event.name"
                :class="event.name === step.event ? 'bg-primary/8' : 'hover:bg-ink/4'"
                class="flex w-full flex-col items-start gap-0.5 rounded-(--ps-radius-x-small) px-2 py-1.5 text-left transition-colors duration-150"
                type="button"
                @click="choose(index, event.name)"
              >
                <span class="font-mono text-xs text-ink">{{ event.name }}</span>
                <span class="line-clamp-1 text-[11px] text-ink/55">{{ event.description }}</span>
              </button>
            </template>
            <p v-if="!byCategory.length" class="m-0 px-2 py-3 text-xs text-ink/55">
              No event matches.
            </p>
          </div>
        </v-card>
      </v-menu>

      <span
        v-for="condition in step.where"
        :key="condition.property"
        class="inline-flex h-6 items-center gap-1 rounded-(--ps-radius-x-small) bg-ink/5 pr-0.5 pl-2 text-[11px] text-ink/80"
      >
        <span class="text-ink/50">where</span>
        <span class="font-mono">{{ condition.property }} = {{ condition.value }}</span>
        <button
          :aria-label="`Remove ${condition.property} = ${condition.value}`"
          class="flex size-5 items-center justify-center rounded-(--ps-radius-x-small) text-ink/45 hover:bg-ink/8 hover:text-ink"
          type="button"
          @click="removeCondition(index, condition.property)"
        >
          <Icon aria-hidden="true" class="text-sm" name="material-symbols:close" />
        </button>
      </span>

      <v-menu
        v-if="freeProperties(step).length"
        :close-on-content-click="false"
        :offset="4"
        location="bottom start"
        @update:model-value="picking = null"
      >
        <template #activator="{ props: activator }">
          <button
            v-bind="activator"
            class="inline-flex h-6 items-center gap-0.5 rounded-(--ps-radius-x-small) px-1.5 text-[11px] text-ink/50 transition-colors duration-150 hover:bg-ink/5 hover:text-ink"
            type="button"
          >
            <Icon aria-hidden="true" class="text-sm" name="material-symbols:add" />
            where
          </button>
        </template>

        <v-card class="w-60" flat>
          <template v-if="picking?.step === index">
            <button
              class="flex w-full items-center gap-1 border-b border-line px-3 py-2 text-left text-xs text-ink/60 hover:text-ink"
              type="button"
              @click="picking = null"
            >
              <Icon aria-hidden="true" class="text-sm" name="material-symbols:keyboard-arrow-left" />
              <span class="font-mono">{{ picking.property }}</span> =
            </button>
            <div class="max-h-64 overflow-y-auto p-1">
              <button
                v-for="value in eventOf(step.event)?.properties.find(property => property.key === picking?.property)?.values ?? []"
                :key="value"
                class="flex w-full rounded-(--ps-radius-x-small) px-2 py-1.5 text-left font-mono text-xs text-ink hover:bg-ink/4"
                type="button"
                @click="addCondition(index, picking!.property, value)"
              >
                {{ value }}
              </button>
            </div>
          </template>
          <template v-else>
            <p class="m-0 px-3 pt-2.5 pb-1 text-[11px] text-ink/55">
              Only count <span class="font-mono">{{ step.event }}</span> where
            </p>
            <div class="p-1">
              <button
                v-for="property in freeProperties(step)"
                :key="property.key"
                class="flex w-full items-center justify-between rounded-(--ps-radius-x-small) px-2 py-1.5 text-left hover:bg-ink/4"
                type="button"
                @click="picking = { step: index, property: property.key }"
              >
                <span class="font-mono text-xs text-ink">{{ property.key }}</span>
                <span class="text-[11px] text-ink/45">{{ property.type }}</span>
              </button>
            </div>
          </template>
        </v-card>
      </v-menu>

      <span class="ml-auto flex items-center opacity-60 transition-opacity duration-150 group-focus-within:opacity-100 group-hover:opacity-100">
        <button
          :aria-label="`Move step ${index + 1} up`"
          :disabled="index === 0"
          class="ps-funnel-steps__control"
          type="button"
          @click="move(index, -1)"
        >
          <Icon aria-hidden="true" class="text-base" name="material-symbols:keyboard-arrow-up" />
        </button>
        <button
          :aria-label="`Move step ${index + 1} down`"
          :disabled="index === steps.length - 1"
          class="ps-funnel-steps__control"
          type="button"
          @click="move(index, 1)"
        >
          <Icon aria-hidden="true" class="text-base" name="material-symbols:keyboard-arrow-down" />
        </button>
        <button
          :aria-label="`Remove step ${index + 1}`"
          :disabled="steps.length <= MIN_STEPS"
          class="ps-funnel-steps__control"
          type="button"
          @click="remove(index)"
        >
          <Icon aria-hidden="true" class="text-base" name="material-symbols:close" />
        </button>
      </span>
    </li>

    <li class="flex items-center justify-between px-5 py-2">
      <v-btn
        :disabled="steps.length >= MAX_STEPS"
        class="text-xs font-medium"
        color="neutral"
        prepend-icon="material-symbols:add"
        size="small"
        variant="text"
        @click="add"
      >
        Add step
      </v-btn>
      <span class="text-[11px] text-ink/45">{{ steps.length }} of {{ MAX_STEPS }} steps</span>
    </li>
  </ol>
</template>

<style scoped>
.ps-funnel-steps__control {
  align-items: center;
  border-radius: var(--ps-radius-x-small);
  color: rgba(var(--v-theme-on-surface), 0.6);
  display: inline-flex;
  height: 26px;
  justify-content: center;
  width: 26px;
}

.ps-funnel-steps__control:hover:not(:disabled) {
  background: rgba(var(--v-theme-on-surface), 0.06);
  color: rgb(var(--v-theme-on-surface));
}

.ps-funnel-steps__control:disabled {
  color: rgba(var(--v-theme-on-surface), 0.2);
}
</style>
