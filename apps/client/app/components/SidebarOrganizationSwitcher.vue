<script setup lang="ts">
// The organization the dashboard is showing, and the menu that changes it. Above the
// navigation because every screen under it is scoped to it. The mark is an initial on the
// accent rather than a logo: an organization is a tenant, not a brand we hold an image for.
//
// Placeholder until control-api's /organizations + project-access list are wired in —
// this becomes the real organization/project switcher (§55).
const props = defineProps<{
  organization: string
  /** Folded to the mark alone, with the name in a tooltip. */
  rail: boolean
}>()

const organizations = computed(() => [props.organization])

function initial(name: string): string {
  return name.trim().charAt(0).toUpperCase()
}
</script>

<template>
  <!-- Padding and gap put the mark's centre on the same line as the glyphs in the list
       below it. Folded, the button is the rail's width and centres. -->
  <button
    :aria-label="rail ? `Organization: ${organization}` : undefined"
    :class="rail ? 'justify-center' : 'gap-2.5 px-2.5'"
    class="
      group flex h-9 w-full items-center overflow-hidden rounded-(--ps-radius-small) bg-ink/4
      text-ink transition-[background-color,transform]
      hover:bg-ink/6
      focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary
      active:scale-[0.99]
    "
    type="button"
  >
    <span
      aria-hidden="true"
      class="
        flex size-5 flex-none items-center justify-center rounded-md bg-primary text-[10px]
        font-semibold text-on-primary
      "
    >
      {{ initial(organization) }}
    </span>

    <template v-if="!rail">
      <span class="min-w-0 flex-1 truncate text-left text-[13px] font-medium">
        {{ organization }}
      </span>
      <Icon
        aria-hidden="true"
        class="
          flex-none text-base text-ink/50 transition-colors
          group-hover:text-ink/80
        "
        name="material-symbols:unfold-more"
      />
    </template>

    <v-tooltip
      v-if="rail"
      :text="organization"
      activator="parent"
      location="end"
      open-delay="300"
    />

    <v-menu :offset="6" activator="parent" location="bottom start">
      <v-list min-width="232" nav>
        <p class="truncate px-3 pt-1 pb-2 text-xs text-ink/60">
          Organizations
        </p>
        <v-list-item
          v-for="name in organizations"
          :key="name"
          :active="name === organization"
          :title="name"
        >
          <template #prepend>
            <span
              aria-hidden="true"
              class="
                flex size-5 items-center justify-center rounded-md bg-primary text-[10px]
                font-semibold text-on-primary
              "
            >
              {{ initial(name) }}
            </span>
          </template>
          <template v-if="name === organization" #append>
            <Icon aria-hidden="true" class="text-base text-ink/70" name="material-symbols:check" />
          </template>
        </v-list-item>
      </v-list>
    </v-menu>
  </button>
</template>
