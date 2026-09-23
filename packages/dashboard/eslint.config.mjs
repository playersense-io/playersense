// @ts-check
// The apps lint through @nuxt/eslint's generated config; a layer has no .nuxt of its own,
// so it builds the same one directly with the same stylistic choices.
import { createConfigForNuxt } from '@nuxt/eslint-config/flat'
import betterTailwindcss from 'eslint-plugin-better-tailwindcss'

export default createConfigForNuxt({
  features: {
    typescript: true,
    stylistic: {
      commaDangle: 'never',
      braceStyle: '1tbs'
    }
  }
}).append(
  {
    rules: {
      'vue/no-multiple-template-root': 'off',
      'vue/max-attributes-per-line': ['error', { singleline: 3 }],
      // Vuetify's slots are named `item.<key>`, which the rule reads as modifiers.
      'vue/valid-v-slot': ['error', { allowModifiers: true }],
      // Tag, Statistic, Steps, Sparkline: the names idenzy's pages use them by.
      'vue/multi-word-component-names': 'off'
    }
  },
  betterTailwindcss.configs['correctness-error'],
  {
    rules: {
      // Components' own scoped classes (BEM, `block__element--modifier`) and the `ps-`
      // hooks theme.css styles are not Tailwind's to know about.
      'better-tailwindcss/no-unknown-classes': ['error', {
        ignore: ['^ps-', '__', '--', '^(action|data-table|tag|record|segmented|statistic|search-field|settings-tab|metric-chart|metric-key|metric-skeleton)$']
      }]
    }
  },
  {
    settings: {
      'better-tailwindcss': {
        entryPoint: 'app/assets/css/dashboard.css'
      }
    }
  }
)
