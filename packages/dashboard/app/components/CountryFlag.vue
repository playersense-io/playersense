<script setup lang="ts">
/**
 * A country's flag, as a small circle beside its name, for the places a country is a row
 * or a key: a cohort split by country, a segment in a table.
 *
 * Icons rather than the flag emoji, which Windows draws as two letters. Every name below
 * is written out in full because @nuxt/icon bundles what it finds in the source and draws
 * nothing for a name assembled at runtime; the list is the markets a game's installs
 * mostly come from, and a code outside it, or "Other", is the globe. Decorative beside a
 * name that says the country, so hidden from a screen reader.
 */
const props = withDefaults(defineProps<{
  /** ISO 3166-1 alpha-2, either case: "US", "jp". */
  code: string
  /** In pixels. */
  size?: number
}>(), {
  size: 14
})

const FLAGS: Record<string, string> = {
  ae: 'circle-flags:ae',
  ar: 'circle-flags:ar',
  at: 'circle-flags:at',
  au: 'circle-flags:au',
  be: 'circle-flags:be',
  br: 'circle-flags:br',
  ca: 'circle-flags:ca',
  ch: 'circle-flags:ch',
  cl: 'circle-flags:cl',
  cn: 'circle-flags:cn',
  co: 'circle-flags:co',
  de: 'circle-flags:de',
  dk: 'circle-flags:dk',
  eg: 'circle-flags:eg',
  es: 'circle-flags:es',
  fi: 'circle-flags:fi',
  fr: 'circle-flags:fr',
  gb: 'circle-flags:gb',
  hk: 'circle-flags:hk',
  id: 'circle-flags:id',
  ie: 'circle-flags:ie',
  il: 'circle-flags:il',
  in: 'circle-flags:in',
  it: 'circle-flags:it',
  jp: 'circle-flags:jp',
  kr: 'circle-flags:kr',
  mx: 'circle-flags:mx',
  my: 'circle-flags:my',
  ng: 'circle-flags:ng',
  nl: 'circle-flags:nl',
  no: 'circle-flags:no',
  nz: 'circle-flags:nz',
  pe: 'circle-flags:pe',
  ph: 'circle-flags:ph',
  pl: 'circle-flags:pl',
  pt: 'circle-flags:pt',
  ru: 'circle-flags:ru',
  sa: 'circle-flags:sa',
  se: 'circle-flags:se',
  sg: 'circle-flags:sg',
  th: 'circle-flags:th',
  tr: 'circle-flags:tr',
  tw: 'circle-flags:tw',
  ua: 'circle-flags:ua',
  us: 'circle-flags:us',
  vn: 'circle-flags:vn',
  za: 'circle-flags:za'
}

const icon = computed(() => FLAGS[props.code.toLowerCase()])
</script>

<template>
  <Icon
    v-if="icon"
    :name="icon"
    :style="{ width: `${size}px`, height: `${size}px` }"
    aria-hidden="true"
    class="ps-country-flag"
  />
  <Icon
    v-else
    :style="{ width: `${size}px`, height: `${size}px` }"
    aria-hidden="true"
    class="ps-country-flag ps-country-flag--other"
    name="material-symbols:public"
  />
</template>

<style scoped>
/* A hairline ring, so a flag with white at its edge still reads as a circle on the surface. */
.ps-country-flag {
  border-radius: 999px;
  box-shadow: 0 0 0 1px rgba(var(--v-theme-on-surface), 0.08);
  flex: none;
  vertical-align: -2px;
}

.ps-country-flag--other {
  box-shadow: none;
  color: rgba(var(--v-theme-on-surface), 0.45);
}
</style>
