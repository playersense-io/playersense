<script setup lang="ts">
import type { Step } from '../utils/components'
/**
 * Where a multi-step form is: the steps in order, the one the reader is on, the ones
 * behind them ticked. A step behind is a button, because going back to change a choice
 * is the whole reason to show the list; a step ahead is not, because it depends on
 * what the current one has not yet said.
 *
 * `reached` is for the case where that last sentence is not true: a wizard opened over a
 * record that already exists is a set of tabs rather than a funnel, and making somebody
 * click through two steps they are not changing to reach the third is a funnel pretending
 * to be one. A step at or before `reached` is a button whichever side of the current one
 * it sits on. Absent, nothing ahead is reachable and the list behaves as before.
 */
const props = withDefaults(
  defineProps<{
    steps: readonly Step[]
    current: string
    /** The furthest step the reader may jump to. */
    reached?: string
  }>(),
  { reached: '' }
)

const emit = defineEmits<{
  select: [id: string]
}>()

const at = computed(() => props.steps.findIndex(step => step.id === props.current))
const furthest = computed(() =>
  props.reached === '' ? -1 : props.steps.findIndex(step => step.id === props.reached)
)

function stateOf(index: number): 'done' | 'current' | 'ahead' | 'reachable' {
  if (index < at.value) return 'done'
  if (index === at.value) return 'current'
  return index <= furthest.value ? 'reachable' : 'ahead'
}

function clickable(index: number): boolean {
  const state = stateOf(index)
  return state === 'done' || state === 'reachable'
}
</script>

<template>
  <ol aria-label="Steps" class="m-0 flex list-none items-center gap-2 p-0 text-xs">
    <li v-for="(step, index) in steps" :key="step.id" class="flex min-w-0 items-center gap-2">
      <component
        :is="clickable(index) ? 'button' : 'span'"
        :aria-current="stateOf(index) === 'current' ? 'step' : undefined"
        :class="[
          stateOf(index) === 'current' ? 'text-ink' : '',
          clickable(index) ? 'text-ink/70 hover:text-ink' : '',
          stateOf(index) === 'ahead' ? 'text-ink/40' : ''
        ]"
        :type="clickable(index) ? 'button' : undefined"
        class="inline-flex items-center gap-2 rounded-(--ps-radius-x-small) py-0.5 pr-1 transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        @click="clickable(index) && emit('select', step.id)"
      >
        <span
          :class="[
            stateOf(index) === 'current' ? 'border-primary bg-primary text-on-primary' : '',
            stateOf(index) === 'done' ? 'border-ink/20 bg-ink/6 text-ink' : '',
            stateOf(index) === 'reachable' ? 'border-ink/20 text-ink/70' : '',
            stateOf(index) === 'ahead' ? 'border-line text-ink/45' : ''
          ]"
          aria-hidden="true"
          class="flex size-5 flex-none items-center justify-center rounded-full border font-mono text-[10px] leading-none tabular-nums"
        >
          <Icon v-if="stateOf(index) === 'done'" class="text-xs" name="material-symbols:check" />
          <template v-else>{{ index + 1 }}</template>
        </span>
        <span class="truncate font-medium">{{ step.label }}</span>
      </component>
      <span
        v-if="index < steps.length - 1"
        :class="stateOf(index) === 'done' ? 'bg-ink/25' : 'bg-line'"
        aria-hidden="true"
        class="h-px w-6 flex-none sm:w-10"
      />
    </li>
  </ol>
</template>
