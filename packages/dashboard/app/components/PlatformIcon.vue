<script setup lang="ts">
/**
 * A platform's mark beside its name: Apple's for iOS, iPadOS and macOS, Android's robot,
 * Steam's, a globe for the web, and a generic device for anything else.
 *
 * The Apple mark and the globe are drawn in the ink, so they are dark on the light theme
 * and light on the dark one; Android and Steam keep their own colours, which read on both.
 * Names are written out in full so @nuxt/icon bundles them. Decorative beside a name that
 * says the platform, so hidden from a screen reader.
 */
const props = withDefaults(defineProps<{
  /** The platform as the SDK reports it, any case: "iOS", "android", "Steam", "web". */
  platform: string
  /** In pixels. */
  size?: number
}>(), {
  size: 14
})

const PLATFORMS: Record<string, { icon: string, ink: boolean }> = {
  ios: { icon: 'mage:apple', ink: true },
  ipados: { icon: 'mage:apple', ink: true },
  macos: { icon: 'mage:apple', ink: true },
  android: { icon: 'material-icon-theme:android', ink: false },
  steam: { icon: 'selfhst:steam', ink: false },
  web: { icon: 'mdi:web', ink: true }
}

const mark = computed(() => PLATFORMS[props.platform.toLowerCase()] ?? { icon: 'material-symbols:devices-outline', ink: true })
</script>

<template>
  <Icon
    :class="mark.ink ? 'ps-platform-icon--ink' : ''"
    :name="mark.icon"
    :style="{ width: `${size}px`, height: `${size}px` }"
    aria-hidden="true"
    class="ps-platform-icon"
  />
</template>

<style scoped>
.ps-platform-icon {
  flex: none;
  vertical-align: -2px;
}

/* The theme's ink, so a mark with no colour of its own follows light and dark. */
.ps-platform-icon--ink {
  color: rgb(var(--v-theme-on-surface));
}
</style>
