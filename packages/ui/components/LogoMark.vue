<script setup lang="ts">
withDefaults(defineProps<{ size?: number | string }>(), { size: 24 })
</script>

<template>
  <!-- assets/brand/mark.svg: the mark alone, cropped square to its bounds. -->
  <svg
    :width="size"
    :height="size"
    viewBox="108 108 296 296"
    xmlns="http://www.w3.org/2000/svg"
    class="text-neutral-950 dark:text-neutral-50"
    aria-hidden="true"
  >
    <g fill="currentColor">
      <path d="M108 352V292A20 20 0 0 1 148 292V352Z" />
      <path d="M172 352V212A20 20 0 0 1 212 212V352Z" />
      <path d="M236 352V132A20 20 0 0 1 276 132V352Z" />
      <path d="M300 352V212A20 20 0 0 1 340 212V352Z" />
      <path d="M364 352V292A20 20 0 0 1 404 292V352Z" />
      <rect x="108" y="376" width="296" height="24" rx="12" />
    </g>
  </svg>
</template>
