<script setup lang="ts">
// The conditions that narrow an event to the occurrences that count: a chip per condition,
// removable, and a "where" menu that lists the event's properties and then the values of
// the one chosen. A property is offered once; `exclude` keeps out one the caller already
// uses for something else — the level a progression steps through.
//
// Laid out as siblings rather than in a box of their own, so they wrap in the caller's row.
import type { EventDefinition } from '~/utils/events'

const props = withDefaults(defineProps<{
  catalogue: readonly EventDefinition[]
  event: string
  exclude?: readonly string[]
}>(), {
  exclude: () => []
})

const where = defineModel<FunnelCondition[]>({ required: true })

/** The property the menu is choosing a value for, once it has got that far. */
const picking = ref<string | null>(null)

const properties = computed(() => props.catalogue.find(entry => entry.name === props.event)?.properties ?? [])

const free = computed(() => properties.value.filter(property => !props.exclude.includes(property.key) && !where.value.some(condition => condition.property === property.key)))

const values = computed(() => properties.value.find(property => property.key === picking.value)?.values ?? [])

function add(value: string) {
  if (!picking.value) return
  where.value = [...where.value, { property: picking.value, value }]
  picking.value = null
}

function remove(property: string) {
  where.value = where.value.filter(condition => condition.property !== property)
}
</script>

<template>
  <span
    v-for="condition in where"
    :key="condition.property"
    class="inline-flex h-6 items-center gap-1 rounded-(--ps-radius-x-small) bg-ink/5 pr-0.5 pl-2 text-[11px] text-ink/80"
  >
    <span class="text-ink/50">where</span>
    <span class="font-mono">{{ condition.property }} = {{ condition.value }}</span>
    <button
      :aria-label="`Remove ${condition.property} = ${condition.value}`"
      class="flex size-5 items-center justify-center rounded-(--ps-radius-x-small) text-ink/45 hover:bg-ink/8 hover:text-ink"
      type="button"
      @click="remove(condition.property)"
    >
      <Icon :aria-hidden="true" class="text-sm" name="material-symbols:close" />
    </button>
  </span>

  <v-menu
    v-if="free.length"
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
        <Icon :aria-hidden="true" class="text-sm" name="material-symbols:add" />
        where
      </button>
    </template>

    <v-card class="w-60" flat>
      <template v-if="picking">
        <button
          class="flex w-full items-center gap-1 border-b border-line px-3 py-2 text-left text-xs text-ink/60 hover:text-ink"
          type="button"
          @click="picking = null"
        >
          <Icon :aria-hidden="true" class="text-sm" name="material-symbols:keyboard-arrow-left" />
          <span class="font-mono">{{ picking }}</span> =
        </button>
        <div class="max-h-64 overflow-y-auto p-1">
          <button
            v-for="value in values"
            :key="value"
            class="flex w-full rounded-(--ps-radius-x-small) px-2 py-1.5 text-left font-mono text-xs text-ink hover:bg-ink/4"
            type="button"
            @click="add(value)"
          >
            {{ value }}
          </button>
        </div>
      </template>
      <template v-else>
        <p class="m-0 px-3 pt-2.5 pb-1 text-[11px] text-ink/55">
          Only count <span class="font-mono">{{ event }}</span> where
        </p>
        <div class="p-1">
          <button
            v-for="property in free"
            :key="property.key"
            class="flex w-full items-center justify-between rounded-(--ps-radius-x-small) px-2 py-1.5 text-left hover:bg-ink/4"
            type="button"
            @click="picking = property.key"
          >
            <span class="font-mono text-xs text-ink">{{ property.key }}</span>
            <span class="text-[11px] text-ink/45">{{ property.type }}</span>
          </button>
        </div>
      </template>
    </v-card>
  </v-menu>
</template>
