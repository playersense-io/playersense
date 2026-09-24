<script setup lang="ts">
// A block of code in the panel's own colours: a compiled document, a snapshot, a call in C#.
// A band on top names the language and copies the code; the lines are numbered, and scroll
// sideways rather than wrap, since a wrapped line of JSON reads as two.
//
// Highlighted by Shiki with PANEL_CODE_THEME (utils/code-theme.ts), whose colours are the
// variables in assets/css/code.css, set from the theme's palette for light and dark. The
// highlighter loads on first use; until it has, the same text is shown plain, so the block
// never waits to be read.
const props = withDefaults(defineProps<{
  code: string
  lang?: 'json' | 'csharp' | 'shellscript' | 'javascript' | 'python' | 'go'
  /** The band's label; the language's name when absent. */
  title?: string
  lineNumbers?: boolean
}>(), {
  lang: 'json',
  title: '',
  lineNumbers: true
})

const html = ref('')

watchEffect(async () => {
  const { code, lang } = props
  const highlighter = await getShikiHighlighter()
  if (!highlighter.getLoadedThemes().includes(PANEL_CODE_THEME.name)) await highlighter.loadTheme(PANEL_CODE_THEME)
  html.value = highlighter.codeToHtml(code, { lang, theme: PANEL_CODE_THEME.name })
})

const plain = computed(() => props.code.split('\n'))
</script>

<template>
  <div :class="lineNumbers ? 'ps-code-block--numbered' : ''" class="ps-code-block flex min-w-0 flex-col overflow-hidden rounded-(--ps-radius-large) border border-line">
    <header class="flex h-9 flex-none items-center justify-between gap-2 border-b border-line bg-ink/3 pr-1.5 pl-3">
      <span class="text-[11px] font-medium text-ink/55">{{ title || CODE_LANGUAGE_NAMES[lang] }}</span>
      <CopyButton :value="code" label="Copy the code" />
    </header>
    <div class="min-h-0 flex-1 overflow-auto">
      <div v-if="html" :innerHTML="html" />
      <pre v-else><code><span v-for="(line, index) in plain" :key="index" class="ps-code-line">{{ line }}
</span></code></pre>
    </div>
  </div>
</template>
