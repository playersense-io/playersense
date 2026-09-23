<script setup lang="ts">
// One rule of a flag: if a player is in this audience, serve this value, to this share of them.
// Rules are read top to bottom and the first that matches answers, so a rule's number is part of
// what it means — it is on the card, and moving a rule is a control on it rather than a drag
// somebody could make by accident.
//
// If on the left, then on the right: the audience, and what that audience gets — the value to
// serve and the share of matching players who get it. The share is the part people misread, so
// under a partial one the card says plainly that the rest get the fallback, not the next rule.
const props = withDefaults(defineProps<{
  index: number
  count: number
  kind: FlagKind
  segments: readonly SegmentDef[]
  grantsValue?: boolean
  problems?: readonly FlagProblem[]
  suggestions?: readonly string[]
  /** What the rest of the audience gets, named under a partial share. */
  fallback?: Json
}>(), {
  fallback: null,
  grantsValue: false,
  problems: () => [],
  suggestions: () => []
})

const variant = defineModel<FlagVariant>({ required: true })
const invalid = defineModel<boolean>('invalid', { default: false })

const emit = defineEmits<{
  move: [by: -1 | 1]
  duplicate: []
  remove: []
}>()

const audience = computed({
  get: () => variant.value.audience,
  set: (value: TargetRule) => {
    variant.value = { ...variant.value, audience: value }
  }
})

const value = computed({
  get: () => variant.value.value,
  set: (next: Json) => {
    variant.value = { ...variant.value, value: next }
  }
})

/* The slider moves in whole percents; the field beside it takes a fraction. */
const share = computed({
  get: () => variant.value.rollout / 100,
  set: (next: number) => {
    variant.value = { ...variant.value, rollout: Math.round(next) * 100 }
  }
})

const rollout = computed({
  get: () => variant.value.rollout,
  set: (next: number) => {
    variant.value = { ...variant.value, rollout: next }
  }
})

const audienceProblem = computed(() => props.problems.find(problem => problem.field === 'audience')?.message ?? '')
const valueProblem = computed(() => props.problems.find(problem => problem.field === 'value')?.message ?? '')
</script>

<template>
  <section
    :aria-label="`Rule ${index + 1}`"
    :class="problems.length ? 'border-error/40' : 'border-line'"
    class="flex flex-col rounded-(--ps-radius-large) border bg-surface"
  >
    <header class="flex items-center gap-2 border-b border-line px-4 py-2">
      <span class="inline-flex size-5 flex-none items-center justify-center rounded-full bg-ink/8 font-mono text-[11px] text-ink/70">{{ index + 1 }}</span>
      <h3 class="m-0 text-xs font-medium text-ink">
        Rule {{ index + 1 }}
      </h3>
      <span class="min-w-0 truncate text-xs text-ink/50">{{ describeRule(variant.audience, key => segments.find(segment => segment.key === key)?.name ?? key) }}</span>
      <div class="ml-auto flex flex-none items-center">
        <v-btn
          :disabled="index === 0"
          aria-label="Move up"
          color="neutral"
          icon="material-symbols:arrow-upward"
          size="small"
          variant="text"
          @click="emit('move', -1)"
        />
        <v-btn
          :disabled="index === count - 1"
          aria-label="Move down"
          color="neutral"
          icon="material-symbols:arrow-downward"
          size="small"
          variant="text"
          @click="emit('move', 1)"
        />
        <v-menu location="bottom end">
          <template #activator="{ props: activator }">
            <v-btn
              v-bind="activator"
              :aria-label="`Rule ${index + 1} options`"
              color="neutral"
              icon="material-symbols:more-horiz"
              size="small"
              variant="text"
            />
          </template>
          <v-list min-width="200" nav>
            <v-list-item prepend-icon="material-symbols:content-copy-outline" title="Duplicate rule" @click="emit('duplicate')" />
            <v-list-item prepend-icon="material-symbols:delete-outline" title="Remove rule" @click="emit('remove')" />
          </v-list>
        </v-menu>
      </div>
    </header>

    <!-- If on the left, then on the right: who the rule is for, and what they get. A JSON
         document wants the card's width, so for one the two stack. -->
    <div
      :class="kind === 'json' ? '' : 'lg:grid-cols-[minmax(0,1fr)_20rem]'"
      class="grid"
    >
      <div class="flex min-w-0 flex-col gap-3 p-4">
        <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
          If
        </p>
        <AudienceEditor
          v-model="audience"
          :grants-value="grantsValue"
          :problem="audienceProblem"
          :segments="segments"
        />
      </div>

      <div
        :class="kind === 'json' ? 'border-t' : 'border-t lg:border-t-0 lg:border-l'"
        class="flex min-w-0 flex-col gap-4 border-line bg-ink/2 p-4 lg:rounded-br-(--ps-radius-large)"
      >
        <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
          Then
        </p>

        <div class="flex flex-col gap-1.5">
          <span class="flex items-center gap-1 text-xs text-ink/60">
            Serve
            <v-tooltip location="top" max-width="280" text="The value players who match this rule get. A player outside the share below gets the flag's fallback instead, and the rules after this one are not read for them.">
              <template #activator="{ props: tip }">
                <Icon
                  v-bind="tip"
                  aria-label="About the value"
                  class="text-sm text-ink/35"
                  name="material-symbols:info-outline"
                />
              </template>
            </v-tooltip>
          </span>
          <FlagValueEditor
            v-model="value"
            v-model:invalid="invalid"
            :error="valueProblem !== ''"
            :kind="kind"
            :label="`Rule ${index + 1} value`"
            :suggestions="suggestions"
            block
            dense
          />
          <span v-if="valueProblem" class="text-[11px] text-error">{{ valueProblem }}</span>
        </div>

        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">To</span>
          <div class="flex items-center gap-3">
            <ShareField v-model="rollout" :label="`Rule ${index + 1} share of matching players`" />
            <v-slider
              v-model="share"
              :aria-label="`Rule ${index + 1} share of matching players`"
              class="ps-share-slider min-w-0 flex-1"
              color="primary"
              hide-details
              max="100"
              min="0"
              step="1"
              thumb-size="14"
              track-color="neutral"
              track-size="4"
            />
          </div>
          <span class="text-[11px] leading-4 text-ink/50">of the players who match</span>
        </div>

        <p v-if="rollout < FULL_ROLLOUT" class="m-0 flex flex-wrap items-center gap-1 text-xs text-ink/55">
          <span>The other {{ formatRollout(FULL_ROLLOUT - rollout) }} get the fallback</span>
          <FlagValue :value="fallback" />
        </p>
      </div>
    </div>
  </section>
</template>

<style scoped>
/* The slider sits beside a 36px field, so its own row is that height and its track the quiet
   ink under the accent; the thumb keeps its ring only while it is held. */
.ps-share-slider :deep(.v-slider__container) {
  height: var(--ps-toolbar-height);
}

.ps-share-slider :deep(.v-slider-track__background) {
  opacity: 0.12;
}

.ps-share-slider :deep(.v-slider-thumb__surface::before) {
  display: none;
}
</style>
