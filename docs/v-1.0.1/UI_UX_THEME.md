# UI/UX Theme & Design System

**Purpose**: you (the founder) said you're not confident in UI/UX. This file exists so you never have to make a visual judgment call again — every color, spacing, and layout decision below is fixed and justified. Any AI agent building a screen for this product **must** follow this file exactly. If a new screen isn't covered here, extend the existing tokens/patterns below rather than inventing new ones — do not introduce a new color, font, border-radius, or shadow style that isn't defined in this document.

---

## 1. What this is grounded in

Researched comparable restaurant POS/back-office products (Toast, Square for Restaurants, Lightspeed Restaurant, and several POS UI case studies) before setting these decisions. The consistent, real findings that shaped this system — not aesthetic preference:

- POS/back-office UI is judged on **speed and error-avoidance**, not decoration. Staff use it mid-shift, often on a phone or tablet, sometimes in bright or cramped conditions. Legacy systems that look "ugly" but are fast are preferred by daily users over pretty-but-slow ones.
- **Status color-coding is the single most load-bearing visual element** in this category — table status, order status, payment status. It has to be unambiguous and consistent everywhere or staff make costly mistakes (serving the wrong table, missing an unpaid order).
- Category-tabs + card-grid is the dominant, proven pattern for item/menu selection during order entry.
- This is a **back-office operations and finance tool for restaurant owners**, not a consumer food-ordering app — so it deliberately does *not* use the "warm/appetizing" orange-and-red palette most food apps default to. That palette is for making people hungry; this product is for making owners trust their numbers. That distinction is the core design decision everything else follows from.
- The target market is Bangladesh; menu items, restaurant names, and staff input will often be in Bangla. Typography must support Bangla and English with equal visual quality — this is a functional requirement, not a nice-to-have, and is treated as such below.

---

## 2. Color — fixed palette, do not add colors outside this list

```css
:root {
  /* Base */
  --ink:      #1E1C19;  /* primary text — warm near-black, not pure #000 */
  --ink-dim:  #6B6558;  /* secondary text, timestamps, helper copy */
  --paper:    #FBFAF7;  /* page background */
  --surface:  #FFFFFF;  /* cards, panels, table rows */
  --border:   #E3DFD5;  /* hairline dividers — the ONLY separator; no shadows on cards */

  /* Brand / primary actions */
  --brand:      #2B4570;  /* buttons, links, focus rings, active nav item */
  --brand-dim:  #1C2E4C;  /* hover/pressed state of brand */

  /* Status — each color means exactly ONE thing, everywhere, always.
     Never reuse a status color for decoration or branding. */
  --status-good:    #2F7A4D;  /* table: available · order: open & on-track · payment: paid */
  --status-warn:    #C97C2E;  /* table: occupied · order: in progress · subscription: past_due (pre-lock grace period) */
  --status-pending: #6B5FA8;  /* table: reserved · order: awaiting kitchen/payment confirmation */
  --status-danger:  #B23A2E;  /* account/restaurant: locked · payment: failed · destructive actions */
}
```

**Why these specific values, so no one "improves" them later**: the background/accent combination most AI design tools default to is a cream background (`#F4F1EA`) with a terracotta accent (near `#D97757`) — that pairing has become an instant tell for AI-generated design and was deliberately avoided here. `--paper` is a near-white, not cream. `--brand` is a deep blue, not a warm/orange tone, precisely because this is a finance/ops tool, not a food-appetite app (see §1).

**Rules**:
- `--brand` is reserved for actions and navigation — primary buttons, links, active states, focus rings. It never appears as a status indicator.
- Status colors are reserved for status — they never appear as branding, decoration, or on marketing/login screens.
- No gradients, anywhere.
- No drop shadows on cards or panels — `--border` is the only separator between surfaces. Reserve real shadows exclusively for things that visually float above the page: dropdown menus, modals, toasts.

---

## 3. Typography

| Role | Typeface | Why |
|---|---|---|
| UI text, body, labels | **IBM Plex Sans** (Latin) paired with **IBM Plex Sans Bengali** (Bangla) | Same type family designed to match across both scripts — Bangla and English text carry equal visual weight instead of Bangla falling back to a mismatched system font, which is the actual functional problem to solve here, not a stylistic pick. |
| Numbers — prices, quantities, invoice totals, report figures | **IBM Plex Mono**, tabular figures | Every number in this product is money, a count, or a table/order ID. Monospaced tabular figures keep columns of prices aligned in tables and invoices — a functional requirement, not a display flourish. |
| Headings | IBM Plex Sans, semibold/bold weight — same family as body, no separate display face | One family used deliberately across roles, per design guidance: distinguish through weight and size, not by introducing a third typeface. |

Type scale (rem, 16px base): `12 / 14 / 16 / 20 / 24 / 32` — labels/meta at 12–14, body at 16, section headings at 20–24, page titles/hero numbers (e.g. today's total sales on the dashboard) at 32. Line length for any paragraph content (rare in this product, but e.g. help text) stays under 80 characters.

**Never**: all-caps labels, letter-spaced "eyebrow" text above headings, middle-dot-joined meta strings ("Today · 12 orders · ৳4,500"), an em-dash-joined label format, or an arrow (→) appended to button/link text. These are the most common generic-AI tells and are explicitly excluded.

---

## 4. Layout

```
Desktop (owner/admin — reports, menu setup, settings, billing):
┌────────────┬──────────────────────────────────────┐
│  Sidebar   │  Page content                          │
│  (persist) │  Page title                            │
│  Tables    │  ─────────────────────────────────     │
│  Orders    │  [content: table / cards / form]       │
│  Menu      │                                         │
│  Reports   │                                         │
│  Invoices  │                                         │
│  Billing   │                                         │
└────────────┴──────────────────────────────────────┘

Phone/tablet (staff — order entry mid-shift):
┌──────────────────────────────┐
│ Category tabs (horiz scroll) │
├──────────────────────────────┤
│ [item] [item] [item]          │  <- card grid, image + name + price
│ [item] [item] [item]          │
├──────────────────────────────┤
│ Current order (bottom sheet)  │  <- always visible, running total
└──────────────────────────────┘
```

- **Sidebar navigation** (desktop, owner/admin surfaces) is persistent and fixed — Tables, Orders, Menu, Reports, Invoices, Billing. Do not redesign this per page; every back-office screen lives inside this shell.
- **Order entry** (the screen staff use most, under time pressure) is a category-tab + card-grid pattern, not a dense data table — this is the one proven pattern from POS research in §1. The current order and running total stay visible at all times (bottom sheet on mobile, side panel on desktop) so staff never lose track mid-order.
- **Tables view**: a grid of table cards, each colored by its status dot (`--status-good`/`--status-warn`/`--status-pending`) — color is the primary signal, read at a glance across a room, not text.
- **Reports/dashboard**: numbers-forward. Lead with the figure, not a chart — a big tabular number with a small label beneath it, sparingly used charts only where a trend genuinely needs showing (e.g. sales over the week), never a decorative chart.
- **Tables/lists**: text left-aligned, all numeric columns (price, quantity, totals) right-aligned using the tabular-figure numeric font from §3 — standard accounting convention, not a stylistic choice.
- **Border-radius**: one value platform-wide, `6px`. Not the rounded-everywhere "SaaS card kit" look (12–16px) — sharper, because this is a working tool, not a marketing page.
- **Touch targets**: minimum `44×44px` on any interactive element reachable on a phone/tablet — order buttons, quantity steppers, table cards. This is non-negotiable given staff use this mid-shift, often quickly.

---

## 5. Motion

Motion is used only to confirm an action already taken — adding an item to an order, saving a form, a status changing. No hover-fade transitions on cards, no page-load reveal animations, no scroll-triggered effects. A fast, silent interface beats a decorated one for this audience (§1).

---

## 6. Writing / copy in the interface

- Buttons name the exact action in active voice: "Save menu item," "Close order," "Lock restaurant" — never "Submit" or "OK."
- The vocabulary stays identical through a whole flow: if a button says "Close order," the resulting confirmation says "Order closed," never "Transaction completed."
- Empty states are an invitation to act, in the product's own voice: "No tables yet — add your first table," not "Oops, nothing here!"
- Errors state exactly what happened and how to fix it, without apologizing or being vague: "Payment failed — bKash declined the transaction. Try again or use a different number," not "Something went wrong."

---

## 7. Mapping to the schema (so status colors are never ambiguous)

| Schema field | Value | Color |
|---|---|---|
| `tables.status` | `available` | `--status-good` |
| `tables.status` | `occupied` | `--status-warn` |
| `tables.status` | `reserved` | `--status-pending` |
| `orders.status` | `open` | `--status-warn` (in progress) |
| `orders.status` | `closed` | `--status-good` |
| `orders.status` | `cancelled` | `--status-danger` |
| `payments.status` | `completed` | `--status-good` |
| `payments.status` | `pending` | `--status-warn` |
| `payments.status` | `failed` | `--status-danger` |
| `accounts.status` / `restaurants.status` | `active` | `--status-good` |
| `accounts.status` | `past_due` (grace period, not yet locked) | `--status-warn` |
| `accounts.status` | `locked` / `suspended` / `canceled` | `--status-danger` |
| `restaurants.status` | `locked` | `--status-danger` |
| `inventory_items` | `current_quantity > reorder_threshold` (or no threshold set) | `--status-good` |
| `inventory_items` | `current_quantity <= reorder_threshold` | `--status-warn` |
| `inventory_items` | `current_quantity <= 0` | `--status-danger` |

This table is the authoritative answer to "what color should this status be" — an agent should never guess or invent a new mapping.

---

## 8. Checklist for any new screen

Before shipping any new page or component, confirm:

- [ ] Uses only colors from §2 — no new hex values introduced
- [ ] Uses IBM Plex Sans / Plex Sans Bengali for text, IBM Plex Mono (tabular) for numbers
- [ ] No gradients, no card shadows (only true overlays may have a shadow)
- [ ] Border-radius is `6px`, platform-wide
- [ ] All interactive touch targets ≥ 44×44px
- [ ] Any status shown uses the exact mapping in §7 — not a color chosen ad hoc
- [ ] No hover-fade/reveal animation added — motion only confirms a completed action
- [ ] Copy uses active voice and matches the vocabulary already used elsewhere for the same action
