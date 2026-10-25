#!/usr/bin/env python3
"""Audit the migrated Web source, lockfile, provenance, and release boundary."""

from __future__ import annotations

import argparse
import collections
import hashlib
import json
import os
from pathlib import Path
import re
import sys
from typing import Any

if __package__:
    from .frontend_source_namespace import canonical_source_records
else:
    from frontend_source_namespace import canonical_source_records


DEFAULT_ROOT = Path(__file__).resolve().parents[2]
FRONTEND = "frontend"
SOURCE_MANIFEST = "SOURCE_IMPORT_MANIFEST.json"
MIGRATION_MANIFEST = "MIGRATION_MANIFEST.json"
LICENSE_REPORT = "THIRD_PARTY_LICENSES.json"

BLOCKED_SERVER_PACKAGES = {
    "@synon-ai/web-host",
    "@x-science/web-host",
    "bcryptjs",
    "better-sqlite3",
    "cookie-parser",
    "cors",
    "express",
    "express-rate-limit",
    "jsonwebtoken",
    "multer",
    "sharp",
    "ws",
    "yauzl",
}

ALLOWED_ADAPTATIONS = {
    "packages/desktop/src/common/platform/IPlatformServices.ts": "Document the canonical current log-directory namespace without changing configured operator paths.",
    "packages/desktop/src/common/platform/index.ts": "Use the canonical environment and development application names, preserving multi-instance behavior.",
    "packages/desktop/src/common/platform/NodePlatformServices.ts": "Derive only the package-name fallback from the current root product slug, retaining configured package names and existing data/log paths.",
    "packages/desktop/src/common/chat/slash/availability.ts": "Rename current first-party transport documentation to the current product display without changing command eligibility or protocol identifiers.",
    "packages/desktop/src/renderer/pages/conversation/GroupedHistory/ProjectHistorySection.tsx": "Synchronize the committed project list before visible reorder handles can receive input.",
    "tests/unit/renderer/ProjectHistorySection.dom.test.tsx": "Exercise immediate keyboard input on committed project handles before passive effects flush.",
    'packages/desktop/src/renderer/main.tsx': 'Remove the global runtime monkey-patch import so React, ResizeObserver, requestAnimationFrame, warnings, and errors retain their native observable behavior.',
    "packages/desktop/src/common/chat/chatLib.ts": "Carry validated canonical Transcript message coordinates and cancelled tool-call status through the migrated message model.",
    "packages/desktop/src/common/chat/normalizeToolCall.ts": "Normalize the canonical cancelled tool-call status without guessing terminal state.",
    "package.json": "Replace Bun/WebHost workspace runtime with a browser-only npm build package and exact dependency boundary.",
    "packages/desktop/package.json": "Remove @x-science/web-host workspace dependency and identify the browser-only renderer.",
    "packages/desktop/src/common/adapter/browser.ts": "Use the authenticated same-origin Go event WebSocket and adapt its event and control envelopes without retaining the legacy WebHost transport.",
    "packages/desktop/src/common/adapter/httpBridge.ts": "Retire the legacy WebSocket owner and bind the explicit kind-and-type bridge to the single authenticated realtime runtime.",
    "packages/desktop/src/common/adapter/ipcBridge.ts": "Represent the Go MCP OAuth pending-browser contract without changing the shared bridge API shape.",
    "packages/desktop/src/common/config/storage.ts": "Keep the browser runtime status model aligned with canonical completed, failed, and cancelled Transcript terminal states.",
    "packages/desktop/src/common/config/i18n-config.json": "Narrow the product language contract to fully maintained Simplified Chinese and English bundles.",
    "packages/desktop/src/renderer/hooks/context/AuthContext.tsx": "Replace the WebHost CSRF stubs with the Go session and double-submit CSRF bootstrap contract.",
    "packages/desktop/src/renderer/hooks/mcp/useMcpOAuth.ts": "Complete MCP OAuth in WebUI through a bounded popup-and-status-poll flow backed by the Go server.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/MarkdownViewer.tsx": "Use the locked Streamdown 1.5.1 contract and avoid its redundant runtime KaTeX CSS import because styles are bundled statically.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/XScienceStructureViewer.tsx": "Unify PDB, mmCIF, SDF, and related molecular formats on one Molstar preview with composition-aware controls, collapsed auxiliary panels, and source-fenced 2D interaction refresh.",
    "packages/desktop/src/renderer/index.html": "Project the root X-Science product identity and load the CSP-compatible pre-render theme bootstrap without creating a second version authority.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/AcpE2EStreamInjector.tsx": "Expose a session-gated E2E assistant-message injector so browser preview tests do not require a paid live model.",
    "packages/desktop/src/renderer/pages/onboarding/OnboardingFlow.tsx": "Keep bundled Python and R required while preserving optional first-run scientific-runtime choices, informational storage estimates, authenticated drafts, capability allowlists, and one recoverable launch.",
    "packages/desktop/src/renderer/pages/onboarding/OnboardingElicitCard.tsx": "Give the custom onboarding task input an explicit accessible label while preserving the task-selection contract.",
    "packages/desktop/src/renderer/pages/onboarding/onboardingModel.ts": "Derive initial connector and Skill state from the server allowlist without duplicating the immutable selection model.",
    "packages/desktop/src/renderer/pages/onboarding/onboarding.module.css": "Keep the three capability tabs and long scientific-runtime list readable and vertically scrollable without horizontal overflow on short or narrow viewports.",
    "packages/desktop/src/renderer/pages/settings/ComputeSettings.tsx": "Tighten settings copy for the Go-owned compute and inference control surface.",
    "packages/desktop/src/renderer/pages/settings/CredentialsSettings.tsx": "Tighten settings copy while preserving encrypted credential boundaries.",
    "packages/desktop/src/renderer/pages/settings/GeneralSettings.tsx": "Remove duplicated model controls and simplify local Go deployment guidance.",
    "packages/desktop/src/renderer/pages/settings/GovernanceSettings.tsx": "Condense governance copy for the migrated memory surface.",
    "packages/desktop/src/renderer/pages/settings/NetworkSettings.tsx": "Clarify the Go runtime network allowlist controls.",
    "packages/desktop/src/renderer/pages/settings/PermissionsSettings.tsx": "Clarify remembered permission review and revocation controls.",
    "packages/desktop/src/renderer/pages/settings/StorageSettings.tsx": "Compose independently loaded storage metadata, truthful usage, file rules, and cloud connections with safe mutation dialogs.",
    "packages/desktop/src/renderer/pages/settings/XScienceExpertsSettings/ExpertWorkbench.tsx": "Improve responsive expert rows and compact long descriptions without dropping source detail.",
    "packages/desktop/src/renderer/pages/settings/XScienceModelsSettings.tsx": "Condense model-provider settings copy for the migrated Web surface.",
    "packages/desktop/src/renderer/pages/settings/XScienceSkillsSettings.tsx": "Unify installed Skill sources in one searchable library and consolidate acquisition into one action while preserving personal, import, toggle, and market workflows.",
    "packages/desktop/src/renderer/pages/settings/ToolsSettings/XScienceMcpSettings.tsx": "Improve MCP connector density, localization, and responsive control layout.",
    "packages/desktop/src/renderer/pages/settings/ToolsSettings/index.tsx": "Clarify the expert MCP tool-management surface.",
    "packages/desktop/src/renderer/pages/guid/utils/assistantDefaults.ts": "Preserve owner-scoped connected MCP authority when an assistant uses automatic defaults and no explicit remembered selection exists.",
    "packages/desktop/src/renderer/pages/settings/components/SettingsPageHeader.tsx": "Give settings actions a stable responsive layout hook.",
    "packages/desktop/src/renderer/pages/settings/components/SettingsPageWrapper.tsx": "Provide route-aware responsive settings layout without nested page cards.",
    "packages/desktop/src/renderer/pages/settings/components/SettingsSider.tsx": "Align settings navigation icon states with the shared visual tokens.",
    "packages/desktop/src/renderer/pages/settings/components/XScienceMemoryManager.tsx": "Render a recoverable inline memory-load failure state with an explicit retry action.",
    "packages/desktop/src/renderer/pages/settings/components/settings.css": "Stabilize responsive settings dimensions and shared visual tokens across desktop and narrow layouts.",
    "packages/desktop/src/renderer/services/SpeechToTextService.ts": "Attach the Go double-submit CSRF token to same-origin batch speech uploads without leaking it cross-origin.",
    "packages/desktop/src/renderer/services/authSession.ts": "Monitor session expiry and decorate unsafe same-origin browser requests with the Go CSRF token.",
    "packages/desktop/src/renderer/services/i18n/i18n-keys.d.ts": "Regenerate typed locale keys for required Python/R core runtimes and the optional first-run scientific-runtime catalog.",
    "packages/desktop/src/renderer/services/i18n/locales/en-US/guid.json": "Describe required Python/R core runtimes, optional first-run scientific runtimes, and informational storage estimates in the complete English onboarding locale.",
    "packages/desktop/src/renderer/services/i18n/locales/en-US/preview.json": "Add English labels for unified structure composition, collapsed 2D panels, and format-neutral interaction controls.",
    "packages/desktop/src/renderer/services/i18n/locales/zh-CN/guid.json": "Describe required Python/R core runtimes, optional first-run scientific runtimes, and informational storage estimates in the complete Simplified Chinese onboarding locale.",
    "packages/desktop/src/renderer/services/i18n/locales/zh-CN/preview.json": "Add Simplified Chinese labels for unified structure composition, collapsed 2D panels, and format-neutral interaction controls.",
    "packages/desktop/src/renderer/services/i18n/locales/zh-CN/settings.json": "Explain required scientific runtimes, unified user-managed storage, and X-Science Go service guidance.",
    "packages/desktop/src/renderer/services/i18n/locales/zh-TW/settings.json": "Replace obsolete bun start deployment guidance with X-Science Go service guidance.",
    "packages/desktop/src/renderer/services/onboardingService.ts": "Validate and persist the server-owned scientific-runtime selection without imposing client storage ceilings, while preserving exact Artifact and launch authority.",
    "packages/desktop/src/renderer/services/xScienceArtifactPreview.ts": "Route supported protein, complex, and ligand formats through the single composition-aware molecular preview authority.",
    "packages/desktop/src/renderer/styles/workspace-theme.css": "Provide a visible keyboard focus indicator for shared buttons across the migrated Web surface.",
    "packages/desktop/src/renderer/styles/themes/base.css": "Allow the authenticated onboarding and workbench layout to fit the supported 320px viewport without horizontal clipping.",
    "playwright.webui.config.ts": "Point browser acceptance at the future Go same-origin host and standardize disposable output.",
    "public/manifest.webmanifest": "Project the root X-Science identity into installable PWA metadata and retain the text-free molecular icon set.",
    "tests/integration/i18n-packaged.test.ts": "Reference the browser-only production build command used by X-Science Go.",
    "tests/integration/xscienceTestAuth.ts": "Provide an origin-scoped session and CSRF fetch client for authenticated Go integration tests.",
    "tests/unit/common-adapter/browserRealtimeError.test.ts": "Verify the browser adapter uses the authenticated Go event WebSocket and preserves runtime event and control envelopes.",
    "tests/unit/renderer/ArtifactPreview.dom.test.tsx": "Isolate persisted client configuration while testing native artifact preview and cloud export behavior.",
    "tests/unit/previews/XScienceStructureViewer.dom.test.tsx": "Verify format-neutral composition controls, collapsed 2D panels, and source-invalidated interactions after minimization.",
    "tests/unit/previews/xScienceScientificPreview.test.ts": "Verify supported molecular extensions resolve to the unified structure viewer without a competing preview path.",
    "tests/unit/renderer/authSession.dom.test.tsx": "Verify authentication refresh and same-origin unsafe-request CSRF decoration in a real DOM fetch boundary.",
    "tests/unit/renderer/OnboardingFlow.dom.test.tsx": "Verify runtime selection, storage estimates, search, unavailable-host choices, and capability-tab keyboard behavior in onboarding.",
    "tests/unit/renderer/onboardingService.test.ts": "Verify scientific-runtime snapshot validation and exact selected-ID persistence alongside existing onboarding mutations.",
    "tests/unit/renderer/speechToTextService.test.ts": "Verify bounded speech uploads and same-origin-only CSRF header behavior.",
    "tests/unit/settings/GeneralSettings.dom.test.tsx": "Verify the streamlined general settings information architecture.",
    "tests/unit/settings/XScienceMcpSettings.dom.test.tsx": "Verify localized and compact MCP connector presentation.",
    "tests/unit/settings/XScienceMemoryManager.dom.test.tsx": "Verify the explicit memory-load failure and retry state.",
    "tests/unit/settings/XScienceSkillsSettings.dom.test.tsx": "Verify localized Skill source labels and compact rows.",
    "tests/unit/settings/WorkspaceSafetySettings.dom.test.tsx": "Keep safety-settings assertions aligned with the clarified permission copy.",
    "tests/vitest.dom.setup.ts": "Provide the DOMMatrix browser API required by scientific viewers under jsdom.",
    "tests/web-e2e/xscienceArtifactMessageLinks.e2e.ts": "Create real disposable artifacts and verify assistant Markdown links open native previews at desktop and narrow sizes.",
    "tests/web-e2e/xscienceLoginBootstrap.e2e.ts": "Use configurable credentials and verify authenticated cookies, CSRF writes, runtime WebSocket bootstrap, and responsive onboarding interaction.",
    "tests/web-e2e/xscienceMcpSettings.e2e.ts": "Use configured credentials and verify the Go MCP settings flow in responsive Chromium.",
    "tests/web-e2e/xscienceRuntimeOperations.e2e.ts": "Use disposable authenticated runtime state for responsive control-surface acceptance.",
    "tests/web-e2e/xscienceScientificPreview.e2e.ts": "Upload real scientific fixtures and verify parsed, nonblank desktop and narrow previews.",
    "tests/web-e2e/xscienceSequenceNotebookPreview.e2e.ts": "Upload real sequence and notebook fixtures and verify their native responsive viewers.",
    "tests/web-e2e/xscienceSkillsSettings.e2e.ts": "Use configured credentials and verify the Go Skill lifecycle in responsive Chromium.",
    "uno.config.ts": "Use granular UnoCSS packages to reduce build dependency weight.",
    "vite.config.ts": "Use the granular UnoCSS Vite integration.",
    "vitest.config.ts": "Remove the excluded Bun WebHost project and isolate build-output verification from source tests.",
}

ALLOWED_ADDITIONS = {
    "packages/desktop/src/common/adapter/literatureLibrary.ts": "Define the first-party closed independent-literature item, attachment-version, mutation and receipt wire contract without creating another transport or persistence owner.",
    "packages/desktop/src/common/adapter/literatureMetadataPatch.ts": "Strictly snapshot generic Library metadata field deltas and validate only changed receipt keys while preserving unedited canonical metadata, the existing mutation owner, and literal JSON field semantics.",
    "packages/desktop/src/common/adapter/literatureValueIdentity.ts": "Factor the existing canonical Library request and receipt JSON equality into a dependency-free helper without changing its comparison semantics or creating another request, transport, or persistence authority.",
    "packages/desktop/src/common/adapter/specialistBinding.ts": "Define the shared closed Specialist input-reference and frozen send-authority wire contract for IPC, queues and initial-message decoding; no execution or transport owner.",
    "packages/desktop/src/renderer/pages/conversation/components/DelegatedStopActions.tsx": "New React adapter for the reference compact separate selected-branch Stop/pending/error interaction; single-child Stop, receipt inspection and retry are X-Science extensions. Adapted with reference to Open Science6d869cd: src/renderer/src/pages/workspace/ConversationPanel.tsx. Single-child Stop and physical receipt recovery are X-Science additions, not copied reference controls.",
    "packages/desktop/src/renderer/pages/conversation/components/useDelegatedStop.ts": "Binds existing account lifecycle, selected branch and canonical wake events to the one Stop controller.",
    "packages/desktop/src/renderer/services/runtime/delegatedStopContract.ts": "Strict X-Science wire decoding, exact request/receipt checks and field-wise rendered intent coordinate comparison.",
    "packages/desktop/src/renderer/services/runtime/delegatedStopController.ts": "Shared synchronous reservation, rendered coordinate fencing, immutable explicit retry and stale account/read response isolation.",
    "packages/desktop/src/renderer/services/runtime/delegatedStopHints.ts": "Bounded account/incarnation-scoped original request hints; not execution or completion authority.",
    "packages/desktop/src/renderer/services/runtime/delegatedStopRegistry.ts": "Shares bounded controllers by existing account generation and parent incarnation and releases/reset ownership.",
    "packages/desktop/src/renderer/services/runtime/delegatedStopTransport.ts": "Uses the existing authenticated HTTP bridge for snapshot, submit and original-UUID lookup; preserves failure classification.",
    "packages/desktop/src/renderer/services/runtime/delegatedStopTypes.ts": "X-Science typed scope, execution target, frozen request, receipt and presentation unions.",
    "tests/unit/chat/XScienceDelegatedStop.dom.test.tsx": "Exercises actual Dock/Drawer branch entry, added single-child Stop, lost ACK, keyboard close/focus and account switching.",
    "tests/unit/xscience/delegatedStopContract.test.ts": "Exact coordinates, routes, unconfirmed 404, original body and nested target receipt positive/negative tests.",
    "tests/unit/xscience/delegatedStopController.test.ts": "Exercises reservation, exact retries, account/branch fencing and actual stale rendered button handlers.",
    "tests/unit/xscience/delegatedStopFixtures.ts": "X-Science typed wire, deferred requests and isolated hint storage test fixtures.",
    "packages/desktop/src/common/adapter/pdfReadingContext.ts": "Define and strictly decode Go-owned reading scope, immutable version bindings, selection snapshots and exact pending requests; preserve current owner/incarnation/revision identities.",
    "packages/desktop/src/renderer/components/chat/SendBox/reading/PdfReadingBar.tsx": "Adapt the reference linked-PDF bar, open/remove controls and filename layout to the existing composer controller, including truthful pending and blocked states. Adapted with reference to Open Science6d869cd: src/renderer/src/pages/workspace/ConversationPanel.tsx.",
    "packages/desktop/src/renderer/components/chat/SendBox/reading/PdfReadingComposerPrefix.tsx": "Compose the reading bar, initial-send recovery, attachment suggestion, frozen queue notice and ordinary draft-save failure within the existing SendBox prefix.",
    "packages/desktop/src/renderer/components/chat/SendBox/reading/PdfReadingSuggestion.tsx": "Adapt the automatic-reading suggestion and Keep as attachments action without removing the original attachment or claiming a binding is accepted. Adapted with reference to Open Science6d869cd: src/renderer/src/pages/workspace/ConversationPanel.tsx, src/renderer/src/pages/workspace/workspace-composer-controller.ts.",
    "packages/desktop/src/renderer/components/chat/SendBox/reading/ReadingContextPicker.tsx": "Adapt the source reading picker to authenticated Project ArtifactVersion candidates, scoped search/loading/retry/selection and the three-document limit; do not fabricate Library authority. Adapted with reference to Open Science6d869cd: src/renderer/src/pages/workspace/ReadingContextPicker.tsx.",
    "packages/desktop/src/renderer/components/chat/SendBox/reading/readingContext.css": "Translate the reference linked-bar/picker geometry and light/dark color tokens into scoped CSS using existing application radius/weight tokens. Adapted with reference to Open Science6d869cd: src/renderer/src/pages/workspace/ConversationPanel.tsx, src/renderer/src/pages/workspace/ReadingContextPicker.tsx, src/renderer/src/assets/main.css.",
    "packages/desktop/src/renderer/hooks/chat/sendBoxDraftEdits.ts": "Define ordinary editable-field deltas and snapshot comparison while reserving the exact PDF pending request to the typed conditional draft owner.",
    "packages/desktop/src/renderer/hooks/chat/useComposerLifetime.ts": "Capture a component/account lifetime predicate to fence late draft/upload UI callbacks without granting filesystem or send authority.",
    "packages/desktop/src/renderer/hooks/chat/usePdfReadingAttachments.ts": "Adapt automatic eligible-attachment preparation to the existing composer and its captured send snapshot; fence completion and retain ordinary ineligible attachments.",
    "packages/desktop/src/renderer/hooks/chat/usePdfReadingComposer.ts": "Bind one reading controller to the actual account/conversation and existing PreviewProvider, forwarding canonical invalidations, positions and initial-upload preparation.",
    "packages/desktop/src/renderer/pages/artifact/PdfPageNavigator.tsx": "Add bounded previous/next/direct-page controls to the existing PDF viewer using parsed page counts, not a new PDF engine. Adapted with reference to Open Science6d869cd: src/renderer/src/pages/workspace/previews/renderers/PdfPreview.tsx.",
    "packages/desktop/src/renderer/pages/artifact/usePdfReadingPosition.ts": "Track the actual virtualized PDF viewport and route page/reveal actions through the existing viewer; repeat reveal is an action, not a document reload.",
    "packages/desktop/src/renderer/pages/conversation/Messages/components/MessagePdfReadingContext.tsx": "Adapt the reference historical reading badge to canonical Go snapshots and reopen the original immutable version/page without relinking it. Adapted with reference to Open Science6d869cd: src/renderer/src/pages/workspace/WorkspaceMessageItem.tsx, src/renderer/src/pages/workspace/pdf-reading-reveal.ts.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/PdfReadingPreviewAction.tsx": "Adapt Read with agent/remove controls to a borrowed live-composer or Guid-draft port with immutable identity and late-response fencing. Adapted with reference to Open Science6d869cd: src/renderer/src/pages/workspace/use-pdf-context-action.ts, src/renderer/src/pages/workspace/ConversationPanel.tsx.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/AcpComposerAttachments.tsx": "Extract the existing Acp attachment/directory rendering and removal callbacks, adding PDF open delegation while retaining prior imported/adapted code ancestry. Preserve the original imported/adapted ancestry of the extracted target code.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/useAcpCommandAuthority.ts": "Extract the existing X-Science branch/session-option authority capture from AcpSendBox so ordinary and typed sends retain one implementation. Preserve the original imported/adapted ancestry of the extracted target code.",
    "packages/desktop/src/renderer/pages/guid/hooks/useGuidPdfReading.ts": "Adapt pre-conversation PDF selection and preview position to the existing Guid draft; actual binding remains deferred to the canonical first-message handoff.",
    "packages/desktop/src/renderer/services/runtime/PdfReadingController.ts": "Implement the browser-side single composer intent lifecycle over Go CAS endpoints, exact durable retry feedback, account fences and click-time selection preparation; not a second binding authority.",
    "packages/desktop/src/renderer/services/runtime/pdfReadingContext.ts": "Use the existing authenticated HTTP bridge for strict reading read/mutate/candidate responses and bounded Project candidate batches.",
    "packages/desktop/src/renderer/services/runtime/pdfReadingHandoff.ts": "Carry validated immutable PDF sources/page hints through the existing initial-message handoff and reuse a prepared selection on uncertain send retries.",
    "packages/desktop/src/renderer/services/runtime/pdfReadingPendingState.ts": "Project strict original-draft feedback into recoverable/blocked UI state with complete account/frame/root checks; unavailable is not successful persistence.",
    "packages/desktop/src/renderer/services/runtime/pdfReadingPreview.ts": "Define the borrowed composer preview port and immutable ArtifactVersion preview metadata without adding a second preview or server-state owner.",
    "packages/desktop/src/renderer/services/runtime/pdfReadingSendSnapshot.ts": "Implement click-time reading snapshot and own-link mapping over the Go contract, preserving active unlinked-source position and rejecting external selection changes.",
    "tests/unit/renderer/conversation/PdfReadingController.test.ts": "Cover original controller scope, immutable preparation, retry and lost-acknowledgement behavior with typed contracts.",
    "tests/unit/renderer/conversation/PdfReadingControls.dom.test.tsx": "Exercise source-adapted bar/picker UI states and real controller callbacks.",
    "tests/unit/renderer/conversation/PdfReadingPendingOwner.dom.test.tsx": "Exercise late ACK/refresh, next-click protection, ordinary editing, storage failures and exact pending recovery through actual hooks.",
    "tests/unit/renderer/conversation/pdfReadingDraft.test.ts": "Verify reading draft/selection decoding, source identity and owner-bound persistence data.",
    "tests/unit/renderer/conversation/pdfReadingFixtures.ts": "Provide authored typed reference data and pending-owner fixtures for focused C12 tests, not scientific evidence.",
    "tests/unit/renderer/conversation/pdfReadingHandoff.test.ts": "Verify original initial-message reading preparation, exact retry and known/unknown handoff behavior.",
    "tests/unit/renderer/conversation/pdfReadingTransport.test.ts": "Verify strict Go wire decoders/transport response binding and source validation.",
    "tests/unit/renderer/conversation/useGuidPdfReading.dom.test.tsx": "Exercise the existing Guid draft and shared preview port before canonical frame creation.",
    "tests/unit/renderer/useSendBoxDraftReservedFields.dom.test.ts": "Verify ordinary save/clear cannot remove or resurrect the reserved pending request, including durable absence and nested mutation.",
    "packages/desktop/src/renderer/components/xScience/runtime/ContextCompactionAction.module.css": "Style the compact source-aligned maintenance action with shared tokens and no modal backdrop.",
    "packages/desktop/src/renderer/components/xScience/runtime/ContextCompactionAction.tsx": "Render actual maintenance receipts, exact retry/cancel and explicit capacity recovery without fabricated progress.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/acpStopAction.ts": "Route Stop through the observed maintenance owner only; ordinary Stop does not wait for optional bootstrap.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/useAcpContextCompaction.ts": "Bind one composer maintenance controller to account and frame incarnation, keeping recovery reachable with a draft.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/useAcpRuntimeActions.ts": "Share existing ordinary runtime stop and refresh actions while retaining exact maintenance cancellation.",
    "packages/desktop/src/renderer/services/compaction/contextCompactionContract.ts": "Decode canonical scope, numerical event coordinates, admission facts and actual execution observations strictly.",
    "packages/desktop/src/renderer/services/compaction/ContextCompactionController.ts": "Serialize frozen maintenance requests and cancellation/readback with exact lost-ACK recovery and ordinary-input handoff.",
    "packages/desktop/src/renderer/services/compaction/contextCompactionObservation.ts": "Retire only proven terminal maintenance observations without equating unrelated task busy state with compaction.",
    "packages/desktop/src/renderer/services/compaction/contextCompactionRegistry.ts": "Retain unresolved renderer-local request owners under a bounded registry without an additional durable ledger.",
    "packages/desktop/src/renderer/services/compaction/contextCompactionService.ts": "Use the existing authenticated transport for typed canonical maintenance admit, read and exact cancellation routes.",
    "packages/desktop/src/renderer/services/compaction/contextCompactionTypes.ts": "Declare canonical maintenance request, receipt, capability and presentation types without execution authority in the renderer.",
    "tests/unit/renderer/conversation/contextCompactionCapacityTestFixture.ts": "Verify or support the directly scoped context-compaction lifecycle and interaction contract in contextCompactionCapacityTestFixture.ts, without scientific task replay or user artifacts.",
    "tests/unit/renderer/conversation/ContextCompactionController.test.ts": "Verify or support the directly scoped context-compaction lifecycle and interaction contract in ContextCompactionController.test.ts, without scientific task replay or user artifacts.",
    "tests/unit/renderer/conversation/contextCompactionObservation.test.ts": "Verify or support the directly scoped context-compaction lifecycle and interaction contract in contextCompactionObservation.test.ts, without scientific task replay or user artifacts.",
    "tests/unit/renderer/conversation/contextCompactionRegistry.test.ts": "Verify or support the directly scoped context-compaction lifecycle and interaction contract in contextCompactionRegistry.test.ts, without scientific task replay or user artifacts.",
    "tests/unit/renderer/conversation/contextCompactionService.test.ts": "Verify or support the directly scoped context-compaction lifecycle and interaction contract in contextCompactionService.test.ts, without scientific task replay or user artifacts.",
    "tests/unit/renderer/conversation/manualContextCompactionFixtures.ts": "Verify or support the directly scoped context-compaction lifecycle and interaction contract in manualContextCompactionFixtures.ts, without scientific task replay or user artifacts.",
    "packages/desktop/src/renderer/services/runtime/sessionPermissionGrants.ts": "Validate typed owner-scoped session grant snapshots and frozen exact-generation revocation receipts.",
    "packages/desktop/src/renderer/services/runtime/sessionPermissionGrantsController.ts": "Own one scoped live grant observer and frozen revocation command across desktop and mobile presentations.",
    "packages/desktop/src/renderer/hooks/xScience/runtime/useSessionPermissionGrants.ts": "Bind the single session grant controller to actual account, frame incarnation and composer lifetime.",
    "packages/desktop/src/renderer/components/xScience/runtime/SessionPermissionGrants.tsx": "Render compact observed grant categories with exact revocation, uncertainty, conflicts and readback.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/useAcpSessionPermissionGrants.tsx": "Adapt actual composer identity and share the existing mobile permission presentation without duplicate state ownership.",
    "packages/desktop/src/renderer/components/notifications/NotificationBell.tsx": "Expose the source-reference compact message-center trigger from the actual toolbar and account footer using one account-scoped store.",
    "packages/desktop/src/renderer/components/notifications/NotificationInboxList.tsx": "Render verified human notifications and exact observed read receipts without consuming agent delivery or approving a task.",
    "packages/desktop/src/renderer/components/notifications/NotificationInboxOverlay.tsx": "Own one compact inbox overlay with keyboard focus, same-click outside dismissal and a transparent mobile sheet.",
    "packages/desktop/src/renderer/components/notifications/notificationInbox.module.css": "Style the source-reference 36-pixel trigger and compact notification panel without adding a black mask.",
    "packages/desktop/src/renderer/services/notifications/NotificationInboxStore.ts": "Own one account-fenced inbox observer, request reservation and retained exact marking retry across both UI entries.",
    "packages/desktop/src/renderer/services/notifications/notificationInboxActions.ts": "Freeze read mutations and verify exact target incarnation before navigation without starting, approving or cancelling execution.",
    "packages/desktop/src/renderer/services/notifications/notificationInboxObservation.ts": "Invalidate the single human inbox from existing canonical events and reconnect without a competing notification delivery queue.",
    "packages/desktop/src/renderer/services/notifications/notificationInboxService.ts": "Validate the bounded inbox wire contract and use the existing authenticated transport for scoped read and marking requests.",
    "packages/desktop/src/renderer/services/notifications/notificationInboxTypes.ts": "Declare immutable human-inbox snapshot, scope, mutation and explicit incomplete-history state types.",
    "tests/unit/renderer/NotificationInboxEntries.dom.test.tsx": "Verify or support the directly scoped human-inbox protocol, account isolation, actual entries and interaction behavior in NotificationInboxEntries.dom.test.tsx.",
    "tests/unit/xscience/NotificationInbox.dom.test.tsx": "Verify or support the directly scoped human-inbox protocol, account isolation, actual entries and interaction behavior in NotificationInbox.dom.test.tsx.",
    "tests/unit/xscience/NotificationInboxStore.test.ts": "Verify or support the directly scoped human-inbox protocol, account isolation, actual entries and interaction behavior in NotificationInboxStore.test.ts.",
    "tests/unit/xscience/notificationInboxDomFixture.ts": "Verify or support the directly scoped human-inbox protocol, account isolation, actual entries and interaction behavior in notificationInboxDomFixture.ts.",
    "tests/unit/xscience/notificationInboxFixtures.ts": "Verify or support the directly scoped human-inbox protocol, account isolation, actual entries and interaction behavior in notificationInboxFixtures.ts.",
    "tests/unit/xscience/notificationInboxService.test.ts": "Verify or support the directly scoped human-inbox protocol, account isolation, actual entries and interaction behavior in notificationInboxService.test.ts.",
    "packages/desktop/src/renderer/components/chat/ArchiveUndoNotice.tsx": "Render an account-bound archive Undo receipt without a modal or a second conversation mutation authority.",
    "packages/desktop/src/renderer/components/chat/archiveUndo.css": "Style the compact nonmodal archive Undo notice with shared visual tokens.",
    "packages/desktop/src/renderer/pages/conversation/GroupedHistory/hooks/useConversationArchive.ts": "Bind the real conversation-row archive interaction to the canonical presentation CAS and exact Undo receipt.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/notebook/LiveNotebookPreview.tsx": "Render the interactive Notebook against one account-and-frame controller while preserving the read-only file preview.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/notebook/NotebookCodeCell.tsx": "Provide the source-reference compact editor with IME-safe submit and exact execution cancellation.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/notebook/NotebookPreviewContent.tsx": "Choose live Notebook versus file-only preview from actual entry identity without inventing an executable target.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/notebook/NotebookRequests.tsx": "Present durable queued, accepted, terminal and unknown execution receipts without claiming dispatch is completion.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/notebook/notebook.module.css": "Style the compact Notebook editor, output and resizable twenty-percent terminal split.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/notebook/useLiveNotebook.ts": "Subscribe view consumers to one identity-bound Notebook controller without cancelling execution when the view closes.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/notebook/useNotebookSplit.ts": "Own bounded keyboard and pointer resizing for the reference Notebook terminal split.",
    "packages/desktop/src/renderer/pages/settings/ArchivedSettings.tsx": "Render the canonical archived-conversation page with exact restore, pagination, retained failures and no unsafe permanent delete.",
    "packages/desktop/src/renderer/pages/settings/archived/archivedSettings.css": "Style the compact archived-conversation settings page using shared visual tokens and no dark backdrop.",
    "packages/desktop/src/renderer/pages/settings/archived/useArchivedConversations.ts": "Own account-fenced archive paging and restore refresh without converting unknown presentation state into write authority.",
    "packages/desktop/src/renderer/services/notebook/NotebookSessionActions.ts": "Serialize frozen manual Notebook commands and cancellation intent with immutable idempotency identifiers.",
    "packages/desktop/src/renderer/services/notebook/NotebookSessionController.ts": "Compose the single account-and-frame Notebook lifecycle and shared observer used by both preview layouts.",
    "packages/desktop/src/renderer/services/notebook/NotebookSessionReads.ts": "Recover and observe durable execution receipts through bounded read-only pending pages and exact kernel inventory.",
    "packages/desktop/src/renderer/services/notebook/NotebookSessionStore.ts": "Own one Notebook reservation, immutable retry command and account-generation fence without a competing execution queue.",
    "packages/desktop/src/renderer/services/notebook/manualNotebookContract.ts": "Validate the owner-and-incarnation-bound manual Notebook inventory and scoped pending protocol.",
    "packages/desktop/src/renderer/services/notebook/manualNotebookReceipt.ts": "Validate durable execution receipts, terminal states and bounded stdout and error records.",
    "packages/desktop/src/renderer/services/notebook/manualNotebookService.ts": "Use only the typed canonical manual Notebook submit, read and cancel endpoints.",
    "packages/desktop/src/renderer/services/notebook/manualNotebookTypes.ts": "Declare shared manual Notebook scope, exact live target, intent and receipt types.",
    "packages/desktop/src/renderer/services/notebook/notebookPreviewNavigation.ts": "Open the same live Notebook identity from compute controls without replacing artifact preview authority.",
    "packages/desktop/src/renderer/services/runtime/conversationArchive.ts": "Validate canonical archive CAS commands and read-only presentation projections behind the existing authenticated transport.",
    "packages/desktop/src/renderer/services/runtime/conversationArchiveOperations.ts": "Serialize account-bound archive mutations and exact Undo receipts while preserving drafts and task state.",
    "tests/unit/conversation/runtime/conversationArchive.test.ts": "Verify or support the directly scoped archive and manual Notebook contract in conversationArchive.test.ts, without user artifacts or scientific result substitution.",
    "tests/unit/settings/ArchivedSettings.dom.test.tsx": "Verify or support the directly scoped archive and manual Notebook contract in ArchivedSettings.dom.test.tsx, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/LiveNotebookExecution.dom.test.tsx": "Verify or support the directly scoped archive and manual Notebook contract in LiveNotebookExecution.dom.test.tsx, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/LiveNotebookPresentation.dom.test.tsx": "Verify or support the directly scoped archive and manual Notebook contract in LiveNotebookPresentation.dom.test.tsx, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/LiveNotebookRecovery.dom.test.tsx": "Verify or support the directly scoped archive and manual Notebook contract in LiveNotebookRecovery.dom.test.tsx, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/ManualNotebookPanelBoard.dom.test.tsx": "Verify or support the directly scoped archive and manual Notebook contract in ManualNotebookPanelBoard.dom.test.tsx, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/ManualNotebookPreviewEntry.dom.test.tsx": "Verify or support the directly scoped archive and manual Notebook contract in ManualNotebookPreviewEntry.dom.test.tsx, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/ManualNotebookPreviewIdentity.dom.test.tsx": "Verify or support the directly scoped archive and manual Notebook contract in ManualNotebookPreviewIdentity.dom.test.tsx, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/NotebookSessionController.test.ts": "Verify or support the directly scoped archive and manual Notebook contract in NotebookSessionController.test.ts, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/manualNotebookControllerFixture.ts": "Verify or support the directly scoped archive and manual Notebook contract in manualNotebookControllerFixture.ts, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/manualNotebookDomHarness.tsx": "Verify or support the directly scoped archive and manual Notebook contract in manualNotebookDomHarness.tsx, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/manualNotebookFixtures.ts": "Verify or support the directly scoped archive and manual Notebook contract in manualNotebookFixtures.ts, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/manualNotebookService.test.ts": "Verify or support the directly scoped archive and manual Notebook contract in manualNotebookService.test.ts, without user artifacts or scientific result substitution.",
    "tests/unit/xscience/manualNotebookTarget.test.ts": "Verify or support the directly scoped archive and manual Notebook contract in manualNotebookTarget.test.ts, without user artifacts or scientific result substitution.",
    "packages/desktop/src/common/adapter/conversationInputReceipt.ts": "Decode explicit durable input receipts separately from current runtime and bind them to the receiving conversation.",
    "packages/desktop/src/common/adapter/hostGrantFileReference.ts": "Encode and validate explicit host-grant file references before guarded task-owned snapshot materialization.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/PreviewPanel/usePreviewArtifactActionScope.ts": "Fence immutable preview actions by the selected tab, artifact version, account and opening identity.",
    "packages/desktop/src/renderer/pages/conversation/GroupedHistory/hooks/useSessionNotebook.ts": "Read the existing execution log with account/opening fencing and exact-cursor pagination and retry, without owning a kernel.",
    "packages/desktop/src/renderer/pages/conversation/GroupedHistory/sessionExportAvailability.ts": "Share the canonical runtime and local submission availability contract between session export actions and their menu entry.",
    "packages/desktop/src/renderer/services/runtime/asideOperation.ts": "Own one frozen UI operation and its verified creation, followup, cancellation and recovery receipts using existing runtime APIs.",
    "packages/desktop/src/renderer/services/runtime/asidePresentation.ts": "Retain bounded owner-scoped presentation hints without treating stored values as execution or cancellation authority.",
    "packages/desktop/src/renderer/components/chat/BtwOverlay/useAsideRuntimeView.ts": "Observe canonical child messages and runtime progress without polling only final output or imposing an execution deadline.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/useAcpAsideController.tsx": "Compose a single side-chat owner and consume only the exact accepted draft or queued item while preserving newer input.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/useAcpConversationSkillController.tsx": "Compose existing Skill admission, catalog and source-aligned controls outside the composer while retaining the shared execution authority and exact retry identity.",
    "packages/desktop/src/renderer/components/media/conversationControlMenu.css": "Scope source-aligned conversation menu sizing, icons and both appearance states without creating a global overlay authority.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/useConversationSkillTask.ts": "Admit a typed ordinary-agent Skill continuation, fence duplicate and stale frontend actions and refresh only actual catalogs.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/ConversationSkillTaskStatus.tsx": "Present only actionable control submission or catalog-read failures; real execution and publication remain transcript-owned.",
    "tests/unit/renderer/conversation/useConversationSkillTask.dom.test.tsx": "Verify exact active-input identity, hidden controls, same-runtime submission tickets and scope-safe refresh/retry behavior.",
    "public/storage-namespace-upgrade.js": "Transfer only explicitly owned historical browser keys before theme/module initialization, preserving conflicting values and showing bounded recovery diagnostics.",
    "scripts/molstarPickingPatch.ts": "Keep explicit molecular picks synchronous when the locked Molstar hover GPU read is pending, consistently in production and development bundles.",
    "packages/desktop/src/common/config/productIdentity.ts": "Project the singular root product identity into the browser and centralize the current public repository and original brand mark.",
    "packages/desktop/src/renderer/components/branding/ProductBrand.tsx": "Render one original approved mark and root-derived name across current workbench entry points.",
    "packages/desktop/src/renderer/components/branding/ProductBrand.css": "Keep the shared mark unmodified and coordinate compact and prominent responsive brand geometry.",
    "public/branding/x-science-mark.png": "Use the byte-identical first-party orange mark, with source, revision and checksum recorded in the accompanying provenance.",
    "public/branding/x-science-favicon.png": "Use the byte-identical approved browser icon, retaining its 32px dimensions and source checksum.",
    "public/branding/x-science-touch-icon.png": "Use the byte-identical approved 180px touch icon with its original source checksum.",
    "public/branding/x-science-logo.provenance.json": "Record the original user-approved logo origin, exact source revision, dimensions and copied-asset checksums without rewriting historical artwork provenance.",
    "tests/unit/renderer/ProductBrand.dom.test.tsx": "Verify one readable current name, an unchanged decorative mark and coordinated compact/prominent presentations.",
    "tests/unit/renderer/XScienceIdentity.test.ts": "Verify current public identity, browser metadata, PWA references and exact original mark/browser-icon hashes.",
    "tests/unit/renderer/compositeControlLabels.test.ts": "Reject implicit HTML label forwarding around Arco composite pickers across renderer modules while retaining correct native-input labels and existing control implementations.",
    "tests/fixtures/structureLigands.ts": "Share explicitly synthetic receptor/ligand coordinates across parsed-state and native viewer selection regressions; never substitute them for user artifacts or research results.",
    "tests/fixtures/anndata-categorical/README.md": "Document the first-party synthetic AnnData categorical metadata scaffold, locked-parser regression use, optional regeneration requirements, and scientific-validation limits.",
    "tests/fixtures/anndata-categorical/categorical-template.h5ad": "Provide a tiny first-party synthetic AnnData categorical metadata scaffold for real-parser composition regressions, without user records or research results.",
    "tests/fixtures/anndata-categorical/conform-ordered-bool.py": "Regenerate the scalar HDF5 boolean enum in the first-party synthetic test scaffold using optional Python standard-library and system HDF5 tooling, without a routine test runtime dependency.",
    "tests/fixtures/anndata-categorical/create-template.mjs": "Regenerate the bounded first-party synthetic AnnData categorical metadata scaffold with locked h5wasm and the optional local enum helper, without user artifacts or research results.",
    "tests/fixtures/sequence-joined-location/contiguous.gbk": "Provide a small first-party synthetic contiguous GenBank feature fixture as a valid control for the compound-location preview guard.",
    "tests/fixtures/sequence-joined-location/joined.gbk": "Provide a small first-party synthetic joined GenBank feature fixture for truthful unsupported-location preview behavior, without user records or research results.",
    "tests/fixtures/sequence-joined-location/origin.gbk": "Provide a small first-party synthetic origin-spanning joined GenBank feature fixture for compound-location rejection coverage.",
    "tests/fixtures/sequence-joined-location/reverse.gbk": "Provide a small first-party synthetic reverse-strand joined GenBank feature fixture for compound-location rejection coverage.",
    "tests/fixtures/sequence-joined-location/dispatch-comment.jbei": "Provide a small first-party synthetic JBEI fixture with a leading comment to verify locked-parser dispatch priority is preserved.",
    "packages/desktop/src/common/chat/roundSummary.ts": "Validate durable per-round completion timing and model usage shared by live and historical messages; unavailable telemetry never erases answer text.",
    "packages/desktop/src/common/chat/largeToolResultReference.ts": "Validate immutable same-origin large-result references once for transcript summaries and lazy detail loading; previews never replace source evidence.",
    "packages/desktop/src/common/chat/toolProgressObservation.ts": "Extract and adapt the existing migrated chatLib.ts/normalizeToolCall.ts progress model and history/live same-ID merge into one pure observation helper, retaining imported-source provenance while adding X-Science resource-admission validation and revision fencing without another cache, timer, transport, request, or lifecycle owner.",
    "README.md": "Document the browser-only source boundary, dependency authority, scoped checks, and isolated real-backend testing prerequisites.",
    "packages/desktop/src/renderer/styles/tokens.css": "Own the single canonical visual-primitive scale (space, radius, elevation, type, weight, stacking) that every renderer module references instead of restating literals.",
    "packages/desktop/src/renderer/pages/settings/skills/SkillLibraryCard.tsx": "Render installed and draft skills with complete readable copy, declared-category icons and isolated keyboard actions.",
    "packages/desktop/src/renderer/pages/settings/skills/SkillFilePreview.tsx": "Render original Markdown and source files through distinct safe read-only previews.",
    "packages/desktop/src/renderer/pages/settings/skills/useSkillDetailSession.ts": "Bind Skill detail requests to one committed opening, including same-name reopen and mode changes, without altering service authorization.",
    "packages/desktop/src/renderer/pages/settings/skills/useSkillDetailFiles.ts": "Own ordered source-file reads, visible failure, exact-read retry and retained draft content behind the existing Skill library services.",
    "packages/desktop/src/renderer/pages/settings/skills/useSkillDetailMutations.ts": "Serialize original Skill mutations, fence late receipts and distinguish completed changes from retryable library-refresh failure without replaying writes.",
    "packages/desktop/src/renderer/pages/settings/skills/skillSourceLabel.ts": "Share truthful localized source labels between library cards and detail views without claiming authorship.",
    "packages/desktop/src/renderer/pages/settings/skills/SkillLibraryToolbar.tsx": "Provide one responsive search and research-field toolbar with accessible source/status refinement controls.",
    "packages/desktop/src/renderer/pages/settings/skills/SkillMarketModal.tsx": "Reuse the existing pinned-import market in a bounded dialog without resetting the installed library search or filters.",
    "packages/desktop/src/renderer/pages/settings/AppearanceSettings/presets/warm.css": "Keep the warm palette under its visual family name while retaining the original import record.",
    "packages/desktop/src/renderer/pages/settings/AppearanceSettings/presets/cool.css": "Keep the cool palette under its visual family name while retaining the original import record.",
    "packages/desktop/src/renderer/styles/workspace-theme.css": "Provide the shared visual shell under a product-neutral filename and CSS custom-property namespace.",
    "packages/desktop/src/common/chat/textPublicationCoverage.ts": "Bind X-Science text replay suppression to conversation, segment, and durable publication coverage without discarding unseen publications.",
    "tests/integration/xscienceControlledLlmFixture.ts": "Provide the renamed controlled-provider integration fixture with explicit simulated-model provenance and real transcript transport.",
    "package-lock.json": "Deterministic npm build dependency lock.",
    "packages/desktop/src/common/adapter/ipcBridgeTypes.ts": "Define shared typed IPC facade contracts without creating a second transport authority.",
    "packages/desktop/src/common/adapter/ipcClientSettings.ts": "Centralize settings and model-client IPC construction behind the stable facade.",
    "packages/desktop/src/common/adapter/ipcConversationClient.ts": "Own conversation, transcript, realtime, and durable event IPC bindings as one domain client.",
    "packages/desktop/src/common/adapter/ipcCoreClients.ts": "Compose small core IPC clients without retaining transport business logic in the facade.",
    "packages/desktop/src/common/adapter/ipcDatabaseClient.ts": "Own database inspection IPC bindings as one domain client.",
    "packages/desktop/src/common/adapter/ipcFileClient.ts": "Own file and workspace IPC bindings as one domain client.",
    "packages/desktop/src/common/adapter/ipcOperationsClient.ts": "Own operational and lifecycle IPC bindings as one domain client.",
    "packages/desktop/src/common/adapter/ipcPreviewClient.ts": "Own preview and artifact-view IPC bindings as one domain client.",
    "packages/desktop/src/common/adapter/ipcProviderClient.ts": "Own provider, credential, and compute IPC bindings as one domain client.",
    "packages/desktop/src/common/adapter/ipcRuntimeClient.ts": "Own runtime control and execution IPC bindings as one domain client.",
    "packages/desktop/src/common/adapter/conversationRuntimeProtocol.ts": "Validate canonical Go conversation runtime and terminal envelopes before they enter renderer state.",
    "packages/desktop/src/common/adapter/messageStreamProtocol.ts": "Validate canonical streamed message envelopes without guessing terminal or artifact state.",
    "packages/desktop/src/common/adapter/realtimeClient.ts": "Own one identity-bound authenticated realtime connection with fenced socket, listener, cursor, and timer resources.",
    "packages/desktop/src/common/adapter/realtimeProtocol.ts": "Validate the closed realtime control, direct, and durable event wire contract.",
    "packages/desktop/src/common/adapter/realtimeRenderer.ts": "Construct the renderer realtime runtime from the authenticated same-origin endpoint without import-time connection authority.",
    "packages/desktop/src/common/adapter/realtimeRuntime.ts": "Route validated durable and direct events through identity-epoch subscriptions and a stable external-store snapshot.",
    "packages/desktop/src/common/utils/errorRedaction.ts": "Map untrusted frontend failures to bounded diagnostic codes without leaking payloads or owner identity.",
    "packages/desktop/src/renderer/services/onboardingDraft.ts": "Persist bounded owner-scoped capability and scientific-runtime choices across authentication and refresh without retaining uploaded file bytes.",
    "packages/desktop/src/renderer/services/onboardingSuggestions.ts": "Run the authoritative bounded onboarding suggestion and AskUser flow without treating user profile content as instructions.",
    "packages/desktop/src/renderer/components/common/AccessibleActionDialog.tsx": "Provide a keyboard and focus-safe confirmation dialog for migrated destructive actions.",
    "packages/desktop/src/renderer/components/common/AccessibleContentDialog.tsx": "Provide a keyboard and focus-safe bounded content dialog for migrated scientific and runtime detail.",
    "packages/desktop/src/renderer/components/xScience/runtime/XScienceExecutionLogContent.tsx": "Render bounded structured execution logs without exposing raw runtime failures.",
    "packages/desktop/src/renderer/components/xScience/runtime/longTaskStatusModel.ts": "Normalize long-task lifecycle and recovery state outside React components.",
    "packages/desktop/src/renderer/hooks/context/RealtimeContext.tsx": "Bind authoritative authentication identity to the single renderer realtime runtime and stable status store.",
    "packages/desktop/src/renderer/pages/artifact/AudioPreview.tsx": "Render bounded owner-scoped audio Artifact versions with native browser controls and explicit failure states.",
    "packages/desktop/src/renderer/pages/artifact/VideoPreview.tsx": "Render bounded owner-scoped video Artifact versions with native browser controls and explicit failure states.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/XScienceMcpAppArtifactViewer.tsx": "Resolve exact Artifact-version inputs for the isolated read-only MCP App viewer.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/XScienceMcpAppViewer.css": "Style the isolated MCP App host, loading state, and explicit read-only notice.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/XScienceMcpAppViewer.tsx": "Host verified MCP App resources in a capability-minimal origin-bound iframe.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/mcpAppHostProtocol.ts": "Validate the bounded source-, origin-, session-, and generation-bound MCP App host protocol.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/mcpAppSaveContracts.ts": "Define one MCP App save-and-pin contract so editable viewers cannot invent competing persistence paths.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/StructureObjectList.tsx": "Render one composition-derived protein and ligand list with shared visibility, color, select-all, and clear controls.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/XScienceStructureViewer.css": "Style the unified molecular composition panel and collapsed 2D controls without resizing or replacing the established preview canvas.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/molstarDockingLigandShape.ts": "Normalize docking ligand shape and alternate-conformer selection for the shared Molstar structure path.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/molstarElectrostaticTheme.ts": "Provide one Molstar electrostatic color authority reused by protein and ligand surface representations.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/molstarInteractionAnchors.ts": "Render chemically meaningful multi-atom interaction centers from Molstar feature topology without structure-specific coordinate overrides.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/molstarStructureEngine.ts": "Own format-neutral Molstar loading, structure composition, surface styling, ligand extraction, and object visibility operations.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/structureElectrostaticMap.ts": "Validate and decode bounded aligned protein and ligand APBS maps before they enter the unified molecular preview.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/structureComposition.ts": "Classify parsed molecular structures into protein and primary-ligand components while excluding crystallization additives and duplicate conformers.",
    "packages/desktop/src/renderer/assets/icons/python-kernel.svg": "Add the text-free Python kernel icon used by the single governed Compute runtime panel.",
    "packages/desktop/src/renderer/pages/project/projectArtifactLibraryModel.ts": "Retain the imported pure artifact-library model under a module stem that cannot collide with the ProjectArtifactLibrary component on case-insensitive filesystems.",
    "packages/desktop/src/renderer/pages/settings/components/settingsPresentation.ts": "Shared Unicode-safe compaction for dense settings descriptions.",
    "packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/scientificPreviewError.ts": "Shared localized and non-sensitive error mapping for scientific preview failures.",
    "packages/desktop/src/renderer/pages/conversation/Messages/components/artifactReferenceModel.ts": "Resolve exact canonical message artifact_refs without tool-name, filename, path, or latest-version guessing.",
    "packages/desktop/src/renderer/components/Markdown/markdownImageUrlPolicy.ts": "Allow only bounded inert raster data URLs from streamed model and tool image blocks while rejecting active SVG and malformed payloads.",
    "packages/desktop/src/renderer/pages/conversation/Messages/components/MessageScientificFiles.css": "Own the compact workspace final-file tray dimensions and state without cross-module tool CSS overrides.",
    "packages/desktop/src/renderer/services/csrf.ts": "Central same-origin double-submit CSRF cookie and request-header implementation for the Go Web runtime.",
    "packages/desktop/src/renderer/services/i18n/i18n-keys.d.ts": "Generated i18n key declarations from migrated locale sources.",
    "packages/desktop/src/renderer/services/xScienceFileImportError.ts": "Map cloud, local, and SSH file-import failures to bounded recoverable user-facing states.",
    "packages/desktop/src/renderer/services/xScienceConversationTitle.ts": "Create bounded scientific conversation titles without exposing raw task content or adding model-side title authority.",
    "packages/desktop/src/renderer/services/agents/xScienceExpertLocalization.ts": "Localize authoritative expert profiles without changing their runtime identities or capabilities.",
    "public/theme-init.js": "Restore the validated cached light or dark appearance before renderer startup without allowing inline scripts.",
    "public/app-boot.js": "Provide bounded localized startup recovery before the main module loads, with explicit reload and cleanup after mounting.",
    "scripts/rendererBoot.ts": "Project root identity and shared locale resources into the pre-JavaScript document without inline executable code.",
    "public/genomes/ucsc/NOTICE.txt": "Record the UCSC chromosome-size attribution and redistribution notice shipped with offline genome references.",
    "public/genomes/ucsc/PROVENANCE.json": "Pin the source URLs, hashes, and retrieval provenance for packaged UCSC genome references.",
    "public/genomes/ucsc/hg19.chrom.sizes": "Package checksum-bound hg19 chromosome sizes for offline genome preview.",
    "public/genomes/ucsc/hg38.chrom.sizes": "Package checksum-bound hg38 chromosome sizes for offline genome preview.",
    "public/genomes/ucsc/mm10.chrom.sizes": "Package checksum-bound mm10 chromosome sizes for offline genome preview.",
    "public/genomes/ucsc/mm39.chrom.sizes": "Package checksum-bound mm39 chromosome sizes for offline genome preview.",
    "public/rdkit/rdkit-worker.js": "Confine lockfile-pinned RDKit code generation to the dedicated worker and its path-specific production CSP.",
    "tests/integration/xscienceRealConversationFixture.ts": "Create and remove disposable real conversations so integration tests do not depend on deployment-specific migrated IDs.",
    "tests/unit/common/i18nSourceAudit.test.ts": "AST-based guard against unresolved translation keys, raw user-facing literals, and unsafe error notifications.",
    "tests/unit/common-adapter/conversationRuntimeProtocol.test.ts": "Verify exact accepted and rejected canonical runtime and terminal event envelopes.",
    "tests/unit/common-adapter/messageStreamProtocol.test.ts": "Verify strict streamed message, terminal, and artifact reference decoding.",
    "tests/unit/common-adapter/realtimeClient.test.ts": "Verify identity, socket, timer, cursor, reconnect, cleanup, and durable delivery authority under reentry.",
    "tests/unit/common-adapter/realtimeProtocol.test.ts": "Verify the closed realtime wire envelope and size boundary.",
    "tests/unit/common-adapter/realtimeRenderer.test.ts": "Verify Web and host endpoint resolution without import-time connection side effects.",
    "tests/unit/common-adapter/realtimeRuntime.test.ts": "Verify identity epochs, composite subscriptions, listener isolation, and reconnected delivery.",
    "tests/unit/common/modelCapabilities.test.ts": "Verify provider-neutral model capability inference without provider-name hard coding.",
    "tests/unit/renderer/artifactReferenceModel.test.ts": "Verify exact artifact/version association and fail-closed malformed canonical references.",
    "tests/unit/build/viteStaticCopyPaths.test.ts": "Verify Windows static-copy inputs are normalized before entering the glob boundary used by the production build.",
    "tests/unit/i18nTestUtils.tsx": "Shared deterministic bilingual renderer for localized component tests.",
    "tests/unit/workspace/useWorkspaceEvents.dom.test.ts": "Verify long-running workspace event state remains bounded, ordered, and recoverable in the renderer.",
    "tests/unit/chat/MessageSkillSuggest.test.ts": "Verify malformed Skill suggestions remain non-actionable and expose a localized recovery state.",
    "tests/unit/chat/MessageToolCall.dom.test.tsx": "Verify accessible keyboard and status behavior for migrated tool-call messages.",
    "tests/unit/chat/XScienceDelegateDockRecovery.dom.test.tsx": "Verify failed child Agent operations expose deterministic retry and recovery behavior.",
    "tests/unit/previews/ImageViewer.dom.test.tsx": "Verify localized image preview controls and failure behavior.",
    "tests/unit/previews/PDFViewer.dom.test.tsx": "Verify localized PDF preview controls and failure behavior.",
    "tests/unit/previews/PreviewI18nParity.test.ts": "Keep the primary Chinese and English preview resources structurally aligned.",
    "tests/unit/previews/molstarStructureEngine.test.ts": "Verify unified molecular parsing, composition classification, electrostatic surfaces, ligand extraction, and visibility behavior.",
    "tests/unit/previews/structureElectrostaticMap.test.ts": "Verify aligned dual-component APBS response validation, bounded decoding, and malformed-map rejection.",
    "tests/unit/previews/XScienceMcpAppViewer.dom.test.tsx": "Verify MCP App iframe confinement, loading, errors, read-only state, and lifecycle cleanup.",
    "tests/unit/previews/genomeReferenceModel.test.ts": "Verify packaged genome reference identity, bounds, and deterministic chromosome lookup.",
    "tests/unit/previews/mcpAppHostProtocol.test.ts": "Verify bounded MCP App host protocol validation and exact input round trips.",
    "tests/unit/renderer/FilesOverlay.dom.test.tsx": "Verify localized file-overlay states and actions.",
    "tests/unit/renderer/markdownImageUrlPolicy.test.ts": "Verify streamed inline raster images remain bounded and active or malformed data URLs fail closed.",
    "tests/unit/renderer/FirstUseGate.dom.test.tsx": "Verify authenticated first-use recovery, retry, and preserved onboarding draft state.",
    "tests/unit/renderer/AccessibleContentDialog.dom.test.tsx": "Verify focus, keyboard dismissal, labels, and content bounds for the shared dialog.",
    "tests/unit/renderer/conversation/useConversationRuntimeView.dom.test.tsx": "Verify runtime hydration, blocking failures, retries, and canonical terminal state in the conversation view.",
    "tests/unit/renderer/realtimeContext.dom.test.tsx": "Verify authentication identity transitions construct and dispose exactly one realtime authority.",
    "tests/unit/renderer/onboardingDraft.dom.test.ts": "Verify bounded owner-scoped onboarding and scientific-runtime draft persistence, corruption handling, and identity isolation.",
    "tests/unit/renderer/onboardingSuggestions.dom.test.ts": "Verify authoritative onboarding suggestions, timeout, cancellation, and strict AskUser option validation.",
    "tests/unit/renderer/projectArtifactLibraryModel.dom.test.ts": "Retain the imported artifact-library model regression under a module stem distinct from the component test on case-insensitive filesystems.",
    "tests/web-e2e/xscienceStreamingParity.e2e.ts": "Verify localized thinking, live tool stdout, inline images, and exact final artifact cards in desktop and narrow real browser paths.",
    "tests/unit/renderer/XScienceNotesModal.dom.test.tsx": "Verify localized notes lifecycle and failure states.",
    "tests/unit/renderer/XScienceSessionOptionsMenu.dom.test.tsx": "Retain the imported session-options component regression under its exact component name without colliding with the service test on case-insensitive filesystems.",
    "tests/unit/renderer/XScienceTextArtifactViewer.dom.test.tsx": "Verify localized text-artifact rendering and actions.",
    "tests/unit/renderer/hooks/useMcpOAuth.dom.test.ts": "Verify browser popup, polling, blocked-window, and timeout behavior for Go MCP OAuth.",
    "tests/unit/settings/LanguageSwitcher.dom.test.tsx": "Verify the complete offline language catalog and persisted language switching.",
    "tests/unit/settings/settingsPresentation.test.ts": "Verify Unicode-safe settings description compaction and prefix removal.",
    "tests/unit/settings/settingsI18nTestUtils.tsx": "Expose the shared localized test renderer from the settings test boundary.",
    "tests/unit/xscience/XScienceUserMessageActions.dom.test.tsx": "Verify localized edit, retry, and branch actions on user messages.",
    "tests/unit/xscience/XScienceBranchMenu.dom.test.tsx": "Verify branch loading, selection, unavailable state, and bounded failures.",
    "tests/unit/xscience/conversationBranches.test.ts": "Verify closed branch capability and branch coordinate contracts.",
    "tests/unit/xscience/expertProfileLocalization.test.ts": "Verify expert localization preserves authoritative profile fields and fallbacks.",
    "tests/unit/xscience/longTaskStatusModel.test.ts": "Verify long-task lifecycle normalization and invalid-state rejection.",
    "tests/unit/xscience/fileImportError.test.ts": "Verify bounded file-import error classification without leaking raw failures.",
    "tests/unit/xscience/xScienceHttp.test.ts": "Verify the authenticated Go HTTP boundary, fixed errors, and request ownership.",
    "tests/unit/web-e2e/officialChromeContract.test.ts": "Verify official Google Chrome identity, loopback CDP, and disposable profile acceptance boundaries.",
    "tests/web-e2e/xScienceScientificFixture.ts": "Create, upload, address, and delete disposable scientific artifacts through authenticated Go APIs.",
    "tests/web-e2e/xscienceGoConversationLifecycle.e2e.ts": "Verify the Go-owned project, conversation, message, realtime, and reload path against the production browser host.",
    "tests/web-e2e/xscienceGoWebCredentials.ts": "Centralize canonical and legacy-compatible credentials for authenticated Go Web browser acceptance tests.",
    "tests/web-e2e/globalSetup.ts": "Prepare authenticated first-run preferences through real Go APIs before each browser acceptance run.",
    "tests/web-e2e/officialChromeContract.ts": "Define the strict official Google Chrome executable, CDP endpoint, and disposable-profile contract.",
    "tests/web-e2e/officialChromeTest.ts": "Bind Playwright acceptance to one verified official Google Chrome CDP session and isolated context.",
    "tests/web-e2e/rdkitProduction.e2e.ts": "Verify real RDKit worker rendering, pixels, MIME, cache, and path-specific CSP under the production Go host.",
    "tests/web-e2e/xscienceGoFrameFixture.ts": "Apply deterministic frame and delegation state through the compiled Go browser-test fixture.",
    "tests/web-e2e/xscienceMessageProjectionRecovery.e2e.ts": "Verify durable cancel/resume history preserves the final answer and exact in-app artifact preview after refresh through the real Go fixture.",
    "tests/web-e2e/xscienceAskUser.e2e.ts-snapshots/ask-user-typed-desktop-official-windows-chrome.png": "Capture the current typed AskUser fixture in real Windows Chrome at desktop size without pixel editing or weakening assertions.",
    "tests/web-e2e/xscienceAskUser.e2e.ts-snapshots/ask-user-typed-narrow-official-windows-chrome.png": "Capture the current typed AskUser fixture in real Windows Chrome at narrow size without pixel editing or weakening assertions.",
    "tests/web-e2e/xscienceAskUser.e2e.ts-snapshots/ask-user-typed-desktop-dark-official-windows-chrome.png": "Capture the current typed AskUser fixture in real Windows Chrome at desktop size using the existing dark-theme attributes without pixel editing or weakening assertions.",
    "tests/web-e2e/xscienceAskUser.e2e.ts-snapshots/ask-user-typed-narrow-dark-official-windows-chrome.png": "Capture the current typed AskUser fixture in real Windows Chrome at narrow size using the existing dark-theme attributes without pixel editing or weakening assertions.",
    "tests/web-e2e/xscienceOnboarding.e2e.ts": "Verify fresh authentication, accessible five-step onboarding, capability reachability, draft recovery, Artifact refs, and workspace entry.",
    "tests/web-e2e/xScienceMcpApp.e2e.ts": "Verify isolated MCP App loading, editable save-to-Artifact versioning, and refresh rehydration against the production Go host in Google Chrome.",
    "tests/web-e2e/xScienceMemory.e2e.ts": "Verify the owner-scoped Memory settings lifecycle against the production Go host in Google Chrome.",
    "tests/web-e2e/xscienceConversationBranches.e2e.ts": "Verify branch containment, selection, refresh, and responsive behavior against the real Go browser host.",
    "tests/web-e2e/xscienceConversationRecovery.e2e.ts-snapshots/conversation-deleted-tombstone-desktop-linux.png": "Reviewed desktop visual baseline for deleted-conversation recovery.",
    "tests/web-e2e/xscienceConversationRecovery.e2e.ts-snapshots/conversation-deleted-tombstone-narrow-linux.png": "Reviewed narrow visual baseline for deleted-conversation recovery.",
    "tests/web-e2e/xscienceModelListRecovery.e2e.ts-snapshots/model-list-unavailable-desktop-linux.png": "Reviewed desktop visual baseline for model-catalog recovery.",
    "tests/web-e2e/xscienceModelListRecovery.e2e.ts-snapshots/model-list-unavailable-narrow-linux.png": "Reviewed narrow visual baseline for model-catalog recovery.",
    "tests/web-e2e/xscienceRuntimeFailureRecovery.e2e.ts-snapshots/runtime-recovery-model-overloaded-desktop-linux.png": "Reviewed desktop task-center baseline for model-overload recovery.",
    "tests/web-e2e/xscienceRuntimeFailureRecovery.e2e.ts-snapshots/runtime-recovery-model-overloaded-narrow-linux.png": "Reviewed narrow task-center baseline for model-overload recovery.",
    "tests/web-e2e/xscienceRuntimeFailureRecovery.e2e.ts-snapshots/runtime-recovery-model-unavailable-desktop-linux.png": "Reviewed desktop task-center baseline for unavailable-model recovery.",
    "tests/web-e2e/xscienceRuntimeFailureRecovery.e2e.ts-snapshots/runtime-recovery-model-unavailable-narrow-linux.png": "Reviewed narrow task-center baseline for unavailable-model recovery.",
    "tests/web-e2e/xscienceRuntimeFailureRecovery.e2e.ts-snapshots/runtime-recovery-safety-refusal-desktop-linux.png": "Reviewed desktop task-center baseline for safety-refusal recovery.",
    "tests/web-e2e/xscienceRuntimeFailureRecovery.e2e.ts-snapshots/runtime-recovery-safety-refusal-narrow-linux.png": "Reviewed narrow task-center baseline for safety-refusal recovery.",
    "tests/web-e2e/xscienceThirdPartyLicenses.e2e.ts-snapshots/third-party-licenses-desktop-linux.png": "Reviewed desktop visual baseline for the third-party license inventory.",
    "tests/web-e2e/xscienceThirdPartyLicenses.e2e.ts-snapshots/third-party-licenses-narrow-linux.png": "Reviewed narrow visual baseline for the third-party license inventory.",
    "tests/web-e2e/xscienceTranscriptRebase.e2e.ts": "Verify canonical Transcript rebase and reload behavior without restoring a legacy message authority.",
    "packages/desktop/src/renderer/components/layout/Sider/SiderNav/SiderLibraryEntry.tsx": "Expose the native Library route in the existing sidebar while retaining navigation cleanup, keyboard semantics and account authority; no task admission or catalogue mutation owner is added.",
    "packages/desktop/src/renderer/pages/library/LibraryItemDetails.tsx": "Present owned literature metadata, attachment versions and project membership through the existing catalogue DTO and metadata editor; no attachment storage or mutation authority is duplicated.",
    "packages/desktop/src/renderer/pages/library/LibraryPage.tsx": "Connect the Library workbench list, search, detail and receipt readback to the existing authenticated route, catalogue and mutation controller; no competing import, task or persistence path is created.",
    "packages/desktop/src/renderer/pages/library/libraryWorkbench.css": "Style the native Library workbench with existing theme variables, compact split panes, responsive layout and reduced-motion support; no third-party asset or desktop implementation is transplanted.",
    "packages/desktop/src/renderer/pages/library/useLibraryWorkbench.ts": "Own cancellable view reads for the Library list, selected item and project labels with bounded waits and current account/request fencing, while reusing the original HTTP and catalogue authorities.",
    "packages/desktop/src/renderer/pages/library/LibraryAttachmentBrowser.tsx": "Connect immutable Library versions to the existing owner-authorized content route and shared PDF core with account-scoped preview, download, cancellation and exact-version retry.",
    "packages/desktop/src/renderer/pages/library/LibraryAttachmentPreview.tsx": "Bound the shared PDF-core module loading, explicit retry and close lifecycle in a native Library pane; no alternate parser, content transport or storage owner is introduced.",
    "packages/desktop/src/renderer/pages/library/libraryAttachmentTarget.ts": "Derive immutable Library attachment-version targets from typed catalogue metadata without guessing Artifact identities, substituting another version or exposing filesystem paths.",
    'packages/desktop/src/common/adapter/literatureCollections.ts': 'Define strict command-bound ordinary Library collection, membership and receipt contracts without substituting item results or creating another mutation authority.',
    'packages/desktop/src/renderer/services/runtime/literatureCollections.ts': 'Route ordinary Library collection reads and original operation retries through the authenticated existing HTTP bridge and renderer account scope.',
    'packages/desktop/src/renderer/pages/library/LibraryCollectionSidebar.tsx': 'Project the owned ordinary collection tree and server order into real filtering and creation controls without decorative smart or batch entries.',
    'packages/desktop/src/renderer/pages/library/LibraryCollectionEditor.tsx': 'Bind collection create, revision edits, deletion and exact uncertain-request retry to the existing account-wide mutation owner and form generation.',
    'packages/desktop/src/renderer/pages/library/LibraryCollectionMembership.tsx': 'Read and mutate the selected owned paper memberships through the same Library slot while fencing old item and account responses.',
}

ALLOWED_ADDITION_RULES = (
    (
        ("packages/desktop/src/renderer/components/", "packages/desktop/src/renderer/hooks/", "packages/desktop/src/renderer/pages/", "packages/desktop/src/renderer/services/", "packages/desktop/src/renderer/utils/"),
        "Add bounded typed renderer components, hooks, pages, services, and utilities required by the authenticated Go-owned product surface.",
    ),
    (
        ("tests/unit/", "tests/web-e2e/"),
        "Add focused regression and real-browser coverage for the migrated product behavior.",
    ),
    (
        ("public/branding/", "public/dev-sw-cleanup.js"),
        "Add provenance-bound X-Science branding assets and bounded development service-worker cleanup.",
    ),
)

ALLOWED_REMOVALS = {
    "packages/desktop/src/renderer/hooks/file/useWorkspaceSelector.ts": "Retire the unconsumed legacy native workspace selector after all conversation file intake uses the existing browser/project picker or explicit host-grant controls; preserve original import history.",
    "packages/desktop/src/renderer/pages/conversation/Messages/components/SkillSuggestCard.tsx": "Retire the competing synchronous Skill author/install button; preserve historical suggestions as read-only content and use the sole typed conversation action.",
    "public/pwa/icon-180.png": "Retire the superseded product artwork after the original approved touch icon became the only current browser reference.",
    "public/pwa/icon-192.png": "Retire the superseded product artwork after all renderer and service-worker references use the approved original orange mark.",
    "public/pwa/icon-512.png": "Retire the superseded product artwork after the installable application manifest uses the unchanged original 660px mark.",
    "packages/desktop/src/renderer/components/xScience/runtime/ContextUsageIndicator.tsx": "Retire the unused ACP-derived usage ring after the authenticated request-usage card became the single composer authority.",
    "packages/desktop/src/renderer/styles/codex-theme.css": "Rename the shared visual shell to workspace-theme.css while preserving its original import provenance.",
    "packages/desktop/src/renderer/pages/conversation/Messages/acp/MessageAcpToolCall.tsx": "Consolidate tool rendering into the receipt-bound ToolOperationDetail and tool timeline.",
    "packages/desktop/src/renderer/pages/conversation/Messages/components/MessagePlan.tsx": "Consolidate plan rendering into the shared XSciencePlanTree.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/AcpE2EStreamInjector.tsx": "Retire production DOM event injection; browser acceptance uses the real transcript-backed controlled-provider fixture.",
    "tests/integration/xscienceRealLlmFixture.ts": "Rename the controlled-provider fixture to xscienceControlledLlmFixture so simulated provider responses are not presented as real-model evidence.",
    "tests/unit/chat/MessageAcpToolCall.dom.test.tsx": "Move tool presentation regression coverage to the unified MessageToolGroupSummary and ToolOperationDetail consumers.",
    "packages/desktop/src/renderer/hooks/file/useAutoPreviewOfficeFiles.ts": "Retire the unused desktop Office watcher after canonical Artifact references and the unified preview panel became the user-visible file authority.",
    "packages/desktop/src/renderer/hooks/system/useAutoPreviewOfficeFilesEnabled.ts": "Remove the orphaned Office auto-preview setting hook with its retired desktop watcher.",
    "tests/unit/renderer/useAutoPreviewOfficeFiles.dom.test.ts": "Remove the desktop Office watcher regression with its retired implementation; canonical Artifact preview behavior retains focused component coverage.",
    "tests/unit/xscience/xScienceFramePlan.test.ts": "Remove filename-based plan discovery assertions after the typed Frame plan API became the sole authority; the real Frame plan integration regression remains.",
    'packages/desktop/src/renderer/utils/ui/runtimePatches.ts': 'Remove the global console, warning, error, ResizeObserver, and requestAnimationFrame monkey patch after real component and browser paths proved the native runtime is stable without it.',
    'tests/unit/renderer/runtimePatches.dom.test.ts': 'Remove the regression that asserted the retired global runtime monkey patch instead of user-visible component behavior.',
	"packages/desktop/src/renderer/pages/conversation/Preview/components/editors/HTMLEditor.tsx": "Retire the legacy HTML editor after DocumentWysiwygEditor became the single version-aware document editing authority.",
	"packages/desktop/src/renderer/pages/conversation/Preview/components/editors/MarkdownEditor.tsx": "Retire the legacy Markdown source editor after Streamdown preview and DocumentWysiwygEditor became the maintained editing surfaces.",
	"packages/desktop/src/renderer/pages/conversation/Preview/components/renderers/SelectionToolbar.tsx": "Retire the legacy renderer selection toolbar after the canonical preview region-comment layer assumed selection actions.",
	"packages/desktop/src/renderer/hooks/chat/useAutoScroll.ts": "Remove the unused legacy content-scroller hook after conversation scrolling converged on the Virtuoso-owned controller.",
    "packages/desktop/src/renderer/pages/conversation/Messages/useAutoScroll.ts": "Remove the competing DOM, RAF, read-cursor, and virtual-list scroll authority after Virtuoso assumed sole geometry ownership.",
    "tests/unit/renderer/useAutoScroll.dom.test.tsx": "Replace the deleted multi-authority scroll regression suite with the focused single-controller contract and real Chrome flow.",
    "packages/desktop/src/renderer/components/xScience/runtime/streamingMessagesModel.ts": "Remove the second streaming overlay authority after live stdout converged on the root-scoped conversation streaming projection.",
    "packages/desktop/src/renderer/components/xScience/runtime/streamingModel.ts": "Remove the mixed text-thinking-stdout compatibility model after text and thinking remained solely owned by durable message.stream.",
    "packages/desktop/src/renderer/services/xScienceStreaming.ts": "Move streaming recovery I/O beside the unified conversation streaming model and store.",
    "tests/unit/xscience/streamingMessagesModel.test.ts": "Replace the legacy overlay regression with the unified conversation streaming projection contract.",
    "tests/unit/xscience/streamingModel.test.ts": "Replace the mixed compatibility-model regression with the consumed conversation streaming recovery slice contract.",
    "packages/desktop/src/renderer/pages/conversation/Messages/components/MessageToolGroup.tsx": "Retire the duplicate legacy tool-group renderer after every tool-group path converged on the workspace MessageToolGroupSummary projection.",
    "packages/desktop/src/renderer/components/xScience/runtime/XScienceKernelNotebook.tsx": "Retire the duplicate executable kernel notebook after the governed Compute runtime panel became the single kernel inventory, status, interrupt, clear, and task-level stop surface.",
    "packages/desktop/src/renderer/services/skillsCatalog.ts": "Relocate the Skill catalog client into the canonical skills service domain without changing behavior.",
    "packages/desktop/src/renderer/services/xScienceExpertProfiles.ts": "Relocate the expert profile client into the canonical agents service domain without changing behavior.",
    "packages/desktop/src/renderer/services/xScienceMcpApps.ts": "Relocate the MCP app client into the canonical MCP service domain without changing behavior.",
    "packages/desktop/src/renderer/services/xScienceSkillLibrary.ts": "Relocate the Skill library client into the canonical skills service domain without changing behavior.",
    "packages/desktop/src/renderer/assets/logos/brand/app.png": "Remove the previous imported product wordmark after all product surfaces switched to the provenance-tracked text-free PWA icon and HTML X-Science name.",
    "packages/desktop/src/renderer/hooks/chat/useInitialMessage.ts": "Remove an unused candidate hook superseded by the tested ACP initial-message implementation.",
    "packages/desktop/src/renderer/pages/project/projectArtifactLibrary.ts": "Rename the imported pure artifact-library model so extensionless imports cannot collide with the ProjectArtifactLibrary component on case-insensitive filesystems.",
    "packages/desktop/src/renderer/pages/conversation/Messages/components/toolArtifactModel.ts": "Remove filename and tool-name artifact guessing after canonical message artifact_refs became the only association authority.",
    "packages/desktop/src/renderer/pages/TestShowcase.tsx": "Remove an unreferenced development showcase containing corrupted legacy display text.",
    "tests/unit/renderer/projectArtifactLibrary.dom.test.ts": "Rename the imported artifact-library model regression so its module stem cannot collide with the component test on case-insensitive filesystems.",
    "tests/unit/renderer/XScienceSessionOptions.dom.test.tsx": "Rename the imported session-options component regression to its exact component name so it cannot collide with the service test on case-insensitive filesystems.",
    "tests/unit/renderer/toolArtifactModel.test.ts": "Remove the superseded artifact-guessing regression together with its deleted heuristic implementation.",
    "tests/unit/xscience/XScienceKernelNotebook.dom.test.tsx": "Remove the obsolete executable-notebook regression with the duplicate kernel notebook component; the canonical Compute panel has its own interaction coverage.",
    "packages/desktop/src/renderer/pages/conversation/Workspace/utils/xScienceWorkspaceHydration.ts": "Remove the duplicate workspace hydration helper after the canonical workspace tree and frame-read services assumed sole authority.",
    "packages/desktop/src/renderer/pages/conversation/components/ChatHistory.tsx": "Remove the legacy non-virtualized chat history after MessageList became the single canonical conversation renderer.",
    "packages/desktop/src/renderer/pages/conversation/platforms/acp/XScienceConversationSyncNotice.tsx": "Remove the duplicate sync notice after runtime status and terminal failure surfaces became authoritative.",
    "packages/desktop/src/renderer/pages/project/ProjectWorkbench.tsx": "Retire the duplicate project workbench page after project routes converged on the project and conversation workspace surfaces.",
    "packages/desktop/src/renderer/pages/project/projectWorkbenchModel.ts": "Retire the duplicate project-workbench view model with its removed page.",
    "packages/desktop/src/renderer/pages/settings/PermissionsSettings.tsx": "Retire the standalone settings page after permission controls moved into the unified settings information architecture.",
    "packages/desktop/src/renderer/pages/settings/PetSettings.tsx": "Retire the unsupported pet settings route from the focused X-Science product surface.",
    "packages/desktop/src/renderer/pages/settings/SystemSettings.tsx": "Retire the standalone system settings page after supported controls moved into the unified general settings surface.",
    "packages/desktop/src/renderer/pages/settings/models/permissionPresentation.ts": "Remove the legacy permission presentation model with its retired standalone page.",
    "tests/unit/renderer/ProjectWorkbench.dom.test.tsx": "Remove the obsolete duplicate project workbench regression with the retired page.",
    "tests/unit/renderer/XScienceConversationSyncNotice.dom.test.tsx": "Remove the obsolete sync-notice regression with the retired component.",
    "tests/unit/renderer/projectWorkbenchModel.test.ts": "Remove the duplicate project workbench model regression with its retired model.",
    "tests/unit/settings/SystemSettings.dom.test.tsx": "Remove the standalone system settings regression after route convergence.",
    "tests/unit/settings/backgroundUtils.test.ts": "Remove the legacy custom-background regression after theme authority moved to builtin themes.",
    "tests/unit/workspace/xScienceWorkspaceHydration.test.ts": "Remove the duplicate hydration regression with its retired helper.",
    "tests/web-e2e/xscienceProjectWorkbench.e2e.ts": "Remove the duplicate project workbench browser flow after route convergence; its orphan snapshots are quarantined outside the repository.",
}

ALLOWED_REMOVAL_RULES = (
    (
        ("packages/desktop/src/renderer/assets/icons/", "packages/desktop/src/renderer/assets/themes/"),
        "Retire unused legacy icon and theme assets after the branded PWA assets and builtin workspace theme system became authoritative.",
    ),
    (
        ("packages/desktop/src/renderer/pages/cron/", "tests/unit/cron/", "tests/unit/renderer/cron/"),
        "Retire the duplicate legacy scheduled-task Web pages and their isolated UI tests while the Go routine scheduler and governed task runtime remain authoritative.",
    ),
    (
        ("packages/desktop/src/renderer/pages/settings/AppearanceSettings/",),
        "Retire the legacy arbitrary CSS-theme editor and preset gallery after the bounded builtin theme authority replaced it.",
    ),
    (
        tuple(
            f"packages/desktop/src/renderer/services/i18n/locales/{locale}/"
            for locale in (
                "de-DE",
                "es-ES",
                "fa-IR",
                "ja-JP",
                "ko-KR",
                "pt-BR",
                "ru-RU",
                "tr-TR",
                "uk-UA",
                "zh-TW",
            )
        ),
        "Retire incomplete legacy locale bundles after the product language contract was narrowed to complete Simplified Chinese and English coverage.",
    ),
)

# Exact adaptations above retain narrow historical reasons. These ordered
# domain rules classify the later full-product migration. The fingerprint below
# locks the complete path and source/target hash set, so a prefix cannot silently
# authorize future edits or additions.
ALLOWED_ADAPTATION_RULES = (
    (
        (
            "packages/desktop/src/common/adapter/",
            "packages/desktop/src/common/config/",
            "packages/desktop/src/common/theme/",
            "packages/desktop/src/common/types/",
        ),
        "Align shared browser contracts, configuration, themes, and typed preview/provider models with the Go-owned X-Science runtime.",
    ),
    (
        (
            "packages/desktop/src/renderer/styles/",
            "packages/desktop/src/renderer/theme/",
            "packages/desktop/src/renderer/types.d.ts",
            "public/pwa/",
            "public/sw.js",
        ),
        "Converge global styles, builtin theme declarations, PWA assets, service-worker behavior, and renderer type declarations on the focused Web product.",
    ),
    (
        ("packages/desktop/src/common/config/i18n.ts",),
        "Centralize native language labels while preserving normalization for the complete offline locale catalog.",
    ),
    (
        ("packages/desktop/src/common/utils/",),
        "Keep shared browser-safe utility behavior aligned with the complete migrated locale and runtime contracts.",
    ),
    (
        (
            "packages/desktop/src/renderer/components/Markdown/",
            "packages/desktop/src/renderer/components/base/",
        ),
        "Adapt shared renderer primitives for safe native previews, localized errors, accessibility, and responsive Web use.",
    ),
    (
        ("packages/desktop/src/renderer/components/chat/",),
        "Connect the composer, queue, attachments, and chat actions to the Go-owned conversation runtime with localized responsive states.",
    ),
    (
        ("packages/desktop/src/renderer/components/layout/",),
        "Adapt the desktop layout, command palette, navigation, and window controls to authenticated responsive browser operation.",
    ),
    (
        ("packages/desktop/src/renderer/components/media/",),
        "Harden media selection, upload, diff, preview, and embedded-content states for the Go Web trust boundary.",
    ),
    (
        ("packages/desktop/src/renderer/components/settings/",),
        "Adapt shared settings dialogs and language controls to the Go configuration and browser filesystem contracts.",
    ),
    (
        ("packages/desktop/src/renderer/components/xScience/",),
        "Project authoritative Go files, notes, approvals, models, kernels, sessions, and runtime operations into the migrated workbench.",
    ),
    (
        ("packages/desktop/src/renderer/components/workspace/",),
        "Adapt reusable workspace folder selection to the authenticated Go filesystem and project-root boundary.",
    ),
    (
        (
            "packages/desktop/src/renderer/hooks/",
            "packages/desktop/src/renderer/main.tsx",
        ),
        "Bootstrap authenticated Web runtime state and adapt assistant and MCP hooks without retaining Electron server authority.",
    ),
    (
        ("packages/desktop/src/renderer/pages/artifact/",),
        "Connect immutable Go artifacts, annotations, refinement, and native viewers with bounded localized failure handling.",
    ),
    (
        ("packages/desktop/src/renderer/pages/conversation/Preview/",),
        "Migrate preview history and real scientific, office, document, and media viewers with safe parsing and responsive controls.",
    ),
    (
        ("packages/desktop/src/renderer/pages/conversation/",),
        "Connect conversation history, messages, workspace, delegation, commands, and realtime recovery to the Go runtime.",
    ),
    (
        ("packages/desktop/src/renderer/pages/cron/",),
        "Adapt scheduled-task creation, inspection, and lifecycle controls to the Go routine APIs.",
    ),
    (
        (
            "packages/desktop/src/renderer/pages/guid/",
            "packages/desktop/src/renderer/pages/login/",
            "packages/desktop/src/renderer/pages/project/",
        ),
        "Migrate authenticated login, onboarding, project workbench, and artifact workflows to the Go product shell.",
    ),
    (
        ("packages/desktop/src/renderer/pages/settings/",),
        "Expose Go-owned agents, skills, MCP, models, compute, memory, permissions, credentials, storage, and governance settings.",
    ),
    (
        (
            "packages/desktop/src/renderer/services/",
            "packages/desktop/src/renderer/utils/",
        ),
        "Implement typed Go API clients, complete localization resources, runtime projections, and browser-safe presentation models.",
    ),
    (
        ("scripts/generate-i18n-types.js",),
        "Generate deterministic i18n declarations with the locked local formatter and no Bun fallback.",
    ),
    (
        ("tests/integration/",),
        "Exercise migrated Go API, saved-model, authentication, and packaged-localization contracts through real protocol fixtures.",
    ),
    (
        ("tests/web-e2e/",),
        "Exercise authenticated Go runtime, project, conversation, delegation, settings, and preview workflows in real responsive Chromium.",
    ),
    (
        ("tests/unit/",),
        "Align behavioral tests with the migrated Go contracts, full localization, responsive states, and deterministic test isolation.",
    ),
)

APPROVED_ADAPTATION_FINGERPRINT = "6d8a48f000ad76aef305062d200600004c75c0243f1ef6dd4bfd370383563cf8"
APPROVED_REMOVAL_FINGERPRINT = "61490d06fb2aef55e653ccf0ae1f4ed19bb141ac90622553eb5b3a94b2ef19b0"
APPROVED_ADDITION_FINGERPRINT = "77357dfbc4e12d0f2fb311e19e762ca0c0bed3b79f4ca91968b7dde7fa93e323"

MANUAL_LICENSES = {
    "@nightingale-elements/nightingale-msa": {
        "license": "MIT",
        "basis": "upstream repository license",
        "url": "https://github.com/ebi-webcomponents/nightingale/blob/main/LICENSE",
    },
    "emitter-component": {
        "license": "MIT",
        "basis": "transitive package metadata omission; upstream package license",
    },
    "format": {
        "license": "MIT",
        "basis": "transitive package metadata omission; upstream package license",
    },
    "khroma": {
        "license": "MIT",
        "basis": "transitive package metadata omission; upstream package license",
    },
}

BUILTIN_IMPORTS = {
    "child_process",
    "fs",
    "os",
    "path",
    "vitest",
}


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def iter_files_without(root: Path, ignored_directories: set[str]):
    """Yield files without descending into generated dependency/output trees."""
    for directory, names, files in os.walk(root, topdown=True, followlinks=False):
        names[:] = sorted(name for name in names if name not in ignored_directories)
        for name in sorted(files):
            yield Path(directory, name)


def json_bytes(value: Any) -> bytes:
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode("utf-8")


def read_json(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def adaptation_reason(relative: str) -> str | None:
    exact = ALLOWED_ADAPTATIONS.get(relative)
    if exact is not None:
        return exact
    for prefixes, reason in ALLOWED_ADAPTATION_RULES:
        if any(relative == prefix or (prefix.endswith("/") and relative.startswith(prefix)) for prefix in prefixes):
            return reason
    return None


def removal_reason(relative: str) -> str | None:
    exact = ALLOWED_REMOVALS.get(relative)
    if exact is not None:
        return exact
    for prefixes, reason in ALLOWED_REMOVAL_RULES:
        if any(relative.startswith(prefix) for prefix in prefixes):
            return reason
    return None


def addition_reason(relative: str) -> str | None:
    exact = ALLOWED_ADDITIONS.get(relative)
    if exact is not None:
        return exact
    for prefixes, reason in ALLOWED_ADDITION_RULES:
        if any(relative == prefix or (prefix.endswith("/") and relative.startswith(prefix)) for prefix in prefixes):
            return reason
    return None


def adaptation_fingerprint(adaptations: list[dict[str, Any]]) -> str:
    records = [
        {
            "path": record["path"],
            "sourceSHA256": record["sourceSHA256"],
            "targetSHA256": record["targetSHA256"],
            **({"originalPath": record["originalPath"]} if "originalPath" in record else {}),
        }
        for record in adaptations
    ]
    payload = json.dumps(records, ensure_ascii=True, sort_keys=True, separators=(",", ":")).encode("ascii")
    return hashlib.sha256(payload).hexdigest()


def removal_fingerprint(removals: list[dict[str, Any]]) -> str:
    records = [
        {
            "path": record["path"],
            "sourceSHA256": record["sourceSHA256"],
            **({"originalPath": record["originalPath"]} if "originalPath" in record else {}),
        }
        for record in removals
    ]
    payload = json.dumps(records, ensure_ascii=True, sort_keys=True, separators=(",", ":")).encode("ascii")
    return hashlib.sha256(payload).hexdigest()


def addition_fingerprint(additions: list[dict[str, Any]]) -> str:
    records = [
        {
            "path": record["path"],
            "sha256": record["sha256"],
        }
        for record in additions
    ]
    payload = json.dumps(records, ensure_ascii=True, sort_keys=True, separators=(",", ":")).encode("ascii")
    return hashlib.sha256(payload).hexdigest()


def package_name_from_lock_path(path: str) -> str:
    marker = "node_modules/"
    if marker not in path:
        return path
    value = path.rsplit(marker, 1)[1]
    parts = value.split("/")
    if value.startswith("@") and len(parts) >= 2:
        return "/".join(parts[:2])
    return parts[0]


def license_report(frontend: Path, package: dict[str, Any], lock: dict[str, Any]) -> dict[str, Any]:
    direct_runtime = set(package.get("dependencies", {}))
    direct_development = set(package.get("devDependencies", {}))
    records: list[dict[str, Any]] = []
    workspace_links: list[dict[str, Any]] = []
    unresolved_direct: list[str] = []
    license_counts: collections.Counter[str] = collections.Counter()
    for lock_path, metadata in sorted(lock.get("packages", {}).items()):
        if not lock_path or lock_path.startswith("packages/"):
            continue
        name = package_name_from_lock_path(lock_path)
        if metadata.get("link"):
            target = metadata.get("resolved", "")
            target_path = (frontend / target).resolve()
            target_metadata = lock.get("packages", {}).get(target, {})
            if (
                not target.startswith("packages/")
                or not target_path.is_relative_to(frontend.resolve())
                or target_metadata.get("name") != name
            ):
                raise RuntimeError(f"invalid local workspace link: {lock_path}")
            local_package = read_json(target_path / "package.json")
            if any(local_package.get(key) != target_metadata.get(key) for key in ("name", "version")):
                raise RuntimeError(f"workspace package identity mismatch: {lock_path}")
            workspace_links.append({
                "name": name,
                "version": local_package.get("version"),
                "path": target,
                "basis": "local workspace package; source licensing reviewed separately",
            })
            continue
        license_name = metadata.get("license")
        basis = "package-lock metadata"
        url = None
        if not license_name and name in MANUAL_LICENSES:
            manual = MANUAL_LICENSES[name]
            license_name = manual["license"]
            basis = manual["basis"]
            url = manual.get("url")
        if not license_name:
            license_name = "NOASSERTION"
        direct_kind = None
        if name in direct_runtime:
            direct_kind = "runtime-build"
        elif name in direct_development:
            direct_kind = "development"
        if direct_kind and license_name == "NOASSERTION":
            unresolved_direct.append(name)
        license_counts[license_name] += 1
        record = {
            "name": name,
            "version": metadata.get("version"),
            "license": license_name,
            "directKind": direct_kind,
            "basis": basis,
        }
        if url:
            record["sourceUrl"] = url
        records.append(record)
    if unresolved_direct:
        raise RuntimeError("direct dependency licenses unresolved: " + ", ".join(sorted(set(unresolved_direct))))
    return {
        "schemaVersion": 1,
        "package": package.get("name"),
        "lockfileSHA256": sha256_file(frontend / "package-lock.json"),
        "summary": {
            "records": len(records),
            "workspaceLinkRecords": len(workspace_links),
            "directRuntimeBuildDependencies": len(direct_runtime),
            "directDevelopmentDependencies": len(direct_development),
            "licenseCounts": dict(sorted(license_counts.items())),
            "unresolvedTransitiveRecords": license_counts["NOASSERTION"],
            "unresolvedDirectRecords": 0,
        },
        "records": records,
        "workspaceLinks": workspace_links,
    }


def audit_package(frontend: Path, package: dict[str, Any], lock: dict[str, Any]) -> None:
    dependencies = set(package.get("dependencies", {}))
    blocked = sorted(dependencies & BLOCKED_SERVER_PACKAGES)
    if blocked:
        raise RuntimeError("server/runtime dependencies remain in frontend: " + ", ".join(blocked))
    scripts = package.get("scripts", {})
    forbidden_script = re.compile(r"(^|\s)(bun|bunx)(\s|$)|web-host|webui\.ts", re.IGNORECASE)
    invalid_scripts = [name for name, command in scripts.items() if forbidden_script.search(str(command))]
    if invalid_scripts:
        raise RuntimeError("Bun/WebHost runtime scripts remain: " + ", ".join(sorted(invalid_scripts)))
    root_lock = lock.get("packages", {}).get("", {})
    if root_lock.get("name") != package.get("name") or root_lock.get("version") != package.get("version"):
        raise RuntimeError("package-lock root identity does not match package.json")
    if root_lock.get("dependencies", {}) != package.get("dependencies", {}):
        raise RuntimeError("package-lock runtime dependencies do not match package.json")
    if root_lock.get("devDependencies", {}) != package.get("devDependencies", {}):
        raise RuntimeError("package-lock development dependencies do not match package.json")


def migration_manifest(frontend: Path, source: dict[str, Any]) -> dict[str, Any]:
    source_records = canonical_source_records(source)
    adaptations = []
    removals = []
    unchanged = 0
    missing = []
    for relative, record in sorted(source_records.items()):
        target = frontend / relative
        original_path = record["path"]
        origin = {"originalPath": original_path} if original_path != relative else {}
        if target.is_symlink():
            raise RuntimeError("migrated source must not be a symlink: " + relative)
        if not target.is_file():
            reason = removal_reason(relative)
            if reason is None:
                missing.append(relative)
            else:
                removals.append(
                    {
                        "path": relative,
                        **origin,
                        "sourceSHA256": record["sha256"],
                        "sourceBytes": record["bytes"],
                        "reason": reason,
                    }
                )
            continue
        target_hash = sha256_file(target)
        if target_hash == record["sha256"] and original_path == relative:
            unchanged += 1
            continue
        reason = adaptation_reason(relative)
        if reason is None and target_hash != record["sha256"]:
            raise RuntimeError(f"unclassified migrated-source change: {relative}")
        if original_path != relative:
            reason = "Move genuine imported source to the current X-Science namespace. " + (reason or "Preserve its original bytes without content adaptation.")
        if reason is None:
            raise RuntimeError(f"unclassified migrated-source change: {relative}")
        adaptations.append(
            {
                "path": relative,
                **origin,
                "sourceSHA256": record["sha256"],
                "targetSHA256": target_hash,
                "bytes": target.stat().st_size,
                "reason": reason,
            }
        )
    if missing:
        raise RuntimeError("migrated source files are missing: " + ", ".join(missing[:20]))

    unknown_removals = sorted(set(ALLOWED_REMOVALS) - set(source_records))
    if unknown_removals:
        raise RuntimeError("removal policy references unknown source files: " + ", ".join(unknown_removals))
    retained_removals = sorted(relative for relative in ALLOWED_REMOVALS if (frontend / relative).exists())
    if retained_removals:
        raise RuntimeError("removal policy files unexpectedly remain: " + ", ".join(retained_removals))

    removal_digest = removal_fingerprint(removals)
    if removal_digest != APPROVED_REMOVAL_FINGERPRINT:
        raise RuntimeError(
            "migrated-source removal fingerprint mismatch: "
            f"expected {APPROVED_REMOVAL_FINGERPRINT}, got {removal_digest}"
        )

    fingerprint = adaptation_fingerprint(adaptations)
    if fingerprint != APPROVED_ADAPTATION_FINGERPRINT:
        raise RuntimeError(
            "migrated-source adaptation fingerprint mismatch: "
            f"expected {APPROVED_ADAPTATION_FINGERPRINT}, got {fingerprint}"
        )

    missing_required_additions = sorted(relative for relative in ALLOWED_ADDITIONS if not (frontend / relative).is_file())
    if missing_required_additions:
        raise RuntimeError("required migrated frontend additions are missing: " + ", ".join(missing_required_additions[:20]))

    additions = []
    unexpected_additions = []
    for path in iter_files_without(frontend, {"node_modules"}):
        relative_path = path.relative_to(frontend)
        relative = relative_path.as_posix()
        if relative in source_records or relative in {SOURCE_MANIFEST, MIGRATION_MANIFEST, LICENSE_REPORT}:
            continue
        reason = addition_reason(relative)
        if reason is None:
            unexpected_additions.append(relative)
            continue
        additions.append(
            {
                "path": relative,
                "sha256": sha256_file(path),
                "bytes": path.stat().st_size,
                "reason": reason,
            }
        )
    if unexpected_additions:
        raise RuntimeError(
            "unclassified migrated frontend additions: " + ", ".join(sorted(unexpected_additions)[:30])
        )
    additions.sort(key=lambda record: record["path"])
    additions_digest = addition_fingerprint(additions)
    if additions_digest != APPROVED_ADDITION_FINGERPRINT:
        raise RuntimeError(
            "migrated-source addition fingerprint mismatch: "
            f"expected {APPROVED_ADDITION_FINGERPRINT}, got {additions_digest}"
        )
    return {
        "schemaVersion": 1,
        "source": source.get("source"),
        "sourceMutationPolicy": "read-only",
        "sourceImportManifestSHA256": sha256_file(frontend / SOURCE_MANIFEST),
        "adaptationFingerprintSHA256": fingerprint,
        "removalFingerprintSHA256": removal_digest,
        "additionFingerprintSHA256": additions_digest,
        "summary": {
            "sourceFiles": len(source_records),
            "unchangedSourceFiles": unchanged,
            "adaptedSourceFiles": len(adaptations),
            "removedSourceFiles": len(removals),
            "addedFiles": len(additions),
        },
        "adaptations": adaptations,
        "removals": removals,
        "additions": additions,
    }


def audit_tree(frontend: Path, require_clean: bool) -> None:
    blocked_names = {"coverage", "dist", "out", "test-results", "__pycache__"}
    failures = []
    if require_clean and (frontend / "node_modules").exists():
        failures.append("node_modules")
    for directory, names, files in os.walk(frontend, topdown=True, followlinks=False):
        current = Path(directory)
        retained = []
        for name in sorted(names):
            candidate = current / name
            relative = candidate.relative_to(frontend)
            if name == "node_modules":
                continue
            if name in blocked_names:
                failures.append(relative.as_posix())
                continue
            retained.append(name)
        names[:] = retained
        for name in sorted(files):
            path = current / name
            relative = path.relative_to(frontend)
            if path.stat().st_size > 10 * 1024 * 1024:
                failures.append(relative.as_posix() + " (>10 MiB)")
    if failures:
        raise RuntimeError("blocked frontend build/residue paths: " + ", ".join(sorted(set(failures))[:30]))


def audit_module_stem_collisions(frontend: Path) -> None:
    """Reject extensionless module names that collide on case-insensitive filesystems."""

    module_suffixes = {".cjs", ".cts", ".js", ".jsx", ".mjs", ".mts", ".ts", ".tsx"}
    ignored_parts = {"__pycache__", "coverage", "dist", "node_modules", "out", "test-results"}
    paths_by_stem: dict[str, list[str]] = {}
    for path in iter_files_without(frontend, ignored_parts):
        if path.suffix.lower() not in module_suffixes:
            continue
        relative = path.relative_to(frontend)
        extensionless = (relative.parent / path.stem).as_posix()
        paths_by_stem.setdefault(extensionless.casefold(), []).append(relative.as_posix())

    collisions = [sorted(paths) for paths in paths_by_stem.values() if len(paths) > 1]
    if collisions:
        details = "; ".join(" <> ".join(paths) for paths in sorted(collisions))
        raise RuntimeError("case-insensitive extensionless module collision: " + details)


def write_or_check(path: Path, content: bytes, check: bool) -> None:
    if check:
        if not path.is_file() or path.read_bytes() != content:
            raise RuntimeError(f"generated frontend audit artifact is stale: {path.name}")
        return
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_bytes(content)
    os.replace(temporary, path)


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=DEFAULT_ROOT)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--require-clean", action="store_true")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    frontend = args.root.resolve() / FRONTEND
    package = read_json(frontend / "package.json")
    lock = read_json(frontend / "package-lock.json")
    source = read_json(frontend / SOURCE_MANIFEST)
    audit_package(frontend, package, lock)
    audit_tree(frontend, args.require_clean)
    audit_module_stem_collisions(frontend)
    licenses = license_report(frontend, package, lock)
    migration = migration_manifest(frontend, source)
    write_or_check(frontend / LICENSE_REPORT, json_bytes(licenses), args.check)
    write_or_check(frontend / MIGRATION_MANIFEST, json_bytes(migration), args.check)
    mode = "checked" if args.check else "generated"
    print(
        f"frontend-migration-audit: {mode}; "
        f"{migration['summary']['sourceFiles']} source files, "
        f"{migration['summary']['adaptedSourceFiles']} adaptations, "
        f"{licenses['summary']['records']} dependency license records"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (FileNotFoundError, RuntimeError, ValueError, json.JSONDecodeError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        raise SystemExit(1)
