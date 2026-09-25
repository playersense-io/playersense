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
 * Options may carry a group, listed under a heading when it changes, and a note under
 * the label, for a menu long enough to need them: a dozen metrics in two families.
 *
 * Its cap on width is its container's plus that pull: capped at the container alone, it
 * comes out the pull short of its own label and truncates a name that fits.
 */
withDefaults(
  defineProps<{
    options: readonly GhostSelectOption[]
    /** What the control chooses, for a screen reader: "Metric". */
    label: string
    /** Shown until something is chosen. */
    placeholder?: string
    /**
     * Small is for a select inside a card's body rather than its title: the label at the
     * body's 0.75rem, and a shorter menu of compact rows, so a list of two dozen options
     * does not open over half the screen.
     */
    size?: 'default' | 'small'
  }>(),
  { placeholder: 'Choose', size: 'default' }
)

const model = defineModel<string>()
const open = ref(false)
</script>

<template>
  <button
    :aria-label="label"
    :class="[open ? 'bg-ink/5' : '', size === 'small' ? 'h-7 text-xs' : 'h-(--ps-toolbar-height) text-sm']"
    class="group -ml-2 inline-flex max-w-[calc(100%+0.5rem)] items-center gap-1 rounded-(--ps-radius) px-2 font-medium text-ink transition-colors duration-150 hover:bg-ink/5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
    type="button"
  >
    <span class="truncate">
      {{ options.find((option) => option.value === model)?.label ?? placeholder }}
    </span>
    <Icon
      :aria-hidden="true"
      class="flex-none text-base text-ink/50 transition-colors duration-150 group-hover:text-ink/80"
      name="material-symbols:unfold-more"
    />

    <v-menu
      v-model="open"
      :offset="4"
      activator="parent"
      location="bottom start"
    >
      <v-list
        :class="size === 'small' ? 'ps-ghost-select--small max-h-72' : 'max-h-[70vh]'"
        :density="size === 'small' ? 'compact' : undefined"
        min-width="208"
        nav
      >
        <template v-for="(option, index) in options" :key="option.value">
          <v-list-subheader
            v-if="option.group && option.group !== options[index - 1]?.group"
            class="text-[11px] text-ink/45"
          >
            {{ option.group }}
          </v-list-subheader>
          <v-list-item
            :active="option.value === model"
            :subtitle="option.note"
            :title="option.label"
            @click="model = option.value"
          >
            <template v-if="option.value === model" #append>
              <Icon :aria-hidden="true" class="text-base text-ink/70" name="material-symbols:check" />
            </template>
          </v-list-item>
        </template>
      </v-list>
    </v-menu>
  </button>
</template>

<style scoped>
/* The small menu's rows at the label's own size. The list is Vuetify's, so its title is
   reached through it. */
.ps-ghost-select--small :deep(.v-list-item-title) {
  font-size: 0.75rem;
}
</style>
