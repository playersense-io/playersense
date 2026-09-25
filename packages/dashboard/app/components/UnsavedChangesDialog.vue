<script setup lang="ts">
/**
 * The question a screen with a draft asks when the reader leaves it. Three answers, not
 * two: keep editing, throw the draft away, or publish it and go.
 *
 * The third is what stops the dialog being a trap. A reader who is finished and clicked
 * the drawer is offered the thing they meant to do, and only where the draft would
 * actually save: a Publish that answers with a validation error from a dialog the page
 * behind is hidden by is worse than not offering it. When it cannot be offered, the
 * dialog says which problem is in the way rather than leaving a missing button to be
 * interpreted.
 *
 * Staying is the safe answer and sits left; discarding is the one that loses work and
 * wears the error colour.
 */
const props = withDefaults(
  defineProps<{
    /** How many changes go if the draft is discarded, so the loss has a size. */
    changes: number
    /** What one of them is called, so the count reads as a sentence: "change", "language". */
    noun?: string
    /**
     * What is at stake, in the screen's own terms. A prop rather than a fixed paragraph,
     * because what a draft costs differs: an unpublished flag keeps serving the old
     * rules, an unpublished LiveOps event never starts. A dialog that described the wrong one would be worse than a dialog that
     * described neither.
     */
    description?: string
    /** Whether publishing from here would be accepted. */
    publishable?: boolean
    /** Why it would not be, when it would not. */
    blockedReason?: string
    busy?: boolean
  }>(),
  {
    noun: 'change',
    description:
      'The draft is held for this project while the panel is open, and lost on a reload.',
    publishable: false,
    blockedReason: '',
    busy: false
  }
)

const open = defineModel<boolean>({ required: true })

const emit = defineEmits<{
  discard: []
  publish: []
}>()

const titleId = `${useId()}-leave-title`

function discard() {
  if (props.busy) return
  emit('discard')
  open.value = false
}
</script>

<template>
  <v-dialog v-model="open" :persistent="busy" max-width="480">
    <v-card
      :aria-labelledby="titleId"
      border
      flat
      role="dialog"
    >
      <div class="flex flex-col gap-5 p-5">
        <header class="flex flex-col gap-1">
          <h2 :id="titleId" class="m-0 text-sm font-medium text-ink">
            Leave with {{ changes === 1 ? `one ${noun}` : `${changes} ${noun}s` }} unpublished?
          </h2>
          <p class="m-0 text-xs leading-5 text-ink/60">
            {{ description }}
          </p>
        </header>

        <p
          v-if="!publishable && blockedReason"
          class="m-0 flex items-start gap-1.5 text-[11px] leading-4 text-ink/60"
        >
          <Icon
            :aria-hidden="true"
            class="mt-px flex-none text-sm text-ink/45"
            name="material-symbols:info-outline"
          />
          <span>{{ blockedReason }}</span>
        </p>

        <footer class="flex flex-wrap items-center justify-between gap-3">
          <v-btn
            :disabled="busy"
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="open = false"
          >
            Keep editing
          </v-btn>
          <span class="flex items-center gap-2">
            <v-btn
              :disabled="busy"
              class="text-xs font-medium"
              color="error"
              size="small"
              variant="text"
              @click="discard"
            >
              Discard changes
            </v-btn>
            <v-btn
              v-if="publishable"
              :loading="busy"
              class="text-xs font-medium"
              color="primary"
              size="small"
              variant="flat"
              @click="emit('publish')"
            >
              Publish and leave
            </v-btn>
          </span>
        </footer>
      </div>
    </v-card>
  </v-dialog>
</template>
