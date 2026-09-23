/**
 * How far a figure shrinks to fit its row, from the width it wants, the width the row
 * has, and what wants to sit beside it. Pure, so the decision is tested without a layout
 * engine, and because a background tab never delivers a ResizeObserver callback, which
 * makes the layout the one place this cannot be checked.
 */

/**
 * Half size is as far as a figure goes. Below that it is unreadable, so clipping is no
 * worse, and a floor is what keeps a row measured at zero width (hidden, or not yet laid
 * out) from setting the font size to zero and measuring nothing forever after.
 */
export const FIT_FLOOR = 0.5

/**
 * The row wraps, so the aside sits beside the figure when both fit at full size and drops
 * under it otherwise, and the figure shrinks only when it is too wide for the row on its
 * own. Decided from the same widths the flex layout uses, so the two never disagree about
 * which line the aside is on. Text width is linear in font size, which is what makes the
 * answer a ratio.
 */
export function fitScale(natural: number, available: number, beside = 0): number {
  if (!(natural > 0)) return 1
  const room = natural + beside <= available ? available - beside : available
  return Math.min(1, Math.max(FIT_FLOOR, room / natural))
}
