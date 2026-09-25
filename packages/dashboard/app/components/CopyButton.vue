<script setup lang="ts">
/**
 * The one button beside a value somebody has to paste somewhere else.
 *
 * It reports back by becoming a tick for a second, because a copy leaves no other trace
 * and a button that looks unchanged reads as one that did nothing. Extracted so the
 * affordance is one object: `CopyField` boxes a value and puts this beside it, a fact in
 * `FactGrid` is plain text with this beside it, and the two cannot come to disagree about
 * what copying looks like.
 *
 * It always copies `value` itself, never what is on screen. A cell truncates and a fact
 * grid abbreviates; a button that copied the display would hand over a shortened id.
 */
const props = defineProps<{
  value: string
  /** What a screen reader hears: "Copy the project ID". */
  label: string
}>()

const { copy, copied } = useClipboard({ copiedDuring: 1400, legacy: true })
</script>

<template>
  <v-tooltip :text="copied ? 'Copied' : label" location="top">
    <template #activator="{ props: tip }">
      <button
        v-bind="tip"
        :aria-label="label"
        class="inline-flex size-6 flex-none items-center justify-center rounded-(--ps-radius-x-small) text-ink/50 transition-colors duration-150 hover:bg-ink/6 hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        type="button"
        @click="copy(props.value)"
      >
        <Icon
          :class="copied ? 'text-success' : ''"
          :name="copied ? 'material-symbols:check' : 'material-symbols:content-copy-outline'"
          :aria-hidden="true"
          class="text-sm"
        />
      </button>
    </template>
  </v-tooltip>
</template>
