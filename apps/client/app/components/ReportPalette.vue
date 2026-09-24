<script setup lang="ts">
// Everything a report can be built from, by family: where rows come from, what can be done
// to them, and what becomes of them. Drag one onto the canvas where it should go, or click
// it to have it placed after the node selected.
const emit = defineEmits<{ add: [kind: NodeKind, config: NodeConfig] }>()

function start(event: DragEvent, kind: NodeKind, config: NodeConfig = {}) {
  if (!event.dataTransfer) return
  /* What the canvas reads back when it lands. */
  event.dataTransfer.setData('application/x-report-node', JSON.stringify({ kind, config }))
  event.dataTransfer.effectAllowed = 'move'
}

const query = ref('')
const groups = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return PALETTE.map(group => ({
    ...group,
    items: group.items.filter(item => !needle || item.label.toLowerCase().includes(needle) || item.note.toLowerCase().includes(needle))
  })).filter(group => group.items.length)
})

const familyOf = (key: NodeFamily) => FAMILIES.find(entry => entry.key === key)!
</script>

<template>
  <div class="flex h-full flex-col">
    <div class="border-b border-line p-3">
      <SearchField v-model="query" label="Search the parts" placeholder="Filter, group, export…" />
    </div>
    <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-3">
      <section v-for="group in groups" :key="group.family" class="flex flex-col gap-1.5">
        <h3 class="m-0 flex items-center gap-2 px-1 text-[11px] font-medium text-ink/50">
          <span :style="{ background: toneColour(familyOf(group.family).tone) }" aria-hidden="true" class="size-1.5 rounded-full" />
          {{ familyOf(group.family).label }}
        </h3>
        <button
          v-for="item in group.items"
          :key="`${item.kind}:${item.label}`"
          class="group flex cursor-grab items-start gap-2.5 rounded-(--ps-radius) border border-line bg-surface px-2.5 py-2 text-left transition-colors duration-150 hover:border-ink/20 hover:bg-ink/2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary active:cursor-grabbing"
          draggable="true"
          type="button"
          @click="emit('add', item.kind, item.config ?? {})"
          @dragstart="start($event, item.kind, item.config)"
        >
          <span
            :style="{ background: `color-mix(in srgb, ${toneColour(familyOf(group.family).tone)} 14%, transparent)`, color: toneColour(familyOf(group.family).tone) }"
            aria-hidden="true"
            class="flex size-6 flex-none items-center justify-center rounded-(--ps-radius-small) text-sm"
          >
            <Icon :name="item.icon" />
          </span>
          <span class="flex min-w-0 flex-col">
            <span class="text-xs font-medium text-ink">{{ item.label }}</span>
            <span class="text-[11px] leading-4 text-ink/50">{{ item.note }}</span>
          </span>
        </button>
      </section>
    </div>
    <p class="m-0 border-t border-line p-3 text-[11px] leading-4 text-ink/45">
      Drag a part onto the canvas, or click it to add it after the one selected. Connect a right edge to a left edge.
    </p>
  </div>
</template>
