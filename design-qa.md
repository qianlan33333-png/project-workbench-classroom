# 全工作台方案 3 Design QA

## Evidence and normalization

- Selected visual truth: `artifacts/workbench-white-suite/option-3.png`, 1536×1024, displayed option 3 “单栏专注，按需侧栏”. Exact generated source: `exec-cb9e2c8b-453f-4d28-988b-1e576f091662.png`.
- Rendered implementation: local isolated real Store/API, fictional content, deterministic executor. Chrome with the user's authorized local Playwright; no real account or business data opened in the browser.
- Final full-view side-by-side comparison: `artifacts/workbench-white-suite/design-combined.png`, 3072×1024; source left, implementation right. Latest actual board: `design-board.png`, 1536×1024.
- Focused source/implementation comparison: `design-focused.png`, 1800×1480: navigation, composer and expert reading/details. Original readable implementation screenshots: `screens/design-{project,drawer,projects,experts,tasks,settings}.png`.
- A six-screen concept board is not a single viewport. Individual frame boxes and CSS viewports are recorded in `design-frames.json`. Left frames: 1247×571, 1247×489, 1247×493 CSS pixels; right frames: 1395×571, 1395×489, 1395×493. deviceScaleFactor=1. Each full application viewport is scaled uniformly by approximately 0.55 into its corresponding source frame. Browser chrome and board captions are excluded. Focus crops are enlarged equally on both sides.
- Matched states: project conversation with an attachment draft and enabled send, same conversation with files drawer, project index, selected expert overview, selected task details, selected WeChat connection details. All content is fictional. The source's generated claims about capabilities are not treated as API authority.

## Comparison history

Pass 1 is preserved as `design-comparison-pass1.png` and `design-focused-pass1.png`. It was blocked on:

1. P2: inherited @1280 navigation rules hid the brand label and put its toggle on another row. Explicit desktop rules now preserve a single brand row; 768–1099px intentionally uses a compact icon rail.
2. P2: the expert overview repeated its description and kept a permanent tab row. Overview now uses compact reading rows and direct file/scope/version links; editing remains a real action. Other modes retain their tabs.
3. P2: settings row/margin density pushed Advanced below the short source frame. Normal rows and margins were tightened; Advanced is visible in the corresponding final capture.
4. P2 found while matching the attachment state: project chips sat outside the composer boundary. They now sit inside it, and display the local filename. The final draft has an actual uploaded fixture attachment, not a drawn placeholder.

The latest combined and focused images were opened and compared after those fixes. No actionable P0/P1/P2 visual findings remain.

## Required fidelity surfaces

- **Fonts/typography:** existing system Chinese sans fallback, 14–16px primary UI, 23–25px page titles, controlled 1.55–1.85 line height. The generated source does not provide a font file; its inferred sans hierarchy is preserved. Actual text remains live and crisp. Long project/expert titles wrap or truncate without hiding primary controls.
- **Spacing/layout rhythm:** 190px desktop rail, compact page headings, single conversation canvas, wide composer, 350px project drawer and approximately 390–420px detail drawers. Open project context reserves its space on large screens; narrow drawers are modal. No permanent project second column. Lists use light dividers and restrained radii/elevation.
- **Colors/tokens:** white content, #171717 text, #737373 secondary text, #f4f4f4 selection and #e8e8e8 borders. Legacy green declarations were neutralized in their source styles. Necessary error/unknown/locked states retain their meaning. Native controls, focus and selected states stay discernible.
- **Images/assets/icons:** this design has no photos or illustration assets. Existing supplied “工” brand identity is retained; Lucide provides actual standard icons. No raster screenshot replaces interactive UI, and no handcrafted icon art substitutes for an asset. Enlarged generated source crops are softer than browser text; this is source raster density, not an implementation defect.
- **Copy/content:** English eyebrow text, marketing headers, repeated completed labels, keyboard helper and range footer are removed. Ordinary pending tasks omit redundant badges; actual state remains in details and exceptional rows. Technical runtime/receipts sit under Advanced. Source-only imaginary global expert scope, WeChat extraction rights and Feishu document write permissions were not introduced.

## Interaction and responsive evidence

`browser-receipt.json` records 83 passed checks, and `review-receipt.json` records 12 supplemental checks. Screenshots are under `artifacts/workbench-white-suite/screens/`.

Validated with actual isolated API state: file save, 409 preserving local and remote revisions, cancel/save/discard, stable conversation DOM and per-session drafts, attachment-only submission, task edit/completion, AI rule save, expert metadata/history/read-only revision/project binding, import/export menu keyboard handling, channel enable/disable, settings forms, topmost modal Escape/focus return, calendar/event editor, notices/failure boundary, personalization and second-level memory.

Supplemental review verified document/context exclusivity with dirty protection, saved project fields closing without false discard warnings, browser-back cancellation with one prompt, hidden CLI draft retention, deletion closing stale task details and unsaved AI rule protection. Login desktop/mobile rendering was verified through a local auth-state fixture.

Widths 1440/1280/1024/768/390/320 and a 390×420 short screen preserve the composer and avoid document-wide horizontal overflow. Narrow drawers trap focus; mobile navigation closes after selection. Browser page errors and unexpected console errors: zero. Expected unbound-project `/gateway/sessions/:id` 404 probes and intentional file 409 are recorded separately, not hidden as a successful real gateway connection.

## Intentional product constraints and residual scope

- Existing file Inspector, hash revisions, locks and download/history actions are retained. File editing uses that established pane rather than the concept's invented footer operations. Project context and Inspector remain mutually exclusive.
- Expert scope shows actual project/schedule bindings, and versions show actual revision history rather than invented semantic versions. The project index does not fabricate recent-activity values when unavailable.
- Existing notices, logout and optional recent-project navigation remain available. Their extra bottom-rail rows preserve working functionality.
- Mobile behavior is a responsive extension of the desktop concept. No device hardware, real provider OAuth, live customer record or external send was simulated as browser acceptance.
- Harness/channel/production verification is recorded separately in the release acceptance document; a deterministic preview response is not model evidence.

P3 follow-up only: lazy-load the editor bundle if download optimization becomes a priority; Vite reports the existing large application chunk. This does not block the current navigation or layout.

## Checklist

- [x] Open selected source and latest browser screenshots together.
- [x] Compare six matched states and readable focused regions.
- [x] Fix P0/P1/P2 and capture again.
- [x] Verify responsive layouts and actual interaction state.
- [x] Preserve API/permission boundaries and distinguish simulated vs real evidence.

final result: passed

## 2026-11-05 tool components extension

Applied Product Design image-to-code/design-qa against the existing white workbench, with user-authorized isolated Playwright after the in-app browser was disabled. Source and candidate settings were captured at 1440×1000, deviceScaleFactor 1, same authentication/theme/state, and opened together in `.var/tool-components-ui/comparison.png`.

Initial finding: the new settings-entry used inconsistent child structure, pushing its title to the right. Fixed by reusing the existing icon/strong/span/chevron row. The post-fix combined image shows matching left rail, title alignment, white canvas, typography, dividers and spacing; the added component row is the intentional new content. No image assets were needed. New forms reuse existing native controls and button styles, with detailed parameters inside disclosures.

Rendered candidate evidence: `components-desktop.png`, `run-config-desktop.png`, `session-mobile.png` and `session-small-mobile.png` in the same directory. Existing source has no equivalent component form; these surfaces were checked for visual consistency and actual interaction rather than claimed pixel identity. Playwright passed fixed config save/run, scheduling/pause, async result, owner review parameters/confirmation and no page errors. 390×844 and 320×640 have no document horizontal overflow. Focused row/control text is readable in captures; no font or icon substitution was introduced. Browser acceptance uses fictional isolated API state, separate from real Harness/provider/MySQL acceptance.

final result: passed

## 项目右栏统一验收

以用户提供的重复文件树截图和确认的四入口布局为对照。桌面 1440px 的资料和手机 390px 的全屏资料截图位于 `.var/sidebar-preview/`，同时实测了平板 900px。沿用现有字体、黑白灰配色、文件图标和分隔线。右边缘使用图标与文字区分四个入口，选中状态有可见底色。顶部重复入口与第二份文件树移除，面板窄化后输入框和返回操作可达。

隔离浏览器验证了 32 次切换、迟到请求、跨项目草稿、未保存文件取消与放弃、任务编辑跨断点、规则详情和直达链接。控制台页面异常为零。

final result: passed

## 移除合并历史入口

对照用户圈选范围，在同一项目、同一桌面视口比较了发布前页面与候选前端（`.var/hide-history/compare.png`）。历史折叠区及上方分隔线已移除，三条正常对话和四个右栏入口显示正常。切换资料后仍可访问「历史来源」，回到对话时没有额外历史请求或页面异常。

final result: passed
