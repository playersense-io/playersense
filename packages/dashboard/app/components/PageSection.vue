<script setup lang="ts">
/**
 * A part of a page: a title, a sentence saying what it holds, and the way out to the
 * screen that owns it. No frame.
 *
 * A card is a thing laid on the page, and five of them in a column say that five
 * unrelated things happen to be here. A detail page is one subject read top to bottom, so
 * what parts these is a rule and a heading, and what makes each one distinguishable is the
 * shape of its own content: a table looks like a table, a feed like a feed, a list of
 * settings like a list of settings.
 *
 * `foldable` is for the part nobody reads twice. A folded section has to say what is in it
 * while it is shut, which is what `summary` is: "1 year · on declared columns" answers the
 * question the heading raises, and a reader who wanted the reasoning opens it. A fold that
 * hides its own values is a fold somebody has to open every visit.
 */
withDefaults(
  defineProps<{
    title: string
    description?: string
    /** The screen that owns this subject in full. A text link, never a button. */
    to?: string
    action?: string
    /** Drawn as a rule above, which every section but the first wants. */
    divided?: boolean
    foldable?: boolean
    /** The current values, read while the section is shut. Only with `foldable`. */
    summary?: string
  }>(),
  { description: '', to: '', action: '', divided: true, foldable: false, summary: '' }
)

const open = defineModel<boolean>('open', { default: true })

const panelId = useId()
</script>

<template>
  <section :class="divided ? 'border-t border-line pt-6' : ''" class="flex flex-col gap-4">
    <div class="flex flex-wrap items-start justify-between gap-x-6 gap-y-1">
      <component
        :is="foldable ? 'button' : 'div'"
        v-bind="
          foldable
            ? { 'type': 'button', 'aria-controls': panelId, 'aria-expanded': open }
            : {}
        "
        :class="foldable ? 'group cursor-pointer text-left' : ''"
        class="flex min-w-0 flex-col gap-1"
        @click="foldable ? (open = !open) : undefined"
      >
        <h2 class="m-0 flex items-center gap-1.5 text-sm font-medium text-ink">
          {{ title }}
          <Icon
            v-if="foldable"
            :class="open ? 'rotate-180' : ''"
            :aria-hidden="true"
            class="text-base text-ink/45 transition-transform duration-200 group-hover:text-ink"
            name="material-symbols:keyboard-arrow-down"
          />
        </h2>
        <p
          v-if="foldable && !open && summary"
          class="m-0 text-xs leading-5 text-ink/55"
        >
          {{ summary }}
        </p>
        <p
          v-else-if="description || $slots.description"
          class="m-0 text-xs leading-5 text-ink/55"
        >
          <slot name="description">
            {{ description }}
          </slot>
        </p>
      </component>

      <div v-if="to || $slots.actions" class="flex flex-none items-center gap-3">
        <slot name="actions" />
        <NuxtLink
          v-if="to"
          :to="to"
          class="inline-flex items-center gap-0.5 text-xs font-medium text-ink/55 no-underline transition-colors duration-150 hover:text-ink"
        >
          {{ action }}
          <Icon :aria-hidden="true" class="text-sm" name="material-symbols:chevron-right" />
        </NuxtLink>
      </div>
    </div>

    <!-- The same fold as `LayerCard`: a grid row from 0fr to 1fr, so the height animates
         without anybody measuring it, and `inert` so a shut section holds no tab stop. -->
    <div
      v-if="foldable"
      :id="panelId"
      :class="open ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]'"
      class="grid transition-[grid-template-rows] duration-200 ease-out"
    >
      <div :inert="!open" class="min-h-0 overflow-hidden">
        <div class="flex flex-col gap-4">
          <slot />
        </div>
      </div>
    </div>

    <slot v-else />
  </section>
</template>
