<script setup lang="ts">
import type { Fact } from '../utils/components'

/**
 * The things about a subject that are true rather than adjustable, read across the page
 * instead of down it.
 *
 * A fact is a label and a value. Stacked as rows with a paragraph each, six of them are
 * four hundred pixels of reading before the first control; laid in a band they are one
 * glance, and the sentence that explained each one goes behind the glyph on its label,
 * where it is still reachable and no longer in the way. That trade is the whole component.
 *
 * It is a real `<dl>`, because that is what it is: the mosaic every page here draws by
 * hand is a definition list wearing `div`s, and a screen reader was being handed a run of
 * unrelated spans. `label` names the list, since a page carries more than one.
 *
 * No frame of its own beyond the hairline mosaic. A fact grid is state, and state is not a
 * task: the frame on the page belongs to the thing the reader came to change.
 */

const props = withDefaults(
  defineProps<{
    facts: readonly Fact[]
    /** Names the list for a screen reader: "Project facts". */
    label: string
    /**
     * How many across on a wide screen. Written out rather than interpolated, because
     * Tailwind reads the source for class names and a built one is not in the bundle.
     */
    columns?: 2 | 3 | 4 | 5 | 6
  }>(),
  { columns: 4 }
)

/**
 * The ladder each `columns` climbs, as the track count at each breakpoint rather than as
 * one class string. The classes still have to be written out for Tailwind to find them,
 * and the counts are wanted separately: the mosaic's rules depend on how many cells sit in
 * the last row, which is a different answer at every step of the ladder.
 */
const LADDER: Record<number, { at: string, cols: number, klass: string }[]> = {
  2: [
    { at: '', cols: 1, klass: '' },
    { at: 'sm', cols: 2, klass: 'sm:grid-cols-2' }
  ],
  3: [
    { at: '', cols: 1, klass: '' },
    { at: 'sm', cols: 2, klass: 'sm:grid-cols-2' },
    { at: 'lg', cols: 3, klass: 'lg:grid-cols-3' }
  ],
  4: [
    { at: '', cols: 1, klass: '' },
    { at: 'sm', cols: 2, klass: 'sm:grid-cols-2' },
    { at: 'lg', cols: 4, klass: 'lg:grid-cols-4' }
  ],
  5: [
    { at: '', cols: 1, klass: '' },
    { at: 'sm', cols: 2, klass: 'sm:grid-cols-2' },
    { at: 'lg', cols: 3, klass: 'lg:grid-cols-3' },
    { at: 'xl', cols: 5, klass: 'xl:grid-cols-5' }
  ],
  6: [
    { at: '', cols: 1, klass: '' },
    { at: 'sm', cols: 2, klass: 'sm:grid-cols-2' },
    { at: 'lg', cols: 3, klass: 'lg:grid-cols-3' },
    { at: 'xl', cols: 6, klass: 'xl:grid-cols-6' }
  ]
}

const ladder = computed(() => LADDER[props.columns] ?? LADDER[4] ?? [])
const tracks = computed(() =>
  ladder.value
    .map(step => step.klass)
    .filter(Boolean)
    .join(' ')
)

/**
 * The empty slots that complete the last row, one per breakpoint that needs them.
 *
 * The mosaic is borders on the cells rather than a gap over a darker ground, so a cell's
 * bottom rule is really the top border of whatever sits below it and its right rule the
 * left border of whatever sits beside it. That works while the facts fill every row. They
 * usually do not: four facts in three tracks leave a row of one, and the two cells above
 * the gap end up with no bottom rule and the last cell with no right rule, which is the
 * band coming apart at exactly the place a reader is looking.
 *
 * So the gap is filled. A filler is a cell with nothing in it, carrying the same two
 * borders, which is precisely the rule each of its neighbours was missing. How many are
 * needed differs per breakpoint, so each one is shown only where the row it completes is
 * short — `hidden lg:block`, and back to `hidden` again if a wider step does not want it.
 */
const fillers = computed(() => {
  const need = ladder.value.map(
    step => (step.cols - (props.facts.length % step.cols)) % step.cols
  )
  const most = Math.max(0, ...need)

  return Array.from({ length: most }, (_, index) => {
    const classes: string[] = []
    let shown = false
    for (const [step, count] of need.entries()) {
      const wanted = count > index
      if (wanted === shown) continue
      const at = ladder.value[step]?.at ?? ''
      const name = wanted ? 'block' : 'hidden'
      classes.push(at === '' ? name : `${at}:${name}`)
      shown = wanted
    }
    /* Nothing at the narrowest step means it starts hidden, which has to be said. */
    if (classes[0] !== 'hidden' && classes[0] !== 'block') classes.unshift('hidden')
    return { key: `filler-${index}`, klass: classes.join(' ') }
  })
})
</script>

<template>
  <!-- The rules are borders on the cells pulled a pixel into their neighbours, not a
       `gap-px` over a darker ground. A gap only draws a clean mosaic while the facts fill
       every row: eleven facts in four tracks leave three cells of bare backing, which reads
       as a hole in the band. Borders plus `overflow-hidden` leave the surface showing. -->
  <dl
    :aria-label="label"
    :class="tracks"
    class="m-0 grid overflow-hidden rounded-(--ps-radius) border border-line bg-surface"
  >
    <div
      v-for="fact in facts"
      :key="fact.key"
      class="-mt-px -ml-px flex min-w-0 flex-col gap-1.5 border-t border-l border-line px-4 py-3.5"
    >
      <dt class="flex min-w-0 items-center gap-1 text-xs text-ink/55">
        <span class="truncate">{{ fact.label }}</span>
        <!-- A button and not a focusable span, and the same one `Statistic` draws: the
             tooltip is the only place this sentence exists now, so the thing that opens it
             has to have a name and a focus ring of its own. -->
        <button
          v-if="fact.info"
          :aria-label="`About ${fact.label}`"
          class="flex size-4 flex-none items-center justify-center rounded-full text-ink/40 transition-colors duration-150 hover:text-ink/80 focus-visible:text-ink/80 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary"
          type="button"
        >
          <Icon :aria-hidden="true" class="text-sm" name="material-symbols:info-outline" />
          <v-tooltip
            :text="fact.info"
            activator="parent"
            location="top"
            max-width="260"
            open-delay="200"
          />
        </button>
      </dt>

      <dd class="m-0 flex min-w-0 items-center gap-2">
        <span
          v-if="fact.dot"
          :class="fact.dot"
          aria-hidden="true"
          class="size-1.5 flex-none rounded-full"
        />

        <NuxtLink
          v-if="fact.to"
          :to="fact.to"
          class="flex min-w-0 items-center gap-1 truncate text-sm font-medium text-ink no-underline transition-colors duration-150 hover:text-primary"
        >
          <span class="truncate">{{ fact.value }}</span>
          <Icon
            :aria-hidden="true"
            class="flex-none text-base text-ink/40"
            name="material-symbols:chevron-right"
          />
        </NuxtLink>

        <span
          v-else
          :class="fact.mono ? 'font-mono text-[13px]' : 'text-sm'"
          class="min-w-0 truncate font-medium text-ink"
        >
          {{ fact.value }}
        </span>

        <!-- The label as written, not lowercased: every value worth copying here is an
             identifier or an endpoint, so the label is an acronym as often as not. -->
        <CopyButton v-if="fact.copy" :label="`Copy the ${fact.label}`" :value="fact.copy" />
      </dd>

      <p v-if="fact.note" class="m-0 truncate text-xs leading-4 text-ink/45">
        {{ fact.note }}
      </p>
    </div>

    <!-- The empty slots that finish the last row, carrying the two borders their
         neighbours were missing and nothing else. `aria-hidden` because there is no fact
         here: a `<dl>` whose last entry is an empty pair reads as a term with no
         definition, and this is furniture. -->
    <div
      v-for="filler in fillers"
      :key="filler.key"
      :class="filler.klass"
      aria-hidden="true"
      class="-mt-px -ml-px border-t border-l border-line"
    />
  </dl>
</template>
