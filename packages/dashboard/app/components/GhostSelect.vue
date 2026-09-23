<script setup lang="ts">
import type { GhostSelectOption } from '../utils/components'
/**
 * A select that reads as a heading: the chosen option's label in the heading's own type,
 * an unfold glyph to say it opens, and no box until the pointer arrives. For the title
 * of a card that shows one of several metrics, where a field with an outline would make
 * the card's name look like a form.
 *
 * A button and a menu rather than `v-select`, because a field is the wrong shape here: it
 * carries a box, a floating label and an input's states, and every one of them would have
 * to be undone. The menu and its list are Vuetify's, so it opens, closes and moves with
 * the keyboard the way every other menu in the panel does, and `v-menu` marks the
 * activator up as one for a screen reader on its own.
 *
 * The button is pulled left by its own padding so the label sits on the card's edge
 * where a plain heading would, and the hover fill appears around it rather than beside.
 */
withDefaults(
  defineProps<{
    options: readonly GhostSelectOption[]
    /** What the control chooses, for a screen reader: "Metric". */
    label: string
    /** Shown until something is chosen. */
    placeholder?: string
  }>(),
  { placeholder: 'Choose' }
)

const model = defineModel<string>()
const open = ref(false)
</script>

<template>
  <button
    :aria-label="label"
    :class="open ? 'bg-ink/5' : ''"
    class="group -ml-2 inline-flex h-7 max-w-full items-center gap-1 rounded-(--ps-radius-x-small) px-2 text-sm font-medium text-ink transition-colors duration-150 hover:bg-ink/5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
    type="button"
  >
    <span class="truncate">
      {{ options.find((option) => option.value === model)?.label ?? placeholder }}
    </span>
    <Icon
      aria-hidden="true"
      class="flex-none text-base text-ink/50 transition-colors duration-150 group-hover:text-ink/80"
      name="material-symbols:unfold-more"
    />

    <v-menu
      v-model="open"
      :offset="4"
      activator="parent"
      location="bottom start"
    >
      <v-list min-width="208" nav>
        <v-list-item
          v-for="option in options"
          :key="option.value"
          :active="option.value === model"
          :title="option.label"
          @click="model = option.value"
        >
          <template v-if="option.value === model" #append>
            <Icon aria-hidden="true" class="text-base text-ink/70" name="material-symbols:check" />
          </template>
        </v-list-item>
      </v-list>
    </v-menu>
  </button>
</template>
