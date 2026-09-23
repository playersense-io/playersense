<script setup lang="ts">
// A group of conditions and how they combine — all of them, any of them, none of them — with
// the word between rows saying so again, since a reader scanning the rows reads the joiner and
// not the heading. A row is a condition, a segment, or a group of its own, nested as deep as the
// server allows. A segment row is the one place a segment is referred to rather than copied:
// change the segment and every rule naming it follows.
import type { AudienceGroupNode, AudienceNode, AudienceSegmentNode, GroupMatch } from '~/utils/audience'

const props = withDefaults(defineProps<{
  segments: readonly SegmentDef[]
  depth?: number
  /** A segment that may not be referred to here: the one being edited. */
  exclude?: string
  /** The outermost group, which is never removed and reads "Everyone" when empty. */
  root?: boolean
}>(), {
  depth: 1,
  exclude: '',
  root: false
})

const group = defineModel<AudienceGroupNode>({ required: true })

const emit = defineEmits<{ remove: [] }>()

const match = computed({
  get: () => matchOf(group.value),
  set: (value: GroupMatch) => {
    group.value = withMatch(group.value, value)
  }
})

const joiner = computed(() => GROUP_MATCHES.find(entry => entry.value === match.value)!.joiner)

const MATCH_ITEMS = GROUP_MATCHES.map(entry => ({ value: entry.value, title: entry.label }))

const choosable = computed(() => props.segments.filter(segment => !segment.archived && segment.key !== props.exclude))

function replace(index: number, node: AudienceNode) {
  group.value = { ...group.value, children: group.value.children.map((child, at) => (at === index ? node : child)) }
}

function remove(index: number) {
  group.value = { ...group.value, children: group.value.children.filter((_, at) => at !== index) }
}

function add(node: AudienceNode) {
  group.value = { ...group.value, children: [...group.value.children, node] }
}

function segmentLabel(key: string) {
  return props.segments.find(segment => segment.key === key)?.name ?? key
}

const segmentItems = computed(() => choosable.value.map(segment => ({ value: segment.key, title: segment.name, subtitle: describeRule(segment.rule, segmentLabel) })))

const SEGMENT_MODES = [{ value: 'in', title: 'is in' }, { value: 'out', title: 'is not in' }]

function setSegment(index: number, node: AudienceSegmentNode, patch: Partial<AudienceSegmentNode>) {
  replace(index, { ...node, ...patch })
}

/* The server's limit is eight; three levels are as deep as a person can read at a glance. */
const canNest = computed(() => props.depth < 3)
</script>

<template>
  <div
    :class="root ? '' : 'rounded-(--ps-radius) border border-dashed border-ink/15 bg-ink/2 p-3'"
    class="flex flex-col gap-2"
  >
    <div v-if="!root || group.children.length > 1 || group.negate" class="flex items-center gap-2">
      <v-select
        v-model="match"
        :items="MATCH_ITEMS"
        aria-label="How these combine"
        class="ps-audience-group__match w-32 flex-none"
        density="compact"
        hide-details
        variant="outlined"
      />
      <span class="text-xs text-ink/55">these are true</span>
      <v-btn
        v-if="!root"
        aria-label="Remove group"
        class="ml-auto"
        color="neutral"
        icon="material-symbols:close"
        size="x-small"
        variant="text"
        @click="emit('remove')"
      />
    </div>

    <p v-if="root && !group.children.length" class="m-0 flex items-center gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs text-ink/70">
      <Icon aria-hidden="true" class="text-base text-ink/45" name="material-symbols:groups-outline" />
      Everyone. Add a condition or a segment to narrow it.
    </p>

    <ol class="m-0 flex list-none flex-col gap-2 p-0">
      <li v-for="(child, index) in group.children" :key="child.id" class="flex flex-col gap-2">
        <span
          v-if="index > 0"
          class="w-fit rounded-(--ps-radius-x-small) bg-ink/5 px-1.5 text-[10px] leading-5 font-medium tracking-wide text-ink/55 uppercase"
        >{{ joiner }}</span>

        <AudienceCondition
          v-if="child.type === 'condition'"
          :label="`Condition ${index + 1}`"
          :model-value="child"
          @remove="remove(index)"
          @update:model-value="replace(index, $event)"
        />

        <div v-else-if="child.type === 'segment'" class="flex flex-wrap items-center gap-1.5">
          <span class="inline-flex h-8 items-center gap-1.5 px-1 text-xs text-ink/60">
            <Icon aria-hidden="true" class="text-sm" name="material-symbols:group-outline" />
            Player
          </span>
          <v-select
            :items="SEGMENT_MODES"
            :model-value="child.negate ? 'out' : 'in'"
            aria-label="In or not in the segment"
            class="ps-audience-group__match w-32 flex-none"
            density="compact"
            hide-details
            variant="outlined"
            @update:model-value="setSegment(index, child, { negate: $event === 'out' })"
          />
          <v-autocomplete
            :items="segmentItems"
            :model-value="child.segment || null"
            aria-label="Segment"
            class="ps-audience-group__match min-w-48 flex-1"
            density="compact"
            hide-details
            placeholder="Choose a segment"
            variant="outlined"
            @update:model-value="setSegment(index, child, { segment: $event ?? '' })"
          >
            <template #item="{ props: item, item: option }">
              <v-list-item v-bind="item" :subtitle="option.subtitle" />
            </template>
          </v-autocomplete>
          <v-btn
            aria-label="Remove segment"
            color="neutral"
            icon="material-symbols:close"
            size="x-small"
            variant="text"
            @click="remove(index)"
          />
        </div>

        <AudienceGroup
          v-else
          :depth="depth + 1"
          :exclude="exclude"
          :model-value="child"
          :segments="segments"
          @remove="remove(index)"
          @update:model-value="replace(index, $event)"
        />
      </li>
    </ol>

    <div class="flex flex-wrap items-center gap-1">
      <v-btn
        class="text-xs"
        color="neutral"
        prepend-icon="material-symbols:add"
        size="small"
        variant="text"
        @click="add(newCondition())"
      >
        Condition
      </v-btn>
      <v-btn
        :disabled="!choosable.length"
        class="text-xs"
        color="neutral"
        prepend-icon="material-symbols:group-add-outline"
        size="small"
        variant="text"
        @click="add(newSegmentNode())"
      >
        Segment
      </v-btn>
      <v-btn
        v-if="canNest"
        class="text-xs"
        color="neutral"
        prepend-icon="material-symbols:account-tree-outline"
        size="small"
        variant="text"
        @click="add(newGroup())"
      >
        Group
      </v-btn>
    </div>
  </div>
</template>

<style scoped>
.ps-audience-group__match :deep(.v-field__input) {
  font-size: 12px;
}
</style>
