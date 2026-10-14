# Deepzora startup animation and ribbon logo QA

final result: passed

The previous sandbox settings QA report is preserved in [docs/design-qa/sandbox-settings.md](docs/design-qa/sandbox-settings.md).

The upstream model onboarding QA report is preserved in [docs/design-qa/provider-onboarding.md](docs/design-qa/provider-onboarding.md).

## Follow-up: ceramic application icon

The user selected the first displayed app-icon concept (`exec-f7629b05-9448-41ab-84f0-9f59beec9493.png`). The application icon now has its own warm ceramic plate and a larger orange/ivory motif. `desktop/branding/appicon-ceramic.png` is the selected source; `scripts/generate-brand-icons.py` clips its exterior fringe and fits the plate once into each platform's grid. The transparent in-app symbol and monochrome menu-bar variant still derive from `logo-ribbon.png`.

- Opened and reviewed the combined source/export comparison `ceramic-comparison.png`; normalized source and export to equal 512px canvases.
- Inspected the browser-rendered 16–64px icons on white and near-black surfaces and saved `brand-assets-ceramic.png` in the existing evidence directory. Larger motif, readable ivory fold, clean outer rounded contour, and ceramic backdrop match the selected direction. Raster normalization and the 824px macOS grid are intentional platform adjustments.
- Updated native icon tests to require an explicit opaque plate and square safe area. PNG/ICO representation checks and pixel equality between the development PNG and macOS ICNS pass.
- Rebuilt local arm64 package `v0.0.0-startup-local.20261014.2`; bundle structure, ad-hoc signature verification, ready handshake, packaged Version RPC, hold, and normal shell/service shutdown pass. The bundled ICNS hash matches the new source resource.
- Opened that package using the independent local test home. Its startup log confirms the new version is running. Direct Dock screenshot capture was unavailable; actual Dock appearance remains for the user to inspect, rather than being claimed as visually verified here.
- The subsequent wordmark redesign is implemented as described below.

## Follow-up: complete humanist wordmark

The user selected the first displayed lettering concept (`exec-a18057d4-4ae8-49af-9be0-cf1fe3d92158.png`). The separate folded D symbol is followed by the complete name **Deepzora**, replacing the previous symbol-plus-`eepzora` construction. A transparent production rendition is retained at `desktop/branding/wordmark-humanist.png`; the generator embeds that actual artwork into the shared SVG consumed by desktop and the embedded web UI.

- Opened the selected concept and rendered component together in `wordmark-comparison.png`; reviewed shape, full spelling, symbol/letter balance, baseline and spacing. Also checked `brand-assets-humanist.png` on light and near-black backgrounds. The comparison enlarges the real 250px component capture, so interpolation blur in that board is not a source-asset defect.
- Preserved the selected rounded orange letterforms and ivory fold. Transparent counters and padding remain transparent. There is no installed-font dependency or approximation with system text.
- Fixed the old negative sidebar margins, which assumed substantial transparent SVG padding, and removed dark-theme white-mask filters that would erase the selected orange/ivory palette. The sidebar slot is 136 × 28px; the existing welcome image slot remains 240 × 56px.
- Captured the rebuilt native application as `wordmark-native.png`: the complete sidebar lockup is readable and aligned with its surrounding navigation. This startup state uses the creation landing screen; the shared welcome wordmark is additionally inspected through the asset preview rather than claiming it was visible in that native state.
- Rebuilt and opened arm64 local test package `v0.0.0-startup-local.20261014.3`. Frontend production gates, native icon tests, embedded logo serving, bundle structure, signature validation, handshake, packaged Version RPC, hold and normal shutdown pass. The package retains the selected ceramic icon and its ICNS hash matches the current source resource.

No remaining actionable P0/P1/P2 differences in the selected wordmark. The local test application is left open for inspection.

## Integration with current main

Integrated main `519ab7e36`, preserving its first-launch/model onboarding changes and Linux icon naming. The upstream macOS normal/Retina opaque-corner regression is retained, with its color expectation updated for the selected ceramic plate. The macOS export uses a 20% corner radius while other platform exports retain the 24% selected contour; this prevents transparent edge pixels at the macOS compatibility sample points without changing the ribbon or wordmark masters.

After integration, frontend production gates and bundle budgets, root `make lint`, desktop lint, Electron identity/icon resolution, packaging unit tests and embedded logo-serving checks pass. The browser's dark/English view, normal ready exit and Escape skip were rechecked. The earlier local test package predates this integration and corner-export adjustment; its native smoke results are evidence for that package, not a claim that the final PR commit was repackaged.

Final PNG/ICO/ICNS regression checks pass. Generated transparent edge pixels are composited onto the ceramic base before masking so hidden RGB fringe cannot become visible. The updated `verify-macos-icon.swift` compares native plate samples and orange motif bounds to the packaged artwork rather than requiring the superseded solid-orange background. System rendering at 32, 64, 128, 256, 512 and 1024px passes using the final ICNS in a complete, ad-hoc signed copy of the earlier application. Evidence is retained in `/tmp/deepzora-brand-pr-icon/actual-rendered/`; this checks icon rendering without claiming a new full production build.

## Scope and evidence

- Startup layout: the first displayed Product Design concept, `exec-4198fc1c-9595-484e-8ca2-1530bbb79f73.png` (1646 × 956).
- Logo: the user's `codex-clipboard-cb4e3112-7b97-4914-9074-bf33c86c1dcc.png` (362 × 296), subsequently authorized for both application artwork and in-app branding.
- Actual production `StartupSplash` component rendered in the in-app browser through `bench/startup-splash.html`, with the production stylesheet, translations and assets.
- Comparison viewport: 1240 × 720 CSS px, device scale factor 1. The source splash is normalized to those dimensions. Preview controls and absent native window chrome are excluded from fidelity judgments.
- Evidence directory: `/Users/work/.codex/visualizations/2026/10/14/01a139cb-bf0a-7121-bd6c-99d1f0c9bf85/`.
- Combined comparisons opened and reviewed: `startup-comparison.png` and `logo-comparison.png`. Additional captures: `startup-light.png`, `startup-dark.png`, `startup-small-reduced.png`, `brand-assets.png`.

## Findings and repairs

1. **P2, initial orbit sizing:** the generated orbital decoration included padding, making its visible tracks smaller than the chosen reference. Enlarged the decoration slot, then replaced the initial ellipse bitmap with circular tracks projected into a fixed elliptical plane. The image now rotates continuously every 18 seconds while the layout and logo remain stable. Post-fix comparison retains the reference's surrounding orbit composition.
2. **P2, original symbol aspect ratio:** a square image box stretched the original symbol. Added `object-fit: contain`; the new ribbon also preserves its natural aspect ratio.
3. **P2, short-window overlap:** the orbit approached the headline at a 640 × 480 viewport. Reduced its slot below 540px window height; the updated English capture shows separate logo, wordmark, headline and loading regions without horizontal overflow.
4. **P2, 24px icon alpha fringe:** native asset validation found an alpha=1 corner after resampling. Removed that imperceptible fringe during export. All required ICO representations pass the transparency and orange/ivory surface checks; the macOS PNG and ICNS artwork match pixel for pixel.
5. **P2, visible rectangle under the wordmark:** the user identified a visible text-background rectangle interrupting the orbit. Removed the background and its extra span. Moved the orbital center up and flattened its fixed projection to surround only the logo, leaving the wordmark below the full path. Final light/dark and 640 × 480 captures show a transparent wordmark and continuous trajectories without intersecting text. This replaces the earlier masking approach.

No remaining actionable P0/P1/P2 issues.

## Required fidelity surfaces

- **Fonts and typography:** existing system font stack, 28px splash brand, responsive 23–30px headline, 12px status and skip control. Content hierarchy matches the reference and English remains readable at 640 × 480. Shared horizontal branding uses the selected humanist artwork described above. The supporting description is removed per the user's final annotated request.
- **Spacing and layout rhythm:** centered composition, independent orbit/brand slot, 24px headline gap, 32px loading gap directly below the headline. Brand silhouette never rotates into the copy. Small-window overrides retain room for the skip control.
- **Colors and tokens:** orange identity remains `#f2610c`; background and text follow the application's `--bg`, `--fg`, `--fg-dim` tokens. The application's default light surface is cooler than the mock's warm white; retaining the user's active theme rather than hardcoding the mock color is an intentional requirement. Dark-mode contrast is visually checked.
- **Image quality and asset fidelity:** transparent generated ribbon retains the orange D-shaped volume and solid ivory folded face. Reference background, floor shadow and trailing ribbon are removed for logo use. 16/24/32/48/64px native assets reviewed together on white and near-black surfaces. SVG wrappers embed real raster artwork, not vector approximations. Menu-bar variants use a monochrome silhouette with the ivory fold as negative space. A focused comparison of the supplied logo and rendered asset board supplements the full splash comparison.
- **Copy and content:** Chinese headline matches the selected splash concept; its English equivalent is included. Both Chinese and English supporting descriptions are removed per the user's final request. Loading is indeterminate and labeled "Preparing your workspace", with no invented percentages or initialization stages. Skip is a real button and retains Escape/Enter/Space support.

## Behavior and checks

- Reveal delays: symbol 0ms, wordmark 160ms, headline 320ms, loader 480ms, skip 640ms. All reveals finish before the unchanged 1400ms minimum hold.
- Actual computed transforms change over time for the orbit; three loading segments flow sequentially. No new animation runtime dependency.
- `prefers-reduced-motion: reduce`: all splash entry and repeating animations disabled; full content remains visible. Checked through browser media emulation, subsequently restored.
- Browser interactions: normal ready startup ends automatically; changing hold to ready ends it; Escape skips it; the 6s hold guard ends a stalled wait. Locale-module hot replacement caused transient context errors while editing translations; after a clean reload, final interaction checks show no new console warnings/errors.
- Frontend build, hooks lint, CSS syntax, theme token contract, architecture checks and bundle budgets pass.
- Native icon resource tests pass, including all required Windows sizes, macOS safe area and development/bundle equality. Electron identity/icon resolution tests and embedded server logo-serving tests pass.
- The local ad-hoc signed arm64 test package was rebuilt and launched as recorded above. Installed production applications were not replaced; Windows and Linux native packages were not launched.

## Implementation checklist

- [x] Selected splash implemented in the existing component.
- [x] Continuous orbit, sequential reveals and flowing loading segments.
- [x] Light/dark, English/Chinese, small viewport and reduced motion verified.
- [x] Shared in-app symbols, wordmarks, favicons and platform/tray assets replaced.
- [x] Reproducible asset generation script and source artwork retained.
- [x] Combined visual comparison completed after repairs.
