OpenBekci is open-source residential community management: one system for the residents, owners, boards, managers and staff of an apartment building or a gated site. The name is Turkish — a *bekçi* is the neighbourhood watchman, the person who knows every door on the street. The identity is built on that one image: **an open door.**

## The idea

The mark is a door frame and its door, in the same terracotta, with the door swung *toward you*. What tells you it is open is only the dark gap behind it. Read it three ways and all three are the brand:

- The door is open: the software is open source and the project is welcoming.
- Someone is watching the door: the watchman, security, a gate that is managed.
- The frame is a lowercase **n**-silhouette, a building standing on the ground: apartments, sites, homes.

Two colours carry everything. **Tile** (`tile`) is the terracotta of roof tiles over every Turkish site. **Night** (`night`) is the blue-slate of the street after dark, the watchman's hour. Everything else is paper, ink and a handful of status colours.

## Voice

OpenBekci speaks like a good site manager: plain, exact, calm. Money and rules are stated, never dressed up.

- Say what happened and what it costs. "Aidat 1.250,00 ₺ · son ödeme 15 Kasım" beats "Your monthly contribution is now available".
- Sentence case everywhere: headings, buttons, labels, menu items. No title case, no uppercase labels.
- "You" for the person, the plain name for things: "your unit", "the board", "the site". Never "we" in the product; in the docs, "we" means the project.
- No exclamation marks, no emoji, no marketing adjectives inside the app. One exclamation mark per marketing page is already one too many.
- Turkish and English are both first languages: copy is written to translate well, with no idioms and no puns, and every string leaves room to grow by 30%. Dates and amounts follow the site's locale, never the browser's.
- Errors say what to do next: "Payment date cannot be in the future. Pick today or earlier."

## Colour

Palette: **Kiremit** is the brand (`tile` `#bd4e27` light / `#ee8a62` dark; `night` `#14284a` / `#bcd0ee`; `surface` `#fbf6f0` / `#181513`; `ink` `#1a2433` / `#f3ede6`). **Taş** is a second, selectable look (web `data-look="tas"`, mobile profile toggle): `tile` `#a8573e` / `#dc9378`, `surface` `#f6f5f3` / `#1e1d1b`; its values live in `web/docs/palettes/tokens-C.json`. `brand/tokens.json` holds Kiremit only.

Light is the primary theme; the dark theme is a true dark, not an inversion, and every pair below is checked in both.

- Grounds: `surface` for the page, `surface-raised` for cards, dialogs and the sidebar, `surface-sunken` for table heads and wells. Separate them with `line` at `stroke-hairline` and the lightness step — not with shadows. `shadow-popover` and `shadow-dialog` exist only for things that float.
- Text: `ink` for content, `ink-muted` for captions, helper text and table headers, `ink-faint` only for placeholders and disabled text. Never set body text in `tile` or `night` over a tinted ground.
- Brand: `tile` is the colour of action — primary buttons, links, the active navigation item, the selected tab, the focused side of a switch. `tile-deep` is its hover and pressed state. Text on a `tile` fill is `on-tile`, never `#fff`: in the dark theme `tile` lightens and `on-tile` turns to `night-deep`. `tile-tint` is the quiet brand ground for selected rows and the active sidebar item; `tile-wash` is for illustration and the cover, never under small text.
- Night: `night` is the brand's dark — the app-icon tile, hero and footer grounds, the primary button in light when a screen already has a lot of terracotta (one rule per screen: either `tile` or `night` buttons, not both). Text on it is `on-night`.
- Status: `success`, `warning`, `danger`, `info` each with a `-tint` ground. A status is always a colour **and** a word or icon — a paid badge says "Paid". Danger is a bluish red so it never reads as the brand's terracotta; the two are never beside each other on the same control.
- Focus: a solid `focus` ring, `stroke-focus` wide with a 2px offset, on every interactive element in both themes. It is the only blue the brand uses apart from `info`.
- Never: gradients, pure black, pure white as a page ground, violet of any kind, a coloured left border on a card.

## Typography

Three families, all open licensed (OFL), with the real files in `fonts/`.

- **Bricolage Grotesque** (`display`) is the voice of the brand: the wordmark, display sizes and app page titles. Its slightly uneven, hand-cut letterforms are what keeps the identity from looking like every other admin tool. Use weights 500–700 only, at `title` and above. Never below 18px.
- **IBM Plex Sans** (`sans`) does the work: `body` at 15px for everything a person reads in the app, `label` for controls, `caption` for secondary text. It covers Turkish fully and has an Arabic companion for the day RTL ships.
- **IBM Plex Mono** (`mono`) is for figures: every money amount, balance and IBAN is set in `amount` so columns align digit for digit; headline balances in `amount-lg`; code and configuration in `code`. Amounts use the site's locale (`1.250,00 ₺`), a true minus sign (−) for debits, and are right-aligned in tables.
- Line lengths stay at 60–75 characters in the docs; the app's reading column is 640px wide at most.

## The logo

Files are in `brand/logos/`.

- The mark is the door alone; the wordmark is the mark beside **OpenBekci** in Bricolage Grotesque SemiBold, as outlines. The two letters that matter: capital O, capital B, one word, no space — exactly as the project is named.
- Clear space around either is the height of the door frame on every side. Minimum sizes: mark 16px (favicon), wordmark 120px wide.
- On paper and light grounds use `openbekci-mark.svg` / `openbekci-wordmark.svg`; on `night` and dark grounds the `-on-dark` versions (lighter terracotta, paper letters). One colour only: `openbekci-mark-mono.svg` in `night`.
- The app icon is the mark on a `night` tile with `radius-tile` corners; `openbekci-app-icon-light.svg` is the paper variant for stores that ask for one. The favicon is the bare mark, because a dark tile turns to mud at 16px.
- Never: recolour the mark outside `tile`/`night`/paper, add a shadow or gradient, rotate it, close the door, outline it, or place it on `tile` (it disappears). The mark may be flipped so the door opens from the right in RTL layouts — it is the one permitted variant.

## Shape

- Doors are square: the mark has no rounded corners, and UI corners stay modest — `radius-sm` on badges, `radius-md` on controls, `radius-lg` on cards. Nothing is a pill except a status dot.
- Borders are `stroke-hairline` in `line`, `line-strong` for inputs. No 2px borders except the focus ring.
- Spacing is the 4px scale: `space-4` card padding, `space-6` between cards, `space-8` page padding on desktop. Compact density (`--density: 0.75`) is a per-user choice for the big tables.
- Illustration is built from the mark's vocabulary: tall rectangles, slanted parallelograms (a door mid-swing), a single dark slab for shadow, in `tile`, `tile-wash`, `night` and paper. No people, no isometric buildings, no stock photography.

## Iconography

- **Lucide** (`lucide-react`), 20px at `stroke-icon` 1.75px, 16px inside badges and inputs. Lucide is the one icon set; never mix in a second.
- Icons take the text colour beside them. Icons that carry meaning alone (a status, a lock) also carry a tooltip or visually hidden label.
- Direction-bearing icons (arrows, chevrons, "send") flip in RTL with the `rtl-flip` class; the door mark may flip too.
- No emoji anywhere in the product, including announcements the system writes itself.

## Motion

Short and functional: 150ms ease-out for hover and focus, 200ms for menus and popovers opening, 250ms for dialogs. Nothing bounces, nothing loops, nothing moves on scroll. `prefers-reduced-motion` turns it all off.
