<script setup lang="ts">
// A channel type's mark: the brand's own, in its colours, on a plain tile for Slack, Discord,
// Teams and Telegram; the glyph in ink for email, PagerDuty and a webhook, which have none
// that belongs to us to show.
const props = withDefaults(defineProps<{
  type: ChannelType
  size?: 'small' | 'default' | 'large'
}>(), {
  size: 'default'
})

const info = computed(() => CHANNEL_TYPES[props.type])

const SIZES = {
  small: 'size-6 text-sm',
  default: 'size-8 text-base',
  large: 'size-10 text-xl'
} as const
</script>

<template>
  <span
    :class="[SIZES[size], info.brand ? 'border border-line bg-surface' : 'bg-ink/5 text-ink/65']"
    :title="info.label"
    aria-hidden="true"
    class="flex flex-none items-center justify-center rounded-(--ps-radius)"
  >
    <Icon :name="info.icon" />
  </span>
</template>
