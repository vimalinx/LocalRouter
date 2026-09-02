# Protocol Docs Page Overrides

> **PROJECT:** LocalRouter Console
> **Generated:** 2026-08-30 09:03:17
> **Page Type:** Dashboard / Data View

> ⚠️ **IMPORTANT:** Rules in this file **override** the Master file (`design-system/MASTER.md`).
> Only deviations from the Master are documented here. For all other rules, refer to the Master.

---

## Page-Specific Rules

### Layout Overrides

- **Max Width:** 1400px or full-width
- **Grid:** 12-column grid for data flexibility

### Spacing Overrides

- **Content Density:** High — optimize for information display

### Typography Overrides

- No overrides — use Master typography

### Color Overrides

- No overrides — use Master colors

### Component Overrides

- Avoid: Use a clickable div or reveal the only action on hover
- Avoid: Icon buttons without labels
- Avoid: Depend on animationend or transitionend for required state correctness

---

## Page-Specific Components

- No unique components for this page

---

## Recommendations

- Effects: Hover tooltips, chart zoom on click, row highlighting on hover, smooth filter animations, data loading spinners
- Accessibility: Prefer a button and expose pressed or selected state that matches the visible label
- Accessibility: Add aria-label for icon-only buttons
- Animation: Cancel or replace prior motion; set the final semantic state directly and handle cancellation cleanup
