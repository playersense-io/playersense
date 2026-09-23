<script setup lang="ts">
// The one control that changes the theme, in the app bar because it is a preference
// somebody changes for the room they are in, not for the account. The glyph is the theme
// the press lands on, not the one being left, and the aria-label says the same in words.
const { isDark, toggle } = useColorScheme()
</script>

<template>
  <v-btn
    :aria-label="isDark ? 'Switch to light mode' : 'Switch to dark mode'"
    class="
      text-ink/70
      hover:text-ink
    "
    icon
    size="small"
    variant="text"
    @click="toggle()"
  >
    <Icon
      :name="isDark ? 'material-symbols:sunny-outline-rounded' : 'material-symbols:moon-stars-outline-rounded'"
      aria-hidden="true"
      class="text-lg"
    />
    <v-tooltip :text="isDark ? 'Light mode' : 'Dark mode'" activator="parent" location="bottom" />
  </v-btn>
</template>
