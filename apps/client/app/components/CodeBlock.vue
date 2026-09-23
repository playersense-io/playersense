<script setup lang="ts">
// A block of code, highlighted by Shiki: a compiled document, a snapshot, a call in C#. The
// highlighter loads on first use, so the block has its own Suspense and shows the plain text
// until it is ready — the code is readable from the first frame and only gains colour.
//
// Shiki writes both themes into each token as CSS variables; which one shows follows the
// panel's own light or dark (the `.v-theme--dark` class Vuetify sets), not the system's.
withDefaults(defineProps<{
  code: string
  lang?: 'json' | 'csharp' | 'shellscript'
}>(), {
  lang: 'json'
})
</script>

<template>
  <div class="ps-code-block overflow-auto rounded-(--ps-radius) bg-ink/4 p-4 font-mono text-[11px] leading-[18px]">
    <Suspense>
      <Shiki :code="code" :lang="lang" />
      <template #fallback>
        <pre class="m-0 text-ink/80">{{ code }}</pre>
      </template>
    </Suspense>
  </div>
</template>

<style>
/* Unscoped: the markup inside is Shiki's, and the dark rule hangs off Vuetify's theme class
   high above this component. Every rule is under `.ps-code-block`. */
.ps-code-block pre {
  margin: 0;
  background: transparent !important;
  font-family: var(--font-mono);
}

.ps-code-block .shiki,
.ps-code-block .shiki span {
  background-color: transparent !important;
}

.v-theme--dark .ps-code-block .shiki,
.v-theme--dark .ps-code-block .shiki span {
  color: var(--shiki-dark) !important;
}
</style>
