<script setup lang="ts">
// The bell in the app bar and the menu it opens. Nothing sends notifications yet, so the
// menu says so rather than showing an empty box; `count` lights the dot once something does.
withDefaults(defineProps<{
  count?: number
}>(), {
  count: 0
})
</script>

<template>
  <v-btn
    :aria-label="count ? `Notifications, ${count} unread` : 'Notifications'"
    class="
      relative text-ink/70
      hover:text-ink
    "
    icon
    size="small"
    variant="text"
  >
    <Icon :aria-hidden="true" class="text-lg" name="material-symbols:notifications-outline" />
    <span
      v-if="count"
      aria-hidden="true"
      class="absolute top-1.5 right-1.5 size-1.5 rounded-full bg-primary"
    />
    <v-tooltip activator="parent" location="bottom" text="Notifications" />

    <v-menu :offset="6" activator="parent" location="bottom end">
      <v-list min-width="288" nav>
        <p class="truncate px-3 pt-1 pb-2 text-xs text-ink/60">
          Notifications
        </p>
        <p class="m-0 px-3 py-6 text-center text-xs text-ink/55">
          Nothing to show yet.
        </p>
      </v-list>
    </v-menu>
  </v-btn>
</template>
