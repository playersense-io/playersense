<script setup lang="ts">
// One group of fields that folds: a heading that is the toggle, what the fields hold written
// beside it while it is shut, and the fields under it while it is open. For a long form read in
// parts — a player described by device, connection, history and context — where only the part
// being changed needs to be open and the rest still say what they are set to.
//
// The fold is the one `LayerCard` and `PageSection` use, a grid row from 0fr to 1fr, so the
// height animates without being measured, and `inert` keeps a shut group out of the tab order.
// Which group is open is the caller's: an accordion opens one and shuts the others.
defineProps<{
  title: string
  /** The group's values in a line, shown while it is shut: "Android · 2.5.1 · Turkish". */
  summary?: string
}>()

const open = defineModel<boolean>('open', { default: false })

const panelId = useId()
</script>

<template>
  <section class="border-b border-line last:border-b-0">
    <button
      :aria-controls="panelId"
      :aria-expanded="open"
      class="group flex w-full items-center gap-2 py-3 text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
      type="button"
      @click="open = !open"
    >
      <span class="flex flex-none items-center gap-1.5 text-xs font-medium text-ink">
        {{ title }}
        <slot name="mark" />
      </span>
      <span
        :class="open ? 'opacity-0' : 'opacity-100'"
        class="min-w-0 flex-1 truncate text-right text-xs text-ink/50 transition-opacity duration-150"
      >{{ summary }}</span>
      <Icon
        :class="open ? 'rotate-180' : ''"
        aria-hidden="true"
        class="flex-none text-base text-ink/45 transition-transform duration-200 group-hover:text-ink"
        name="material-symbols:keyboard-arrow-down"
      />
    </button>

    <div
      :id="panelId"
      :class="open ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]'"
      class="grid transition-[grid-template-rows] duration-200 ease-out"
    >
      <div :inert="!open" class="min-h-0 overflow-hidden">
        <div class="flex flex-col gap-3 pb-4">
          <slot />
        </div>
      </div>
    </div>
  </section>
</template>
