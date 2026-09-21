# Recipe: Postgres Read-Only Replica Access

Read-only SQL access to production Postgres read replicas, for investigation workers that need
real record state (e.g. campaign fields) not visible through an app's admin UI or API. This is
READ-ONLY, full stop — read "Enforcement" before using it for anything.

## Requirements

Operator-managed env vars, sourced via `spore-with-secrets`:

- `M360_REPLICA_DB_URL` — read replica for the M360/marketer backend Rails database (campaigns,
  creative sets, campaign_package_instances, etc.)
- `PUBLISHING_REPLICA_DB_URL` — read replica for the Publishing service database.

Both are Postgres connection strings (`postgres://user:pass@host:port/dbname`). Resolve fresh per
call via `spore-with-secrets`, same as every other credential in this library — do not export
either into a long-lived shell.

`psql` is required and already present on this host.

## Enforcement — READ ONLY, no exceptions

These are production replicas.

1. **Always set the session to read-only at the Postgres protocol level**, not just by
   discipline: prefix every `psql` call with `PGOPTIONS="-c default_transaction_read_only=on"`.
   This makes Postgres itself reject any write statement — INSERT/UPDATE/DELETE/TRUNCATE/ALTER/
   DROP/COPY FROM/GRANT — with an explicit "cannot execute ... in a read-only transaction" error,
   regardless of what the connecting role's actual grants allow. This is the enforcement layer
   that matters — never connect without it.
2. **Only ever write SELECT statements.** No `SELECT ... INTO`, no `CALL`, no stored procedures
   that might mutate state.
3. **Always `LIMIT`** on any ad hoc query. These replicas serve real traffic elsewhere — an
   unbounded scan or heavy join is a cost imposed on a system you don't own the blast radius of.
4. **Never paste raw row output into anything committed to git** (task files, KB docs under
   `docs/investigations/`) without checking for PII first — names, emails, phone numbers, payment
   details. Summarize/redact before writing to a file that lives in git history permanently.
   Numeric IDs, enum/state columns, and timestamps are fine to cite directly; anything that looks
   like a person's data is not.
5. If a task genuinely needs a write — stop and ask the operator. Never work around read-only-
   ness that's inconvenient in the moment. This recipe covers reads, full stop.

## Worked example

    DB_URL=$(spore-with-secrets bash -c 'echo $M360_REPLICA_DB_URL')
    PGOPTIONS="-c default_transaction_read_only=on" psql "$DB_URL" -c \
      "SELECT id, phase, content_incomplete_at, publish_failed_at
       FROM campaigns
       WHERE id IN (64760, 66434, 69352, 66487)
       LIMIT 10;"

Confirm the read-only guard is actually active on a new connection before trusting it — attempt a
harmless no-op write once per session:

    PGOPTIONS="-c default_transaction_read_only=on" psql "$DB_URL" -c \
      "CREATE TEMP TABLE _ro_probe(x int);"

Expect: `ERROR: cannot execute CREATE TABLE in a read-only transaction` (or equivalent). If this
instead SUCCEEDS, stop immediately and tell the operator — the guard did not take, and this
connection must not be used until that's understood.

## Known schema pointers (grows over time — add as investigations discover more)

- `M360_REPLICA_DB_URL`: `campaigns` table carries `phase`, `content_incomplete_at`,
  `publish_failed_at`, `requires_manual_repair_at` — confirmed via the marketer repo's
  `app/models/campaign.rb` and migration `add_content_incomplete_at_to_campaigns` (see the
  `assistant` project's `docs/investigations/2026-09-21-campaigns-not-spending-missing-creatives.md`
  for the citation trail).
- `PUBLISHING_REPLICA_DB_URL`: schema not yet explored — first investigation that uses it should
  add what it learns here.

## Out of scope

- Writes of any kind — see Enforcement above.
- Schema migrations, admin tooling, anything beyond ad hoc read queries for investigation purposes.
