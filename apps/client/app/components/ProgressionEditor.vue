<script setup lang="ts">
// A progression, defined in one line: an event, the number property it steps through, the
// values from and to, and the conditions every value is narrowed by. Only an event's
// number properties can be stepped through, and choosing another event takes the first
// of its own and clears the conditions, which named the old one's properties.
//
// The range is kept whole: `to` never under `from`, and never more than MAX_VALUES
// values from it.
import type { EventDefinition } from '~/utils/events'

const props = defineProps<{
  catalogue: readonly EventDefinition[]
}>()

const funnel = defineModel<ProgressionFunnel>({ required: true })

const numbers = (event: string) => (props.catalogue.find(entry => entry.name === event)?.properties ?? []).filter(property => property.type === 'number')

const properties = computed(() => numbers(funnel.value.event))

function chooseEvent(event: string) {
  if (event === funnel.value.event) return
  const first = numbers(event)[0]?.key ?? ''
  funnel.value = { ...funnel.value, event, property: first, where: [] }
}

function chooseProperty(property: string) {
  funnel.value = { ...funnel.value, property, where: funnel.value.where.filter(condition => condition.property !== property) }
}

/**
 * What the two fields hold while they are typed in. Applied on Enter or on leaving the
 * field, not on every key: clamped as it is typed, a "2" on its way to "25" would be
 * pulled up to `from` before the "5" arrived.
 */
const fromText = ref(String(funnel.value.from))
const toText = ref(String(funnel.value.to))

watch(() => [funnel.value.from, funnel.value.to], ([from, to]) => {
  fromText.value = String(from)
  toText.value = String(to)
})

function setBound(bound: 'from' | 'to') {
  const value = Math.round(Number(bound === 'from' ? fromText.value : toText.value))
  if (!Number.isFinite(value) || value < 0) {
    fromText.value = String(funnel.value.from)
    toText.value = String(funnel.value.to)
    return
  }
  let { from, to } = funnel.value
  if (bound === 'from') {
    from = value
    to = Math.min(Math.max(to, from), from + MAX_VALUES - 1)
  } else {
    to = Math.min(Math.max(value, from), from + MAX_VALUES - 1)
  }
  funnel.value = { ...funnel.value, from, to }
  fromText.value = String(from)
  toText.value = String(to)
}

const count = computed(() => funnel.value.to - funnel.value.from + 1)
</script>

<template>
  <div class="flex flex-wrap items-center gap-x-2 gap-y-2 px-5 py-3">
    <EventPicker
      :catalogue="catalogue"
      :model-value="funnel.event"
      label="Event"
      @update:model-value="chooseEvent"
    />

    <template v-if="properties.length">
      <span class="text-xs text-ink/55">through</span>

      <v-menu :offset="4" location="bottom start">
        <template #activator="{ props: activator }">
          <button
            v-bind="activator"
            :aria-label="`Property: ${funnel.property}`"
            class="inline-flex h-7 items-center gap-1 rounded-(--ps-radius-x-small) px-2 font-mono text-xs text-ink transition-colors duration-150 hover:bg-ink/5 focus-visible:outline-2 focus-visible:outline-primary"
            type="button"
          >
            {{ funnel.property }}
            <Icon aria-hidden="true" class="text-sm text-ink/45" name="material-symbols:unfold-more" />
          </button>
        </template>
        <v-list density="compact" min-width="180" nav>
          <v-list-item
            v-for="property in properties"
            :key="property.key"
            :active="property.key === funnel.property"
            @click="chooseProperty(property.key)"
          >
            <span class="font-mono text-xs">{{ property.key }}</span>
          </v-list-item>
        </v-list>
      </v-menu>

      <span class="text-xs text-ink/55">from</span>
      <v-text-field
        v-model="fromText"
        aria-label="From"
        class="ps-progression-editor__bound"
        density="comfortable"
        hide-details
        min="0"
        type="number"
        variant="filled"
        @blur="setBound('from')"
        @keydown.enter="setBound('from')"
      />
      <span class="text-xs text-ink/55">to</span>
      <v-text-field
        v-model="toText"
        aria-label="To"
        class="ps-progression-editor__bound"
        density="comfortable"
        hide-details
        :min="funnel.from"
        type="number"
        variant="filled"
        @blur="setBound('to')"
        @keydown.enter="setBound('to')"
      />

      <EventConditions
        :catalogue="catalogue"
        :event="funnel.event"
        :exclude="[funnel.property]"
        :model-value="funnel.where"
        @update:model-value="funnel = { ...funnel, where: $event }"
      />

      <span class="ml-auto text-[11px] text-ink/45">{{ count }} of at most {{ MAX_VALUES }} values</span>
    </template>

    <span v-else class="text-xs text-ink/55">
      has no number property to step through. Choose an event that carries one, like <span class="font-mono">level</span>.
    </span>
  </div>
</template>

<style scoped>
/* Wide enough for three digits and the spinner, and no wider: it sits in a sentence. */
.ps-progression-editor__bound {
  flex: none;
  width: 6rem;
}
</style>
