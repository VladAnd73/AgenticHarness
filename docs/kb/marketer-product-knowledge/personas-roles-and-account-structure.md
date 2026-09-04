# 👥 Personas, Roles & Account Structure

Source: https://app.notion.com/p/39614549bf5f8182bbceeb6c30b1c76d?pvs=204
Path: Engineering / Product Knowledge
Last edited: 2026-07-23T14:34:27.520Z

---

<callout icon="✅" color="green_bg">
	**Status: 🔵 Populated** (pending coordinator verification + operator section gate) · Last verified: <mention-date start="2026-07-08"/> · Owner: coordinator. Every claim below is grounded in **code** (authoritative), triangulated with the M2 backend `docs/` folder, the July 2026 Jira/Confluence reference export (**ref §2/§3**), a targeted **live-Jira** pass (11 persona/role keys pulled & re-checked 2026-07-08), and a **Linear** pass. **M2** = `marketer` Rails backend (`development`, read-only) · **M360** = `marketer-frontend` (this repo). Code paths are `path:line`. Entries marked **⚠️** flag a doc-vs-code (or Jira-vs-code) tension, resolved in the **code's** favour. Built to the same bar as the signed-off <mention-page url="https://app.notion.com/p/39614549bf5f81cab399cee317278b03"/> and <mention-page url="https://app.notion.com/p/39614549bf5f8152a955e98fb5158d64"/>; this page defines **who** uses the platform and **how accounts + permissions** are structured. It extends the Map's *Companies & Users* subsystem and the Glossary's *User / Role* and *Company* entries — reconciled here, not contradicted. **Partial backfill 2026-07-23 (release sweep — pending operator gate):** added the `fetch_from_hierarchy` nil-vs-`false` inheritance clause on §3a ([HK-224](https://linear.app/newbuilds/issue/HK-224)).
</callout>

## Scope
Who uses the Marketer platform and how the paying **customer organization** is structured: **user personas**, the real **roles & permissions** model, the **company / account** hierarchy, and what a **paying customer** actually gets (plans / tiers). Deep on the **Broker Experience** (estate agencies) and **New Builds** (property developers) user groups plus the company-account layer.

> **Two orthogonal axes to keep straight** *(both code-grounded)*: **(1) Experience** is a **company** property (`experience_type`) that decides *which product surface* a user sees (Projects/New-Builds vs Broker Experience). **(2) Role** is a **per-company** permission grant (`Users::Role` → abilities) that decides *what a user may do*. A user's experience comes from their company; their capabilities come from their role.

## How to read this page
Sources per claim: code `path:line` (**M2** / **M360**), M2 `docs/…`, **ref §2/§3** = `marketer-platform-product-reference.txt` (July 2026 export) with Jira keys, live **Jira** keys (re-checked <mention-date start="2026-07-08"/>), **Linear** issue IDs, and dated Notion links. *Uncited = unverified.* ⚠️ = doc/Jira-vs-code tension resolved in code's favour.

---
## 1. User personas
The platform serves **real-estate professionals and Marketer staff — not end consumers** (buyers/ad-recipients are explicitly out of KB scope; ref §2, and hub *Audience & perspective*). Personas below separate **code-grounded** facts (experience gating + the actual role model) from **product-intent** persona titles that come from Jira tickets that are **not shipped as specced** (⚠️ see the reconciliation note under §2).

<table fit-page-width="true" header-row="true">
<tr>
<td>Persona</td>
<td>Who they are & goals</td>
<td>What they do in the product</td>
<td>Experience they see</td>
<td>Sources</td>
</tr>
<tr>
<td>**Property developer / new-build sales**</td>
<td>Developers marketing new-build projects (clients e.g. Skanska, Selvaag, USBL). Goal: sell units across a project's sales stages.</td>
<td>Manage Projects → Sales Stages → Buildings → Units, Property Explorer, Offers/Buyers/Checkout, Project News; run project-level campaigns & leads.</td>
<td>**Developer** (company `experience_type = new_build_developer`)</td>
<td>M2 `app/models/company.rb:37,201`; M360 `src/hooks/useCompanyExperiences.ts:24-35`; ref §2 (PRODM-417); Glossary *Project*</td>
</tr>
<tr>
<td>**Estate agent / broker**</td>
<td>Sells existing (resale) homes; markets themselves and their listings. Goal: promote assignments, share results with sellers.</td>
<td>Order campaign packages per property/broker, manage Existing-Home listings & Order Management, share the login-less **Live Report** with sellers. A Broker is *also* a promotable marketing entity, not only a login.</td>
<td>**Broker** (company `experience_type = broker_agency`)</td>
<td>M2 `app/models/company.rb:38,201`; M360 `src/pages/BrokerExperience/`; Glossary *Broker*, *Broker Experience*, *Live Report*; ref §2 (MT3-8246, MT3-7107)</td>
</tr>
<tr>
<td>**Agency / office manager & company admin**</td>
<td>Runs an agency office or the customer company; oversees users and aggregated performance. Goal: manage seats-equivalent (users+roles) and see cross-office results.</td>
<td>Manages **company users & their roles** (invite/create/update/delete), sees dashboards. In code this is the **Manager / Organization Admin / Existing Homes Admin / Manager** role family with `users:*` abilities (see §2).</td>
<td>Broker or Developer (per company)</td>
<td>M2 `lib/tasks/roles/abilities.rake:39-54`; M360 `src/pages/Profile/Users/`, `src/drawers/UserDrawer/`; Jira MT3-8921 (Released)</td>
</tr>
<tr>
<td>**CSM / Marketer staff (internal)**</td>
<td>Marketer employees who configure companies, packages, projects, property explorers, and campaigns **on behalf of clients**.</td>
<td>Work primarily in the internal **Admin Panel** (Administrate) and as platform **super-admins** in M360 (see all companies). Not customer users.</td>
<td>Admin Panel + M360 as `super_admin`</td>
<td>M2 `app/models/user.rb:89`, `app/controllers/admin_panel/application_controller.rb:11,17`; `app/models/concerns/users/company_concern.rb:33-35`; ref §2 (PRODM-455 — ⚠️ mis-cited, see Gaps)</td>
</tr>
<tr>
<td>**External stakeholder / seller / partner (read-only)**</td>
<td>People linked to a product/deal who need to *view* results but not operate the platform — sellers, DNB/partner viewers, limited external stakeholders.</td>
<td>Read sales reports / Live Reports only; heavily restricted abilities (mostly `sales_reports*:read`).</td>
<td>Broker or Developer (scoped, read-only)</td>
<td>M2 `app/models/users/role.rb:16-19`, `lib/tasks/roles/abilities.rake:44-49`; Glossary *Stakeholder*</td>
</tr>
<tr>
<td>**Buyer / public** *(out of KB scope)*</td>
<td>End consumers who view property pages / submit offers. Named only for completeness.</td>
<td>Browse the public Portal/Storefront & Property Picker; submit Checkout offers. **No M360 login / role.**</td>
<td>Public (external Storefront)</td>
<td>Map §3 *Portal / Storefront*; Glossary *Checkout*, *Offer* (excluded per hub *Non-goals*)</td>
</tr>
</table>

> ⚠️ **Persona-title reconciliation (product-intent vs code).** Ref §2 lists tidy EM1 org titles — *Real Estate Agent, Department Manager, Managing Director, Marketing Manager, Product Owner* — sourced from **MT3-8246** (Feature, **status TODO**, "EM1 – Role-Based Reporting Dashboards") and **MT3-8229** (Feature, **status Rejected**, "EM1 – Roles – Implement Defined Access Model"). Both were pulled live 2026-07-08: **neither shipped as specced.** They describe *intended* business personas, **not** code role names. The **shipped** role model is the `Users::Role` set in §2 below, and it *is* live in production — corroborated by **MT3-8921** (Bug, **Released**): "EM1 Manager role cannot see Campaigns and Leads despite correct abilities", which confirms a real *Manager* role gating `campaigns`/`leads` by ability strings. Where the two disagree, **code wins**.

---
## 2. Roles & permissions (the shipped model)
M2 implements a **role → ability** permission system enforced with the `action_policy` gem (M2 `Gemfile:12`). There are **two distinct role concepts** in code — one live, one deprecated — plus a platform super-admin flag. **There is no "seat" entity: access = User + per-company Role assignment** (Glossary *User / Role*). This **agrees with** ref §2/§3, which likewise documents no seat-based licensing — it is not a correction of the ref.

### 2a. Company roles — `Users::Role` (LIVE)
The real, company-scoped roles. Table `user_roles`; 15 named roles with a weight used for "minimum role" scoping (M2 `app/models/users/role.rb:7-40`). A role owns a set of **abilities** through `role_abilities` (`role.rb:42-48`).

<table fit-page-width="true" header-row="true">
<tr><td>Role (`role_name`)</td><td>Weight</td><td>Abilities granted *(from config + rake seed)*</td></tr>
<tr><td>`Start`</td><td>0</td><td>`leads:create, campaigns:update, leads_projects:read, analytics:read, ads:read`</td></tr>
<tr><td>`Project`</td><td>1</td><td>`projects:read, stages:read, buildings:read` (+ `leads:create, campaigns:update, analytics:read, ads:read`) — **7 abilities**</td></tr>
<tr><td>`Developer`</td><td>2</td><td>`projects:create, stages:create, buildings:create, users:invite` (+ leads/campaigns/analytics/ads)</td></tr>
<tr><td>`Enterprise`</td><td>3</td><td>as Developer, but `campaigns:create` **instead of** `campaigns:update`</td></tr>
<tr><td>`Manager`</td><td>4</td><td>full `users:{create,read,update,delete,invite}`  • projects/stages/buildings/campaigns create + analytics/ads/leads</td></tr>
<tr><td>`Existing Homes Manager`</td><td>4</td><td>weight only in code; abilities assigned per-company / via admin panel (not in the base rake map)</td></tr>
<tr><td>`Existing Homes Admin`</td><td>4</td><td>weight only in code (not in base rake map)</td></tr>
<tr><td>`Organization Admin`</td><td>99</td><td>projects/stages/buildings (read+create), `sales_reports*`, `leads_projects:read, analytics:read`, full `users:*`</td></tr>
<tr><td>`Himla`</td><td>1</td><td>`campaigns:create, ads:read` (brand/customer-specific role)</td></tr>
<tr><td>`EmVest Agent`</td><td>1</td><td>weight only in code (EM1/EmVest brand role; abilities per-company)</td></tr>
<tr><td>`ERA Agent`</td><td>1</td><td>weight only in code (ERA brand role; abilities per-company)</td></tr>
<tr><td>`DNB User`</td><td>1</td><td>`projects:read, sales_reports:{read,update}, sales_reports_excel:read, sales_reports_leads_table:read, users:invite`</td></tr>
<tr><td>`External stakeholder`</td><td>0</td><td>`sales_reports:read, sales_reports_excel:read, sales_reports_leads_table:read`</td></tr>
<tr><td>`Limited external stakeholder`</td><td>0</td><td>`sales_reports:read`</td></tr>
<tr><td>`Seller`</td><td>0</td><td>weight only in code (read-only seller viewer)</td></tr>
</table>

*Sources:* role names + weights M2 `app/models/users/role.rb:7-40`; role→ability seed map M2 `lib/tasks/roles/abilities.rake:26-59` (+ `add_new_roles` 68-86; one-time top-ups under `lib/tasks/one_time/add_*_ability.rake`). Verified <mention-date start="2026-07-08"/>. Note: several brand/EH roles have a weight but no abilities in the base seed — their abilities are attached per company (the rake and admin panel both use `find_or_create`), which is why **MT3-8921** could ship a real "EM1 Manager" role with a bespoke ability set.

### 2b. Ability vocabulary
An **Ability** (`Users::Ability`, table `user_abilities`, unique `value`; M2 `app/models/users/ability.rb:4-15`) is a `resource:action` string. The **base new-build ability namespace** is defined declaratively in **M2 `config/role_abilities.yml`** and materialised by the `roles:abilities:update` rake task (`lib/tasks/roles/abilities.rake:8-13`):

<table fit-page-width="true" header-row="true">
<tr><td>Resource</td><td>Actions</td></tr>
<tr><td>`projects`</td><td>read, create</td></tr>
<tr><td>`stages`, `buildings`</td><td>read, create</td></tr>
<tr><td>`campaigns`</td><td>read, update, create</td></tr>
<tr><td>`leads`</td><td>read, create</td></tr>
<tr><td>`leads_projects`</td><td>read</td></tr>
<tr><td>`analytics`</td><td>read</td></tr>
<tr><td>`ads`</td><td>read</td></tr>
<tr><td>`users`</td><td>read, create, update, delete, invite</td></tr>
<tr><td>`sales_reports`</td><td>read, update</td></tr>
<tr><td>`sales_reports_excel`, `sales_reports_leads_table`</td><td>read</td></tr>
</table>

*Source:* M2 `config/role_abilities.yml` (verified <mention-date start="2026-07-08"/>). This confirms ref §2's example ability strings (`campaigns:read`, `leads:read`, `projects:read`, `analytics:read`) against code. ⚠️ **`role_abilities.yml` is NOT the complete ability namespace.** Additional resource families are enforced directly in policies yet are **absent from the YAML and unseeded** anywhere in code: `brokers:{read,create,update,delete}` (M2 `app/policies/broker_policy.rb:24,32`), `existing_homes:{read,create,update,delete}` (M2 `app/policies/existing_home_policy.rb:24`), and `vitec_documents:read` (M2 `app/policies/vitec/document_policy.rb:12`) — grep of `role_abilities.yml` for these families returns zero hits. These abilities are attached **per company** (admin panel / one-time tasks) — the same mechanism that gives the broker / Existing-Homes brand roles in §2a their ability sets (see Gaps). So the YAML is the *new-build seed namespace*, not the whole permission vocabulary. ⚠️ Ref §2 also claims abilities are "scoped by level (own user / department / company / corporation)" — **no such level scoping exists in the ability value**; scoping is done separately by walking the **company hierarchy** (§2d), not encoded in the ability string.

### 2c. Deprecated **product roles** — `Users::ProductRole`
A separate, older per-product role concept (table `user_product_roles`; enum `admin / agent / stakeholder`; jsonb `abilities`) assigned via `Users::ProductRoleAssignment`. It is **marked for removal** (M2 `app/models/users/product_role.rb:3-4` → "TODO, remove product role" / GitHub issue #1645). Its ability set (`access_admin_console`, `access_live_report`) is seeded from **M2 `config/user_roles.yml`** via `users:roles:update` → `Users::Roles::UpdateList` (M2 `lib/tasks/users/roles.rake:3-17`, `app/services/users/roles/update_list.rb`). ⚠️ **Naming trap:** `User#roles` returns these deprecated **ProductRoles**, while `User#company_roles` returns the live `Users::Role`s (M2 `app/models/user.rb:60-70`). The `user_companies.product_role` string column (M2 `docs/subsystems/companies_and_users.md` schema) is the legacy remnant.

### 2d. How permissions are enforced
`ApplicationPolicy < ActionPolicy::Base` (M2 `app/policies/application_policy.rb`) defines the checks used across controllers/GraphQL:
- **Super-admin bypass** — `admin?` ⇒ `user.super_admin?` (`:15-17`). `super_admin` is a boolean on `users` (M2 `app/models/user.rb:89` scope `:admins`; schema `users.super_admin`).
- **Opt-in gating** — if the company has `with_user_roles = false`, members get **default (full) permissions** (`default_permissions_in_company?`, `:22-30`; company scope `without_roles` = `where(with_user_roles: false)`, M2 `app/models/company.rb:204`). Granular role gating only applies to companies that **opt in** (`with_user_roles = true`).
- **Ability check** — `with_permissions_in_company_to?(values, company)` tests `user_company.role&.permissions(values)` (`:32-40`; `Users::Role#permissions` → `abilities.where(value:)`, `role.rb:65-67`). `permissions_in_any_company?` joins `role → role_abilities → ability` by value (`:52-57`).
- **Hierarchy cascade** — checks resolve up the company tree: `user_companies_with_ancestors` prefers a parent-level `user_company` (`:42-50`), so a **parent-company role cascades down to child companies**.
- Permission snapshots for the client come from `User#company_permissions` → `[{company_id, with_user_roles, roles: [ability values]}]` (M2 `app/models/concerns/users/company_concern.rb:5-15`). *(Live-verified by MT3-9473, Released: "Fix nil crash in **`company_permissions`** when user's company is soft-deleted".)*

### 2e. How M360 consumes it
- The `me` endpoint maps `user_type = super_admin? ? :admin : :user` (M2 `app/serializers/m360/v1/me_serializer.rb:15-16`); the frontend `User.userType` is `'user' | 'admin'` accordingly (M360 `src/entities/user.ts:14`).
- Assignable roles per company come from GraphQL `companyRoles(companyUuid)` → `company.roles` (M2 `app/graphql/queries/companies/roles.rb`, `app/graphql/types/query_type.rb:154`, `RoleType{id, role_name}` `app/graphql/types/role_type.rb`), consumed by M360 `src/drawers/UserDrawer/hooks/useCompanyRoles.ts` and rendered in `src/drawers/UserDrawer/components/RoleField.tsx` (single-select of a company's enabled roles). A user's role is set via `roleId` on `UserInput` (M360 `src/entities/user.ts:33-44`).
- User management UI: M360 `src/pages/Profile/Users/` + `src/drawers/UserDrawer/AddEditUserDrawer.tsx`; user list via GraphQL `companyUsers` (M360 `src/hooks/useCompanyUsers.ts`). User lifecycle states: `active | invite_sent | deactivated` (M360 `src/entities/user.ts:47`).
- **Experience gating** (which product surface): `PrivateExperienceRoute` (M360 `src/contexts/protectedRoutes/ProtectedExperienceRoute.tsx:13-24`) using `useCompanyExperiences()` (`src/hooks/useCompanyExperiences.ts:19-37`) — `broker-experience/*` needs broker experience; `projects`/`campaigns`/`leads`/`analytics` need developer experience. This is **company-level**, independent of role.

### 2f. Marketer staff & the Admin Panel
The internal back-office is **Administrate**-based (M2 `app/controllers/admin_panel/application_controller.rb` → `Administrate::ApplicationController`, `before_action :authenticate_admin!`). Staff authenticate via a dedicated `admins` Devise scope with Google SSO (M2 `config/routes.rb:304-319`, `devise_for :admins` + `admins/auth/google`). The `Admin` model is a `super_admin` **subclass of User** (M2 `app/models/admin.rb:4-6`, `default_scope where(super_admin: true)` — "we need this for Administrate"). ⚠️ A separate `admins` table also exists in the schema (M2 `docs/subsystems/companies_and_users.md` → schema `admins`); its relationship to `Admin < User` (which reads the `users` table) is not fully resolved here — see Gaps.

---
## 3. Customer org / account structure

### 3a. Company = the paying customer
`Company` (M2 `app/models/company.rb`) is the top-level org entity: a paying customer. Key structural facts (schema in M2 `docs/subsystems/companies_and_users.md`, model as cited):
- **Self-referential hierarchy** — `parent_id` (`belongs_to :parent` / `has_many :children`, M2 `app/models/company.rb:51-89`). Recursive up/down traversal via `Companies::Hierarchy` (`with_ancestors` / `with_descendants` SQL CTEs, M2 `app/models/concerns/companies/hierarchy.rb`) plus `top_parent`, `fetch_from_hierarchy`, `with_ancestors` helpers (`company.rb:258-296`). A parent (e.g. an EM1 head office) nests regional/subsidiary child companies; config and role access cascade down the tree. **`fetch_from_hierarchy` inheritance rule (partial backfill 2026-07-23 — pending operator gate):** a child's `nil` / unset value inherits the nearest ancestor's, but an **explicit `false` overrides and stops the walk-up** (a child `false` beats a parent `true`) — so a boolean setting can be turned *off* at a child even when an ancestor has it on. The live boolean case is the Vitec campaign-tag settings read by `MarkChecklistJob` (`settings.vitec.order_campaign_tag`, and its sibling `order_campaign_kfs_tag`). ([HK-224](https://linear.app/newbuilds/issue/HK-224), PR [#14074](https://github.com/marketertechnologies/marketer/pull/14074).) *(Corroborated: Jira MT3-9447 Released "products_companies WITH RECURSIVE…"; Linear HK-188 Done — "EM1 subsidiaries" want per-package settings under a parent.)*
- **Experience type** — `experience_type` enum `new_build_developer | broker_agency`, default `new_build_developer` (M2 `app/models/company.rb:37-43,201-252`; mirrored M360 `src/entities/company.ts:12`). Determines the product surface (see §2e). A company is one experience; a user with companies of both types sees both.
- **Users & roles** — `users` ↔ `companies` many-to-many through `Users::Company` (`user_companies`), each row optionally carrying a `Users::Role` via `user_role_id` + a `changed_permissions` flag (M2 `app/models/users/company.rb:4-15`; unique per `(company_id, user_id)`). A user can hold **different roles in different companies**. Which roles a company *offers* is the `company_roles` join (`Companies::Role`, M2 `app/models/companies/role.rb`; unique `(company_id, user_role_id)`). A company has a `default_new_user_role` (M2 `app/models/company.rb:57`; schema `default_new_user_role_id`) auto-applied to new/provisioned users.
- **Products** — company ↔ promotables through the `products_companies` join (`Products::Company`; M2 `app/models/company.rb:63-64`); one product can attach to multiple companies. (See Glossary *Product*, *Promotable*.)
- **Company-level config** (what varies per customer): CRM credentials & source, ad-platform accounts (Facebook/Snapchat/DV360/Google), brand colours & template colours, custom domains & module subdomains, Facebook pages, GMP/Snapchat config, `currency` (default NOK), `spending_limits` (jsonb, per-currency; M2 `app/models/company.rb:354-375`), and many feature booleans (`portal_enabled`, `properties_enabled`, `sales_report_enabled`, `publishing_service_enabled`, `use_dynamic_creatives`, `managed_stakeholders`, …). Schema: M2 `docs/subsystems/companies_and_users.md` (`companies` table).
- **Storefront API token** — each company gets a long-lived token generated on create (M2 `app/models/company.rb:397-403`; schema `storefront_api_token`). *(Jira MT3-7698 Released — "allScopes token per company".)*
- **Default packages** — creating a `broker_agency` company auto-seeds the default campaign packages (M2 `app/models/company.rb:389-395`).

### 3b. Account-structure diagram (code-grounded)
```mermaid
graph TD
  subgraph Platform
    SA["super_admin flag on User<br/>(Marketer staff / CSM)"]
  end
  Parent["Company (parent)<br/>experience_type, settings, feature flags"]
  Child["Child Company (subsidiary/office)<br/>parent_id → Parent"]
  Parent -->|parent_id self-ref| Child
  Parent -->|company_roles| CR["Companies::Role<br/>(which Users::Role are enabled)"]
  CR --> UR["Users::Role (user_roles)<br/>15 named roles + weights"]
  UR -->|role_abilities| AB["Users::Ability (user_abilities)<br/>resource:action strings"]
  User["User (Devise/JWT login)"]
  User -->|user_companies (Users::Company)| Parent
  User -->|user_companies.user_role_id| UR
  User -.->|products_companies| Prod["Product → Promotable"]
  Parent -->|products_companies| Prod
  SA -.->|bypasses role checks,<br/>sees all companies| Parent
```
*Sources:* as cited in §2–§3. `super_admin` sees all companies via `companies_for_m360` (M2 `app/models/concerns/users/company_concern.rb:33-35`).

---
## 4. Plans / tiers — what a paying customer gets
**Code-grounded finding: there is no subscription / plan / tier / seat model in the codebase.** There is no `Plan`, `Tier`, `Subscription`, or `Seat` model; access is **not** metered per seat (Glossary *User / Role*: "no seat concept in code"). What a customer gets is governed by **four per-company mechanisms**, not a tier:
1. **Experience type** — `new_build_developer` vs `broker_agency` selects the whole product surface (§3a).
2. **Feature flags** — Flipper-backed, per-company (actor) toggles gate capabilities (dynamic creatives, snapshots, publishing service, CRM sync, mailer blocking, …): M2 `app/models/company.rb:319-352`, `app/services/feature_flag.rb`; Glossary *Feature Flag*.
3. **Company config booleans** — e.g. `portal_enabled`, `properties_enabled`, `sales_report_enabled`, `publishing_service_enabled` (schema, §3a) enable/disable areas per customer.
4. **Campaign Packages** — the actual sold/billed unit: packages assigned to a company (`company_campaign_packages`) and instantiated per property/broker; **billing is per Campaign Package Instance** via `InvoiceBase`/`InvoiceBaseItem` exported to xledger (`company.xledger_customer_code`, `invoice_frequency`; Glossary *Billing / Invoice*, *Campaign Package*). There is **no per-seat billing**.

⚠️ This matches ref §2/§3's own gap note ("No documentation… of pricing plans/tiers or seat-based licensing… feature-gated model rather than tiered subscriptions"). The role names `Start / Project / Developer / Enterprise` *look* tier-like but are **permission roles** (with weights), **not** commercial plans (M2 `app/models/users/role.rb:7-40`).

---
## Gaps & uncertainties
- **Product-intent personas are not shipped roles (⚠️ resolved in code's favour).** Ref §2's EM1 org personas (Real Estate Agent, Department Manager, Managing Director, Marketing Manager, Product Owner) derive from **MT3-8246 (TODO)** and **MT3-8229 (Rejected)** — pulled live 2026-07-08, neither shipped as specced. §1/§2 present the **code** role model; the two are bridged by **MT3-8921 (Released)**. Treat the org titles as *business language*, not system roles.
- **`admins` table vs `Admin < User` (unresolved).** The schema has a standalone `admins` Devise table *and* `devise_for :admins` with Google SSO, yet the `Admin` model subclasses `User` (reads the `users` table via `super_admin`). Whether staff auth actually hits the `admins` table or `users` was **not fully traced**; the admin-panel auth (`authenticate_admin!`) and the two tables may be a legacy overlap. Flagged, not asserted.
- **Ref §2 mis-citation.** Ref §2 cites **PRODM-455** for the "CSM" persona, but PRODM-455 (live-checked) is "Add filters to PP configurator" — unrelated. The CSM persona rests on the Admin-Panel/super-admin code evidence, not that ticket. **PRODM-417** (cited for the developer persona) is actually about project archiving — it corroborates *developer clients* only decoratively.
- **Per-company / brand-role abilities not fully enumerated.** Several `Users::Role`s (Existing Homes Manager/Admin, EmVest Agent, ERA Agent, Seller) carry a weight but **no abilities in the base rake seed**; their real ability sets are attached per company (admin panel / one-time tasks) and were **not enumerated from a live DB** (staging not exercised). The base seed also only auto-creates `company_roles` for Start/Project/Developer/Enterprise (M2 `lib/tasks/roles/abilities.rake:61-65`).
- **Provisioning & role precedence** (ref §2: manual override → company default → system fallback; CRM-driven auto user lifecycle for EM1) come from **MT3-8228 (Epic, TODO)** — product intent, only partly reflected in code (the `default_new_user_role` field is code-grounded; the full precedence chain and CRM-driven activation were not traced end-to-end here).
- **Google SSO breadth.** Ref §2 says Google SSO was whitelisted to Marketer admins, "being expanded to all users" — **PRODM-416 (Idea, New/not done)**. M360 customer login supports email/password + Google/Microsoft SSO (see Map *Login/Auth*); the staff `admins` Google SSO is separate.
- **Staging not exercised** (no credentials attempted) — role labels/ability behaviour are grounded in code (models, rake seeds, i18n), not a live screen. Same posture as the Glossary/Map.
- **Linear pass: no roles/permissions/plans documentation found.** Linear `search_documentation` returned only generic Linear product docs; issue search surfaced fork/feature work (HK-188 corroborates EM1 parent/subsidiary hierarchy) but nothing defining the persona/role/plan model. No Linear source contradicts the code.
- **Jira pass targeted, not exhaustive** — 11 persona/role keys pulled & re-checked 2026-07-08 (MT3-8246/8229/8228/8921/8167/9447/9473/7698, PRODM-416/455/417): **0 inaccessible**; 3 status/scoping caveats surfaced (above); MT3-8921 & MT3-9473 (both Released) directly corroborate the live role/ability + `company_permissions` code.
