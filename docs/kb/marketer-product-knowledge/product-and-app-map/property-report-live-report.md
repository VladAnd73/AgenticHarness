# 🧾 Property Report (Live Report) — the shareable per-property/broker campaign-performance report (multi-source deep-dive)

Source: https://app.notion.com/p/3a614549bf5f81a18425e64e691e7739?pvs=204
Path: Engineering / Product Knowledge / Product & App Map
Last edited: 2026-07-23T08:27:09.855Z

---

<callout icon="🔵" color="blue_bg">
	**Status: 🔵 Populated** · <mention-date start="2026-07-23"/> · Owner: coordinator · multi-source deep-dive. **Code = ground truth for behaviour; Jira/Notion = intent.** Verification bases: property-report SPA @`b405fa07`, M2 @`ef140820`, M360 @`e83942c4`. The SPA was **cloned read-only and verified section-by-section** (no longer inferred). Sibling of the [CRM Gateway deep-dive](crm-gateway.md); does not re-assert the ✅ Product & App Map claims.
</callout>

## What it is
Every property (and, in the Agent-Promotion variant, every promoted broker) gets a **shareable, login-free, tokenised URL** showing that listing's marketing-campaign performance, branded per company, for the broker to share with the seller — *"sent to the seller when the first insights are populated on the first live campaign and also available from the product listing page"* (Notion PRD "Property - Live report"; delivery epic MT3-8277, also linked from the SPA README).

## Where it lives (behaviour)
A **separate single-page app** — repo `property-report`, prod `report.marketer.tech` / staging `report-staging.marketer-services.dev` (README), browser-tab title "Live Report" (index.html). **Not** part of the M360 SPA — M360 only builds the outbound link (M360 `src/utils/propertyReport.ts`, `src/hooks/useLiveReportUrl.ts`), gated by the GraphQL feature flags **`existing_homes_live_report`** / **`brokers_live_report`**. Its only data source is the M2 **storefront_api** role (`VITE_STOREFRONT_API_URL`; `.env.example`, `getStorefrontUrl.ts`; see Product & App Map §3 Portal/Storefront, `APP_ROLE=STOREFRONT_API`).

## URL + auth
Single route `/{companyUuid}/{reportId}` (`$companyUuid.$reportId.tsx:27`) = M2's `live_report_url = "#{LIVE_REPORT_URL}/#{company.uuid}/#{report_id}"` (`existing_home.rb:122-124`). **Login-free but token-bearing:** it fetches a per-company public credential from `GET /api/v1/clients/{companyUuid}/credentials` (`fetchToken.ts`) and sends it as `Authorization: Bearer` on every request (`fetcher.ts`) — no user login, no route guard.

## Two variants (same report, different promotable)
Report id is a composed prefixed id — `eh_<uuid>` (ExistingHome/property) or `br_<uuid>` (Broker/Agent Promotion); a bare uuid → ExistingHome for back-compat, resolved **M2-side** (`promotable_resolver.rb:12-53`). The SPA switches on the summary's `promotable_type: 'broker' | 'existing_home'` (`useSummary.ts:26`, `$companyUuid.$reportId.tsx:57`). The **Agent-Promotion variant** (MT3-8867) is reachable from the Agent-Promotion campaign page ("View Live Report", MT3-8945), emails the agent when metrics arrive, and **hides marketplace-specific sections** (External Statistics — MT3-8951).

## What the report contains (verified section-by-section, `$companyUuid.$reportId.tsx:80-102`)
1. **Header** — per-company branding (logo + `template_background_color`/`template_text_color`), property address, a channel selector + a duration/period selector (`Header.tsx`).
2. **Progress** — campaign-duration card ("N days remaining of M total" + bar; "No active campaign") (`ProgressSection.tsx`).
3. **Campaign Performance** — insight cards + a **chart view and a table view with a metric switcher**; the four displayed metrics are **Impressions, Reach, Clicks, and Leads** (`interest`) (`ChannelPerformanceSection.tsx`, `configs/metrics.tsx`). Channels: Facebook, Instagram, Snapchat, Google Ads (`gmp`), Google Search (`configs/channels.ts`).
4. **Best Performing Ads** — "Best Performing Ads / Based on clicks"; a **grid/table view switch** (+ mobile carousel) and an ad-detail modal with per-platform previews (Facebook + GMP preview endpoints; Snapchat/Instagram render from ad data) (`Ads/AdsSection.tsx`, `entities.ts:134-179`).
5. **External Statistics** — Finn.no + Eiendomsmegler portal stats. **Rendered only when `promotable_type === 'existing_home'` AND `location.country_code === 'NO'`** (`$companyUuid.$reportId.tsx:96`) — hidden for the Broker variant (MT3-8951) **and** for non-Norwegian properties. Finn = clicks/email-notifications/messages/favourites/push; Eiendomsmegler = leads/showing-registrations/document-downloads (`entities.ts:77-91`).

## Data surface (storefront_api)
All under `/api/v1/property_report/{reportId}/…`: `summary`, `channels_totals`, `channels_analytics/{channelType}`, `ads`, `ads/{adUuid}/platforms[/{platformId}/insights]`, `preview_facebook_ad/{adUuid}`, `preview_gmp`, `external_statistics/{finn,eiendomsmegler}` (SPA `src/hooks/api/`; M2 `config/routes/storefront_api.rb`).

## Delivery (push is M2, not the SPA)
The report link is **pushed to the broker/seller** once the first insights land — by **email** (two mailers, `broker` + `existing_home`, per-company configurable — Notion "Live Report Email Configuration") and by **SMS via the Vitec/Webtop API** for CRM clients (PRD; MT3-8340…8648; `live_report_sms_text` — M2 `existing_home.rb:125-127`).

> ⚠️ **Code-verified:** the SPA itself carries **no in-app share/email/SMS control** — it is display-only; distribution is entirely M2-side. (Refines the Jira "share button" items MT3-8289/8666, absent at this SHA.)

## How you get here (cross-links, not repeated)
For a CRM-integrated broker the report is the tail of the "order from inside the CRM" flow — see the [CRM Gateway deep-dive](crm-gateway.md) (Flag 2) and the [Flow deep-dive: Broker Experience — CRM Order → Live Ads → Completion on Sale](../user-flows-and-journeys/flow-broker-experience-crm-order.md). **Order Management** (the post-order portal) is a *separate* M360-native surface — see Product & App Map §1 "Order Management".

## Sources & gaps
- Behaviour: property-report SPA @`b405fa07` + M2 storefront_api @`ef140820` (permalinks inline). Intent: Jira MT3-8277/8867 (+children); Notion PRD + Email-config.
- ⚠️ Epic status is **TODO** for both epics (MT3-8277, MT3-8867) despite shipped children + live code — not a delivery signal in this tenant.
- The `br_`/`eh_` prefix + report generation are M2-side; the SPA switches on `promotable_type`.
- Locales in the SPA = 7 (de, en, fi, fr, nl, no, sv) — a superset of M360's five (adds Finnish + Swedish).
