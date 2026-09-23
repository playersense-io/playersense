<script setup lang="ts">
/**
 * The one action on a screen that cannot be taken back, in the one frame on the screen
 * that is not the colour of the others.
 *
 * Last on the page and apart from everything else: this is the place where weight should
 * differ before anything is read, and the notes ask for it separated from the ordinary
 * primary rather than sitting beside it. It is never sticky, because a destructive control
 * that follows the reader down a page they are only reading is a control waiting to be hit.
 *
 * `reason` is written out beside the action rather than parked in a tooltip. A disabled
 * button whose refusal is only discoverable by hovering is a puzzle, and the reader most
 * likely to meet it is the one who cannot hover.
 */
withDefaults(
  defineProps<{
    title: string
    description?: string
    /** Names the outcome: "Delete project", "Leave organisation". Never "Confirm". */
    action: string
    disabled?: boolean
    /** Why the action is refused, stated where the action is. */
    reason?: string
  }>(),
  { description: '', disabled: false, reason: '' }
)

defineEmits<{ confirm: [] }>()
</script>

<template>
  <section class="rounded-(--ps-radius-large) border border-error/25 bg-error/3 p-1">
    <header class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 px-3 py-2">
      <div class="flex min-w-0 flex-col gap-0.5">
        <h2 class="m-0 flex items-center gap-2 text-sm font-medium text-ink">
          <Icon aria-hidden="true" class="text-base text-error" name="material-symbols:warning-outline" />
          {{ title }}
        </h2>
        <p v-if="description" class="m-0 text-xs text-ink/55">
          {{ description }}
        </p>
      </div>
    </header>

    <div class="overflow-hidden rounded-(--ps-radius) border border-line bg-surface p-5">
      <div class="flex flex-wrap items-center justify-between gap-x-6 gap-y-4">
        <div class="min-w-0 flex-1 basis-64">
          <slot />
        </div>

        <div class="flex flex-none flex-col items-end gap-1.5">
          <v-btn
            :disabled="disabled"
            class="text-xs font-medium"
            color="error"
            size="small"
            variant="flat"
            @click="$emit('confirm')"
          >
            {{ action }}
          </v-btn>
          <p v-if="disabled && reason" class="m-0 max-w-64 text-right text-xs leading-4 text-ink/55">
            {{ reason }}
          </p>
        </div>
      </div>
    </div>
  </section>
</template>
