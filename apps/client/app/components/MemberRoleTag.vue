<script setup lang="ts">
// A member's role in the members list: a tag in the role's colour, the way a flag's kind is
// drawn in its list, that is also the control that changes it. It darkens and shows its
// chevron under the pointer; a click opens the roles, each with what it may do, taken from
// the role templates (apps/backend/internal/authz/roles.go).
//
// The last owner cannot be made anything else: an organisation with no owner has nobody who
// can give the role back.
import type { MemberRole } from '~/types'

const props = defineProps<{
  /** Whose role it is, for a screen reader: "Role of Sam Okafor". */
  label: string
  /** Why the role cannot change, when it cannot. */
  locked?: string
}>()

const role = defineModel<MemberRole>({ required: true })

const open = ref(false)

function choose(next: MemberRole) {
  role.value = next
  open.value = false
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
        :aria-label="`${label}: ${MEMBER_ROLES[role].label}`"
        :class="[open ? 'ps-role-tag--open' : '', locked ? 'cursor-default' : 'cursor-pointer']"
        :style="{ '--tag': `var(--v-theme-${MEMBER_ROLES[role].tone})` }"
        :title="locked || undefined"
        class="ps-role-tag group inline-flex h-6 max-w-full items-center gap-0.5 rounded-full pr-1.5 pl-2.5 text-[11px] font-medium whitespace-nowrap transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        type="button"
      >
        <span class="truncate">{{ MEMBER_ROLES[role].label }}</span>
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
        v-for="key in MEMBER_ROLE_ORDER"
        :key="key"
        :active="key === role"
        lines="two"
        @click="choose(key)"
      >
        <template #prepend>
          <span :style="{ background: `rgb(var(--v-theme-${MEMBER_ROLES[key].tone}))` }" aria-hidden="true" class="mr-3 size-2 rounded-full" />
        </template>
        <v-list-item-title class="text-xs font-medium">
          {{ MEMBER_ROLES[key].label }}
        </v-list-item-title>
        <v-list-item-subtitle class="text-[11px]">
          {{ MEMBER_ROLES[key].note }}
        </v-list-item-subtitle>
        <template v-if="key === role" #append>
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
