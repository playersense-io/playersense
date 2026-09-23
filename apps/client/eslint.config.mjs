// @ts-check
import withNuxt from './.nuxt/eslint.config.mjs'
import betterTailwindcss from 'eslint-plugin-better-tailwindcss'

export default withNuxt(
  {
    rules: {
      'vue/no-multiple-template-root': 'off',
      'vue/max-attributes-per-line': ['error', { singleline: 3 }],
      // Vuetify's slots are named `item.<key>`, which the rule reads as modifiers.
      'vue/valid-v-slot': ['error', { allowModifiers: true }]
    }
  },
  betterTailwindcss.configs['correctness-error'],
  {
    rules: {
      // `ps-` hooks are styled by the dashboard layer's theme.css, not Tailwind.
      'better-tailwindcss/no-unknown-classes': ['error', { ignore: ['^ps-'] }]
    }
  },
  {
    settings: {
      'better-tailwindcss': {
        entryPoint: 'app/assets/css/main.css'
      }
    }
  }
)
