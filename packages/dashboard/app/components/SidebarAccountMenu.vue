<script setup lang="ts">
// Who is signed in, at the foot of the drawer, and the menu for the things that are about
// them rather than about what they are looking at. The mark is initials, because there is
// no picture to show and a fetched one would be a third-party request.
const props = defineProps<{
  /** Folded to the mark alone, with the name in a tooltip. */
  rail: boolean
  /** The signed-in name or address; a dash while it is unknown. */
  name: string
  items: readonly MenuItem[]
}>()

// `jane.doe@…` files as JD, `Jane Doe` as JD, `hasan` as HA.
const initials = computed(() => {
  const local = props.name.split('@')[0] ?? props.name
  const words = local.split(/[\s._+-]+/).filter(word => word.length > 0)
  const letters = words.length >= 2 ? `${words[0]?.charAt(0)}${words[1]?.charAt(0)}` : local.slice(0, 2)
  return letters.toUpperCase()
})
</script>

<template>
  <!-- The tile sits in a box the width of the rail, so folding the drawer moves nothing
       but the name. -->
  <button
    :aria-label="rail ? name : undefined"
    :class="rail ? 'justify-center' : ''"
    class="
      group flex h-10 w-full items-center overflow-hidden rounded-(--ps-radius) text-ink
      transition-[background-color,transform]
      hover:bg-ink/6
      focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary
      active:scale-[0.98]
    "
    type="button"
  >
    <span class="flex size-10 flex-none items-center justify-center">
      <span
        aria-hidden="true"
        class="
          flex size-7 items-center justify-center rounded-lg border border-line bg-ink/4
          text-[10px] font-semibold text-primary transition-colors
          group-hover:bg-ink/6
        "
      >
        {{ initials }}
      </span>
    </span>

    <span v-if="!rail" class="min-w-0 flex-1 truncate pr-3 text-left text-[13px]">
      {{ name }}
    </span>

    <v-tooltip
      v-if="rail"
      :text="name"
      activator="parent"
      location="end"
      open-delay="300"
    />

    <v-menu :offset="6" activator="parent" location="top start">
      <v-list min-width="232" nav>
        <p class="truncate px-3 pt-1 pb-2 text-xs text-ink/60">
          {{ name }}
        </p>

        <v-list-item
          v-for="item in items"
          :key="item.title"
          :prepend-icon="item.icon"
          :title="item.title"
          :to="item.to"
          @click="item.onSelect?.()"
        />
      </v-list>
    </v-menu>
  </button>
</template>
