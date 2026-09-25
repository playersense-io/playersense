<script setup lang="ts">
// A member's role in the members list: a tag in the role's colour, the way a flag's kind is
// drawn in its list, that is also the control that changes it. It darkens and shows its
// chevron under the pointer; a click opens the organization's roles (GET …/roles), each with
// what it may do.
//
// The last owner cannot be made anything else: an organisation with no owner has nobody who
// can give the role back.
import type { RoleRecord } from '~/composables/members'

const props = defineProps<{
  /** The roles it can be, from the API. */
  roles: RoleRecord[]
  /** Whose role it is, for a screen reader: "Role of Sam Okafor". */
  label: string
  /** Why the role cannot change, when it cannot. */
  locked?: string
}>()

/** The role's key. */
const role = defineModel<string>({ required: true })

const open = ref(false)

const ordered = computed(() => sortRoles(props.roles))
const current = computed(() => roleLook(props.roles.find(entry => entry.key === role.value) ?? { key: role.value, name: role.value }))

function choose(next: string) {
  open.value = false
  if (next !== role.value) role.value = next
}
</script>

<template>
  <v-menu
    v-model="open"
    :disabled="Boolean(props.locked)"
    :offset="4"
    content-class="ps-field-menu"
    location="bottom start"
  >
    <template #activator="{ props: activator }">
      <button
        v-bind="activator"
        :aria-label="`${label}: ${current.label}`"
        :class="[open ? 'ps-role-tag--open' : '', locked ? 'cursor-default' : 'cursor-pointer']"
        :style="{ '--tag': `var(--v-theme-${current.tone})` }"
        :title="locked || undefined"
        class="ps-role-tag group inline-flex h-6 max-w-full items-center gap-0.5 rounded-full pr-1.5 pl-2.5 text-[11px] font-medium whitespace-nowrap transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        type="button"
      >
        <span class="truncate">{{ current.label }}</span>
        <Icon
          v-if="!locked"
          :class="open ? 'rotate-180 opacity-100' : 'opacity-50 group-hover:opacity-100'"
          aria-hidden="true"
          class="flex-none text-sm transition-[opacity,transform] duration-150"
          name="material-symbols:keyboard-arrow-down"
        />
        <Icon
          v-else
          aria-hidden="true"
          class="flex-none text-xs opacity-60"
          name="material-symbols:lock-outline"
        />
      </button>
    </template>

    <v-list class="w-80" nav>
      <v-list-subheader class="text-[11px]">
        Role
      </v-list-subheader>
      <v-list-item
        v-for="entry in ordered"
        :key="entry.key"
        :active="entry.key === role"
        lines="two"
        @click="choose(entry.key)"
      >
        <template #prepend>
          <span :style="{ background: `rgb(var(--v-theme-${roleLook(entry).tone}))` }" aria-hidden="true" class="mr-3 size-2 rounded-full" />
        </template>
        <v-list-item-title class="text-xs font-medium">
          {{ roleLook(entry).label }}
        </v-list-item-title>
        <v-list-item-subtitle class="text-[11px]">
          {{ roleLook(entry).note }}
        </v-list-item-subtitle>
        <template v-if="entry.key === role" #append>
          <Icon aria-hidden="true" class="text-base text-ink/70" name="material-symbols:check" />
        </template>
      </v-list-item>
    </v-list>
  </v-menu>
</template>

<style scoped>
/* Tag.vue's colours: the word in its tone, a step towards the ink, on a wash of the tone.
   The wash deepens under the pointer and while the menu is open, which is what says it can
   be clicked. */
.ps-role-tag {
  background: color-mix(in srgb, rgb(var(--tag)) 14%, transparent);
  color: color-mix(in srgb, rgb(var(--tag)) 60%, rgb(var(--v-theme-on-surface)));
}

.ps-role-tag:not(.cursor-default):hover,
.ps-role-tag--open {
  background: color-mix(in srgb, rgb(var(--tag)) 24%, transparent);
}
</style>
