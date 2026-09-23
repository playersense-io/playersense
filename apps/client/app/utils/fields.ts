// What a select, a combobox or an autocomplete under an outlined field is given so its
// dropdown is the panel's own menu — the rounded tray, the inset and the nav row — rather
// than Vuetify's bare list (theme.css, "A dropdown under an outlined field").
//
//   <v-select v-bind="FIELD_MENU" item-props variant="outlined" />
//
// Sizes: a field in a dialog or a settings card states no density and is 40px; a field in
// a dense row — a rule's condition, the player preview — is `comfortable`, the 36px step.
// `compact` is 24px, a chip's height, and never a field's.

export const FIELD_MENU = {
  menuProps: { contentClass: 'ps-field-menu', offset: 4 },
  listProps: { nav: true }
}
