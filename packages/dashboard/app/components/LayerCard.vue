<script setup lang="ts">
/**
 * A card set in a tray: an outer frame in the page's own tone, an optional strip along
 * its top for a title and controls, and the content on a surface panel inset by the
 * frame's padding. The two edges and the step in tone are what make the panel read as
 * laid on the page rather than cut out of it, and the strip reads as part of the tray
 * and not of the panel, so a section's name sits above its content instead of inside.
 *
 * `padded` is off for content that is its own layout, a table or an empty state, and
 * on for prose and forms. `badge` sits beside the title, for a state the section is in;
 * `actions` sits at the strip's far end, for the controls that change it.
 *
 * `expandable` makes the strip the panel's toggle: the title becomes a button, the
 * strip takes the click except where a control in it was the target, and a chevron on
 * the right says which way and lights under the pointer, which is the whole of the
 * hover: a wash over the strip read as a second surface. `open` is a model, so a
 * screen decides which of its cards start unfolded.
 */
const props = withDefaults(
  defineProps<{
    title?: string
    description?: string
    padded?: boolean
    expandable?: boolean
  }>(),
  { title: '', description: '', padded: true, expandable: false }
)

const open = defineModel<boolean>('open', { default: true })

const panelId = `${useId()}-panel`
const folded = computed(() => props.expandable && !open.value)

function toggle() {
  if (props.expandable) open.value = !open.value
}

/* A control in the strip keeps its click; anywhere else on the strip is the toggle. */
function onStripClick(event: MouseEvent) {
  if (!props.expandable) return
  const target = event.target
  if (target instanceof Element && target.closest('[data-card-actions], button, a, input, label')) {
    return
  }
  toggle()
}
</script>

<template>
  <section class="rounded-(--ps-radius-large) border border-line bg-canvas p-1">
    <header
      v-if="title || $slots.header || $slots.actions"
      :class="expandable ? 'group cursor-pointer select-none' : ''"
      class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 px-3 py-2"
      @click="onStripClick"
    >
      <div class="flex min-w-0 flex-1 flex-col gap-0.5">
        <slot name="header">
          <div class="flex min-w-0 flex-wrap items-center gap-2">
            <h2 class="m-0 text-sm font-medium text-ink">
              <button
                v-if="expandable"
                :aria-controls="panelId"
                :aria-expanded="open"
                class="cursor-pointer appearance-none rounded-(--ps-radius-x-small) border-0 bg-transparent p-0 text-left text-sm font-medium text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
                type="button"
                @click="toggle"
              >
                {{ title }}
              </button>
              <template v-else>
                {{ title }}
              </template>
            </h2>
            <slot name="badge" />
          </div>
        </slot>
        <p v-if="description" class="m-0 text-xs text-ink/55">
          {{ description }}
        </p>
      </div>
      <div v-if="$slots.actions" class="flex flex-wrap items-center gap-2" data-card-actions>
        <slot name="actions" />
      </div>
      <span
        v-if="expandable"
        aria-hidden="true"
        class="-my-1 -mr-1.5 inline-flex size-7 flex-none items-center justify-center rounded-full text-ink/50 transition-colors duration-150 group-hover:bg-ink/8 group-hover:text-ink"
      >
        <Icon
          :class="open ? 'rotate-180' : ''"
          class="text-lg transition-transform duration-200"
          name="material-symbols:keyboard-arrow-down"
        />
      </span>
    </header>

    <!--
      The fold is a grid row going from one fraction to none, so the gap above the panel
      folds with it and nothing is measured; a height animation cannot reach zero past
      its own padding and snaps at the end.
    -->
    <div
      :id="panelId"
      :aria-hidden="folded || undefined"
      :class="folded ? 'grid-rows-[0fr]' : 'grid-rows-[1fr]'"
      class="grid transition-[grid-template-rows] duration-200 ease-out"
    >
      <div :inert="folded" class="min-h-0 overflow-hidden">
        <div :class="title || $slots.header || $slots.actions ? 'pt-1.5' : ''">
          <div
            :class="padded ? 'p-5' : ''"
            class="overflow-hidden rounded-(--ps-radius) border border-line bg-surface"
          >
            <slot />
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
