<script setup lang="ts">
// One rule of a flag: if a player is in this audience, serve this value, to this share of them.
// Rules are read top to bottom and the first that matches answers, so a rule's number is part of
// what it means — it is on the card, and moving a rule is a control on it rather than a drag
// somebody could make by accident.
//
// The rollout is the part people misread, so the card says it plainly: a player who matches
// this rule but falls outside its share gets the fallback, not the next rule.
const props = withDefaults(defineProps<{
  index: number
  count: number
  kind: FlagKind
  segments: readonly SegmentDef[]
  grantsValue?: boolean
  problems?: readonly FlagProblem[]
  suggestions?: readonly string[]
}>(), {
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
          size="x-small"
          variant="text"
          @click="emit('move', -1)"
        />
        <v-btn
          :disabled="index === count - 1"
          aria-label="Move down"
          color="neutral"
          icon="material-symbols:arrow-downward"
          size="x-small"
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
              size="x-small"
              variant="text"
            />
          </template>
          <v-list density="compact" nav>
            <v-list-item prepend-icon="material-symbols:content-copy-outline" title="Duplicate rule" @click="emit('duplicate')" />
            <v-list-item prepend-icon="material-symbols:delete-outline" title="Remove rule" @click="emit('remove')" />
          </v-list>
        </v-menu>
      </div>
    </header>

    <!-- A JSON document wants the card's width; everything else sits beside the audience. -->
    <div :class="kind === 'json' ? '' : 'lg:grid-cols-[minmax(0,1fr)_minmax(0,18rem)]'" class="grid gap-x-6 gap-y-5 p-4">
      <div class="flex min-w-0 flex-col gap-2">
        <p class="m-0 text-[11px] font-medium tracking-wide text-ink/45 uppercase">
          If the player matches
        </p>
        <AudienceEditor
          v-model="audience"
          :grants-value="grantsValue"
          :problem="audienceProblem"
          :segments="segments"
        />
      </div>

      <div :class="kind === 'json' ? 'border-t border-line pt-5' : 'lg:border-l lg:border-line lg:pl-6'" class="flex min-w-0 flex-col gap-5">
        <div class="flex flex-col gap-2">
          <p class="m-0 text-[11px] font-medium tracking-wide text-ink/45 uppercase">
            Serve
          </p>
          <FlagValueEditor
            v-model="value"
            v-model:invalid="invalid"
            :error="valueProblem !== ''"
            :kind="kind"
            :label="`Rule ${index + 1} value`"
            :suggestions="suggestions"
          />
          <span v-if="valueProblem" class="text-[11px] text-error">{{ valueProblem }}</span>
        </div>

        <div class="flex flex-col gap-2">
          <p class="m-0 text-[11px] font-medium tracking-wide text-ink/45 uppercase">
            To this share of them
          </p>
          <RolloutField v-model="rollout" :label="`Rule ${index + 1} rollout`" />
          <p v-if="rollout < FULL_ROLLOUT" class="m-0 text-[11px] leading-4 text-ink/55">
            The other {{ formatRollout(FULL_ROLLOUT - rollout) }} of this audience get the fallback, not the next rule.
          </p>
        </div>
      </div>
    </div>
  </section>
</template>
