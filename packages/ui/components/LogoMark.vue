<script setup lang="ts">
withDefaults(defineProps<{ size?: number | string }>(), { size: 24 })
</script>

<template>
  <!-- assets/brand/mark.svg: the Aperture mark alone, cropped square to its bounds. -->
  <svg
    :width="size"
    :height="size"
    viewBox="92 92 328 328"
    xmlns="http://www.w3.org/2000/svg"
    class="text-neutral-950 dark:text-neutral-50"
    aria-hidden="true"
  >
    <g fill="currentColor">
      <path d="M196 196H300A104 104 0 0 0 196 92Z" />
      <path d="M196 196H300A104 104 0 0 0 196 92Z" transform="rotate(90 256 256)" />
      <path d="M196 196H300A104 104 0 0 0 196 92Z" transform="rotate(180 256 256)" />
      <path d="M196 196H300A104 104 0 0 0 196 92Z" transform="rotate(270 256 256)" />
    </g>
  </svg>
</template>
