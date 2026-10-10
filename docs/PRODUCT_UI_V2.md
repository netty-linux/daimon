# Product UI V2 — Phase 16.5

## Audit and migration plan

Baseline: 58 UI tests in 13 files passed before edits. The current App owns
resource selection, CRUD dialogs, per-conversation runs and transcript observation.
Its sidebar mixes Bot navigation with provider/MCP/Computer/Sandbox diagnostics;
Environment occupies every chat; English runtime terminology, raw paths/IDs and
event counters dominate. Messages are plain text boxes without reading-width or
scroll-follow behavior. No frontend router or Markdown dependency exists.

Preserve API DTOs, defensive decoding, same-origin requests, native dialogs,
useConversation pagination/cancellation, useSessionEvents bounded replay/fallback,
one-shot approvals, complete escaped previews, and ComputerViewer authority/media
cleanup. Build static assets before Go compilation; no backend changes.

Proposed navigation: Bot sidebar, selected Bot's conversations, main surface with
Chat / Computador / Arquivos / Memória / Atividade. Settings contain providers,
MCP, Computer, local isolation/cloud and system details. Approval remains outside
tab visibility. Existing #thread= bookmarks survive; selected tab is added to hash.

Component hierarchy: App retains admission/selection orchestration; feature
components own navigation, transcript/composer, activity and settings. Existing
capability panels are reused behind progressive disclosure. Central PT-BR catalog
contains UI copy, errors, lifecycle labels and presentation-only vocabulary.
Central CSS tokens define charcoal surfaces, off-white accent, spacing, radii,
system fonts, focus, layers and reduced motion; no fonts/CDN/framework added.

Migration: preserve contracts first, establish tokens/localization, extract product
surfaces, restyle capability panels/editors, migrate semantic test selectors, then
extend browser and responsive checks. Existing assertions for escaping, no retry,
approval completeness, input authority, cancellation, persistence and bounds remain.

## Principles and information architecture

Chat is primary. Users choose a Bot, create/select a conversation and send a task.
Infrastructure appears only when deliberately selected. Primary UI excludes raw
IDs, backend codes, phase numbers and provider IDs. Advanced settings retain them.
No feature is a permission grant; the server remains authoritative.

## Terminology and localization

PT-BR only in V2, with a central catalog ready for future replacement. Thread is
Conversa; Session is Execução when needed; Computer is Computador; Environment is
Ambiente persistente; Sandbox is Computador isolado. Model names, code, user content,
complete approval contract and external descriptions retain exact original text.

## Design system

Central design tokens cover colors, spacing, typography, radii, focus, z-index and
motion. Reusable restrained buttons, surfaces, fields and native dialogs. No neon,
external fonts, background image, analytics, account/billing or heavy dependencies.

## Navigation, chat and activity

Three desktop columns, independently collapsible navigation. Chat width stays
comfortable, user bubbles subtle, assistant Markdown rendered as React text/nodes,
never raw HTML. Code blocks scroll/copy. No fabricated token streaming. Transcript
refresh follows accepted runs and terminal snapshots. Scroll follows only near the
bottom; activity remains a separate safe human-readable timeline.

## Computer, Files and Memory

Computer reuses the existing deliberate media/control implementation and Escape
release. Frames/tickets/control never persist. Files shows Environment metadata
only, explicit enable and confirmed delete, including irreversible committed state
and cloud-saving cost. No file browser or new filesystem endpoint. Memory remains
explicit CRUD, grouped by conversation/Bot/global; no inferred/automatic memories.

## Settings and advanced mode

Infrastructure configuration remains on the server. Provider list is not a model
catalog; no availability is invented. Bot PUT still requires complete instructions
because GET deliberately omits them. Thread creation still requires an explicit
server folder; V2 cannot silently replace this immutable backend contract. Advanced
fields reveal exact declarations/identifiers, never secrets or raw server errors.

## Accessibility and responsive behavior

Native modal focus trap/restoration, visible keyboard focus, semantic tab panels,
arrow/Home/End tab navigation, textual status, labeled buttons and reduced motion.
Ctrl/Cmd+Enter sends from the composer only. Escape remains local to Computer/modal.
Navigation collapses at narrower widths; content remains usable without body overflow.

## Non-goals

(histórico) No runtime/backend redesign, authentication, subscriptions, Honcho, Intelligent
Memory, background runs, subagents, file manager, mobile/desktop app or Phase 17.

## Validation

Baseline preserved: 58 tests / 13 files; final interface suite: 69 tests / 14 files.
`npm ci --offline`, `npm test`, `npm run build` (includes TypeScript check).
Browser fixtures use Edge, local provider/driver doubles and temporary resources;
no paid CUA, cloud credentials, installation or external provider requests.
Final Linux and browser results follow below.


## Files and migration boundaries

Created: `ui/src/design/{tokens.css,product.css,Icon.tsx}`,
`ui/src/i18n/{pt-BR.ts,copy.ts,errors.ts}`,
`ui/src/features/navigation/Navigation.tsx`, `features/settings/Settings.tsx`,
`features/activity/Activity.tsx`, `features/chat/{Chat.tsx,Markdown.tsx}`,
`features/product.test.tsx`, `components/ProductTabs.tsx`, and this document.

Changed: App, Editors, MemoryPanel, EnvironmentPanel, ComputerViewer, ComputerPanel,
SandboxPanel, MCPPanel, API presentation errors/event labels, event connection
notices, styles and HTML metadata. Existing component tests and browser selectors
were migrated to semantic PT-BR labels while retaining behavior assertions.
Browser smoke now covers reconnect, execution failure, settings and viewport widths
1440/1280/1024/600. Static embedded assets regenerated. README, AGENTS and historical
Web UI documentation updated. Backend source and API contracts were not redesigned.

## Remaining limits and explicit choices

- Providers expose connections, not a model catalog: the model identifier is entered
  explicitly. No fabricated default model or availability.
- Conversation creation still needs an explicit server folder. Existing immutable
  Bot/folder binding cannot become optional through a presentation change.
- Creation instructions may be omitted: the visible, controlled default is submitted.
  Editing still requires complete instructions because Bot GET does not expose them.
- Markdown is a small safe subset, not a full CommonMark/GFM engine. Raw HTML and
  embedded images are not interpreted; clipboard availability/errors are handled.
- First cloud configuration requires cost confirmation. It does not provision
  compute or replace runtime approvals. No billing/accounts introduced.
- No file manager, automatic memory, token streaming or guarantee against browser
  resource limits. Real paid cloud behavior was not exercised by these fixtures.

Phase 17 — Background Tasks: parte Native Routines Foundation implementada (escopo restrito); próximo passo é a validação com CUA real.
Deferred: background agents, Honcho, Intelligent Memory, subagents, billing, accounts
and standalone mobile/desktop apps. None implemented in this phase.


## Final validation results

All commands completed successfully:

```sh
# ui/
npm ci --offline
npm test                         # 69 tests, 14 files
npm run build                    # TypeScript + Vite embedded assets
# Browser fixtures: DAIMON_SMOKE_CHANNEL=msedge
node scripts/smoke.mjs
node scripts/approval-smoke.mjs
node scripts/approval-smoke.mjs --mcp
node scripts/memory-smoke.mjs
node scripts/computer-smoke.mjs
node scripts/computer-smoke.mjs --missing
node scripts/computer-view-smoke.mjs
node scripts/sandbox-smoke.mjs
node scripts/sandbox-smoke.mjs --cloud
node scripts/environments-smoke.mjs
# Root, real Linux Docker golang:1.27.1, network disabled
gofmt -w .
gofmt -l .                       # empty
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go run ./cmd/daimon demo          # completed, two steps, one tool call
# Rechecked final regenerated embedded assets in Linux
go test -count=1 ./internal/server
go build ./cmd/daimon
git diff --check
```

Browser coverage includes persisted CRUD/transcript, failed admission and explicit
retry affordance, SSE reconnect, safe deletion, exact approval previews, allow/deny,
reload/two-tab replay, abort/shutdown, memory isolation and immutable active context,
missing/crashed drivers, live view/human input/exclusion/release, cloud cleanup and
persistent environment revisions across local/cloud and restart. Menus close after
action; memory deletion uses a modal; notification dismissal has a distinct label.
A screenshot contains only fixture chat content, never captured computer media.
(histórico) No commit, push, paid execution or implementation of Phase 17 was performed.
