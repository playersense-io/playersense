<script setup lang="ts">
/**
 * One setting: what it is called, what it does, and the control that changes it, laid
 * out so a card of them reads as a list. A setting that cannot be changed is a row too,
 * with the reason in place of a control, because a fixed rule the operator can see is
 * a question they do not have to ask.
 *
 * `disabled` dims the row for a setting that only means something while another is on,
 * and the control inside is the caller's to disable with it. A button that opens
 * something, rather than a control that holds a value, goes in `actions`: it shares the
 * title's line, so the description below keeps the row's whole width.
 */
withDefaults(
  defineProps<{
    label: string
    description?: string
    /** In place of a control: a word saying why there is none, "Always" or "Fixed". */
    fixed?: string
    /** The id of the control, so the label reaches it. */
    controlId?: string
    disabled?: boolean
  }>(),
  { description: '', fixed: '', controlId: '', disabled: false }
)
</script>

<template>
  <div
    :class="disabled ? 'opacity-45' : ''"
    class="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 py-4 transition-opacity duration-150 first:pt-0 last:pb-0"
  >
    <!--
      `basis-48` and not the implicit zero: with `flex-1 min-w-0` the text column shrinks to
      nothing rather than letting the row wrap, so a wide control beside it, a segmented
      control of four options, squeezed a paragraph into one word per line on a phone. A
      floor of 12rem is what makes `flex-wrap` above fire instead. It is chosen against the
      two ends: a switch still sits beside the text in a card at 390px, and anything the
      width of a segmented control drops under it. On a desktop every row fits as before.
    -->
    <div class="flex min-w-0 flex-1 basis-48 flex-col gap-1">
      <div class="flex items-center justify-between gap-4">
        <component
          :is="controlId ? 'label' : 'span'"
          :for="controlId || undefined"
          class="flex min-w-0 flex-wrap items-center gap-2 text-sm font-medium text-ink"
        >
          {{ label }}
          <span
            v-if="fixed"
            class="rounded-full border border-line px-1.5 text-[10px] leading-4 font-medium text-ink/55"
          >
            {{ fixed }}
          </span>
        </component>
        <div v-if="$slots.actions" class="-my-1 flex flex-none items-center gap-3">
          <slot name="actions" />
        </div>
      </div>
      <p v-if="description || $slots.description" class="m-0 text-xs leading-5 text-ink/60">
        <slot name="description">
          {{ description }}
        </slot>
      </p>
      <div v-if="$slots.detail" class="mt-1">
        <slot name="detail" />
      </div>
    </div>
    <div v-if="$slots.default" class="flex flex-none items-center">
      <slot />
    </div>
  </div>
</template>
