export default defineAppConfig({
  ui: {
    colors: {
      // Monochrome for now — neutral-950 ink in light mode, neutral-50 in
      // dark mode, no separate accent hue yet. Swap `primary` to a real
      // brand color once the reference design system lands.
      primary: 'neutral',
      neutral: 'neutral'
    }
  }
})
