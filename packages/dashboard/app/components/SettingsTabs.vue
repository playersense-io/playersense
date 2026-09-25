<script setup lang="ts">
import type { SettingsTab } from '../utils/components'
/**
 * The settings rail: a column of destinations beside the screen they open.
 *
 * Links and not tabs, in the markup and in what a screen reader is told. Each one
 * changes the URL, so this is a `nav` of `NuxtLink`s marked with `aria-current`, never
 * `role="tab"`: tab semantics promise a panel that swaps in place, and announcing a
 * navigation as a tab leaves somebody waiting for content that has in fact replaced the
 * page. It also means every tab is linkable, which is the reason the routes are nested
 * rather than a query parameter.
 *
 * Each row carries the scope of what is behind it, because that is the one thing these
 * three screens are most often wrong about: the project's settings, the organisation's
 * team and the reader's own account sit next to each other and a mutation on the wrong
 * one is invisible until afterwards.
 *
 * Vertical from `lg` up and a scrolling strip below it, rather than a narrower column:
 * the layout blueprint asks for stacking rather than shrinking under the desktop
 * breakpoint, and a 120px rail would truncate every label on it.
 */
defineProps<{
  tabs: readonly SettingsTab[]
  /** Names the rail for a screen reader: "Settings". */
  label: string
}>()

const labelId = `${useId()}-rail`
</script>

<template>
  <nav :aria-labelledby="labelId" class="min-w-0">
    <p :id="labelId" class="m-0 hidden px-3 pb-2 text-[11px] font-medium text-ink/45 lg:block">
      {{ label }}
    </p>

    <!--
      One list, two arrangements. Below `lg` it scrolls sideways with the rows shoulder to
      shoulder; from `lg` it is the column. `scrollbar-none` is not used: a strip that can
      scroll should look like it can.
    -->
    <ul
      class="m-0 flex list-none gap-1 overflow-x-auto p-0 pb-1 lg:flex-col lg:overflow-visible lg:pb-0"
    >
      <li v-for="tab in tabs" :key="tab.to" class="flex-none lg:w-full">
        <NuxtLink
          :to="tab.to"
          class="settings-tab group flex items-center gap-2.5 rounded-(--ps-radius-small) px-3 text-ink/70 no-underline transition-colors duration-150 hover:bg-ink/5 hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary lg:items-start lg:py-2"
        >
          <Icon
            :name="tab.icon"
            :aria-hidden="true"
            class="flex-none text-lg text-ink/55 transition-colors group-hover:text-ink/80 lg:mt-px"
          />
          <span class="flex min-w-0 flex-col">
            <span class="truncate text-[13px] font-medium whitespace-nowrap">{{ tab.title }}</span>
            <!-- The scope is desktop only: on a strip there is no room for a second line,
                 and each screen states its own scope in its header anyway. -->
            <span v-if="tab.scope" class="hidden text-[11px] leading-4 text-ink/50 lg:block">
              {{ tab.scope }}
            </span>
          </span>
        </NuxtLink>
      </li>
    </ul>
  </nav>
</template>

<style scoped>
/*
 * The row's height is the drawer's, so the rail and the navigation beside it read as one
 * family. On the strip it is a control and centres; in the column it holds two lines and
 * sizes to them.
 */
.settings-tab {
  min-height: var(--ps-nav-item-height);
}

/*
 * The current screen. `router-link-active` is not used: it matches a parent path too, so
 * `/settings` would light while `/settings/members` is open. `exact-active` is the only
 * one of the two that answers the question the rail is asking.
 */
.settings-tab.router-link-exact-active {
  background: rgba(var(--v-theme-on-surface), 0.06);
  color: rgb(var(--v-theme-on-surface));
  font-weight: 500;
}

.settings-tab.router-link-exact-active :deep(svg) {
  color: rgb(var(--v-theme-on-surface));
}
</style>
