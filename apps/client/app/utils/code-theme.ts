// The panel's own colours for highlighted code, as a Shiki theme whose every colour is a CSS
// variable: CodeBlock.vue sets the variables from the theme's palette, so one theme follows
// the light and the dark, and code reads as part of the panel rather than as GitHub's.
//
// Few colours on purpose. Keys and names are ink, strings the green slot, numbers the amber,
// booleans and types the violet, keywords the accent, comments and punctuation a quiet ink —
// a JSON document is mostly keys and values, and those two are what should stand apart.

const token = (name: string) => `var(--ps-code-${name})`

export const PANEL_CODE_THEME = {
  name: 'playersense',
  type: 'dark' as const,
  colors: {
    'editor.foreground': token('text'),
    'editor.background': 'transparent'
  },
  tokenColors: [
    { settings: { foreground: token('text') } },
    { scope: ['comment', 'punctuation.definition.comment'], settings: { foreground: token('comment'), fontStyle: 'italic' } },
    { scope: ['punctuation', 'meta.brace', 'keyword.operator', 'punctuation.separator', 'punctuation.definition.dictionary', 'punctuation.definition.array'], settings: { foreground: token('punctuation') } },
    { scope: ['string', 'punctuation.definition.string'], settings: { foreground: token('string') } },
    { scope: ['support.type.property-name', 'support.type.property-name.json', 'meta.object-literal.key'], settings: { foreground: token('key') } },
    { scope: ['constant.numeric'], settings: { foreground: token('number') } },
    { scope: ['constant.language', 'entity.name.type', 'support.class', 'entity.name.class', 'storage.type.primitive'], settings: { foreground: token('type') } },
    { scope: ['keyword', 'storage', 'storage.type', 'storage.modifier', 'keyword.control', 'keyword.other'], settings: { foreground: token('keyword') } },
    { scope: ['entity.name.function', 'support.function', 'entity.name.function.member'], settings: { foreground: token('function') } },
    { scope: ['variable.other', 'variable.parameter', 'punctuation.definition.variable'], settings: { foreground: token('variable') } },
    { scope: ['constant.other.option', 'entity.name.command', 'support.function.builtin'], settings: { foreground: token('keyword') } }
  ]
}

export const CODE_LANGUAGE_NAMES: Record<string, string> = {
  json: 'JSON',
  csharp: 'C#',
  shellscript: 'Shell'
}
