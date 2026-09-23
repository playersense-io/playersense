<script setup lang="ts">
// A yes-or-no among a column of fields: the question on the left edge, the switch on the right
// edge, and the whole row the target. Smaller than the settings switch — a step under it, 28 by
// 16 — because here it sits among 36px fields as one more of them, not as the row's one control.
defineProps<{
  label: string
  /** One quieter line under the question, for what saying yes changes. */
  note?: string
}>()

const model = defineModel<boolean>({ required: true })

const id = useId()
</script>

<template>
  <div class="flex items-center justify-between gap-4">
    <label :for="id" class="flex min-w-0 cursor-pointer flex-col gap-0.5">
      <span class="text-[13px] text-ink">{{ label }}</span>
      <span v-if="note" class="text-[11px] leading-4 text-ink/50">{{ note }}</span>
    </label>
    <v-switch
      :id="id"
      v-model="model"
      class="ps-switch-row flex-none"
      color="primary"
      hide-details
      inset
    />
  </div>
</template>

<style scoped>
/* theme.css draws the inset switch at 36 by 20; this one is the step below. The thumb travels
   the track less its own width less two pixels, as there. */
.ps-switch-row {
  --v-switch-track-height: 16px;
  --v-switch-thumb-height: 12px;
  --v-switch-thumb-width: 12px;
}

.ps-switch-row :deep(.v-switch__track) {
  min-width: 28px;
}

.ps-switch-row :deep(.v-selection-control__wrapper) {
  width: 28px;
}

.ps-switch-row :deep(.v-selection-control__input) {
  transform: translateX(-6px);
}

.ps-switch-row :deep(.v-selection-control--dirty .v-selection-control__input) {
  transform: translateX(6px);
}
</style>
