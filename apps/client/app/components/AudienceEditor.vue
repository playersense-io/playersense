<script setup lang="ts">
// Who a rule or a segment is for, built from conditions, segments and groups of them, and
// stored as the grammar the server reads. The tree is the editor's; the rule is the model, and
// a rule arriving from outside — a discard, another flag opened — rebuilds the tree, while one
// the tree itself just wrote does not, so the row being typed in keeps its focus.
//
// Below the rows, what the rule says in one sentence, and the one warning that matters: a flag
// that grants value, reached through a key the game client asserts (ADR 0027).
import type { AudienceGroupNode } from '~/utils/audience'

const props = withDefaults(defineProps<{
  segments: readonly SegmentDef[]
  exclude?: string
  /** The flag grants something of value, so a key the client can forge is warned. */
  grantsValue?: boolean
  /** Why the server would refuse it, already worded. */
  problem?: string
}>(), {
  exclude: '',
  grantsValue: false,
  problem: ''
})

const rule = defineModel<TargetRule>({ required: true })

const tree = ref<AudienceGroupNode>(rootFromRule(rule.value))
let written = ''

watch(rule, (next) => {
  if (JSON.stringify(next) === written) return
  tree.value = rootFromRule(next)
}, { deep: true })

function update(next: AudienceGroupNode) {
  tree.value = next
  const out = ruleFromRoot(next)
  written = JSON.stringify(out)
  rule.value = out
}

const segmentName = (key: string) => props.segments.find(segment => segment.key === key)?.name ?? key

const forgeable = computed(() => {
  const rules = new Map(props.segments.map(segment => [segment.key, segment.rule]))
  return [...new Set(keysIn(inlineSegments(rule.value, rules)).filter(key => authorityOf(key) === 'client'))]
})

const history = computed(() => {
  const rules = new Map(props.segments.map(segment => [segment.key, segment.rule]))
  return needsHistory(inlineSegments(rule.value, rules))
})
</script>

<template>
  <div class="flex flex-col gap-3">
    <AudienceGroup
      :exclude="exclude"
      :model-value="tree"
      :segments="segments"
      root
      @update:model-value="update"
    />

    <p v-if="problem" class="m-0 flex items-start gap-1.5 text-xs text-error">
      <Icon :aria-hidden="true" class="mt-0.5 flex-none text-sm" name="material-symbols:error-outline" />
      {{ problem }}
    </p>

    <InfoNote v-if="grantsValue && forgeable.length" icon="material-symbols:warning-outline" tone="warning">
      This flag grants value, and the audience reads
      <span v-for="(key, index) in forgeable" :key="key"><span class="font-mono">{{ key }}</span>{{ index < forgeable.length - 1 ? ', ' : '' }}</span>,
      which the game client sets itself. A modified client can send any value and qualify. Target on
      purchases, activity or the connection instead, which PlayerSense asserts.
    </InfoNote>

    <p v-if="history" class="m-0 flex items-start gap-1.5 text-[11px] leading-4 text-ink/50">
      <Icon :aria-hidden="true" class="mt-px flex-none text-sm" name="material-symbols:history" />
      Reads the player's purchase or activity history: each request costs a lookup, cached for an hour.
    </p>

    <p v-if="Object.keys(rule).length" class="m-0 text-[11px] leading-4 text-ink/50">
      <span class="text-ink/40">Reads as:</span> {{ describeRule(rule, segmentName) }}
    </p>
  </div>
</template>
