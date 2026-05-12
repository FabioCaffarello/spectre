# Spectre v1alpha2 pilot questionnaire

Per-layer structured questionnaire for collecting
real-deployment feedback to inform Spectre's v1alpha2
Wave 5+ priorities. Mirrors the framework's seven-layer
platform-vs-driver domain map.

## §0 — How to use this questionnaire

This questionnaire collects **qualitative feedback** from
operators running production web-scraping workloads —
spectre users, prospective users, or anyone running a
mature scraping stack on something else. The goal is to
inform Spectre's [Wave 5+ priorities](../roadmap.md) in
v1alpha2: which infra-services to ship first, which
features matter most, which trade-offs the architecture
should optimise for.

**How the answers feed the roadmap.** Three concrete
decisions take pilot answers as direct input:

- **W5.1 — proxy-broker provider picks.** Per
  [ADR-0028 §5 criterion #2](../adr/0028-ancillary-infra-services-catalog.md),
  the first inhabitant of `infra-services/` must wire at
  least two providers (the canonical
  two-provider-design admission gate). Pilot answers from
  §1 directly inform which two of BrightData / Oxylabs /
  Smartproxy / self-hosted are wired first.
- **W5.1 feature scope.** Rotation policies, geo-targeting,
  per-domain pinning, cooldown semantics — the answers in
  §1 calibrate which proxy capabilities land in W5.1
  versus later iterations.
- **W5.2+ slot ordering.** The
  [15-service catalog](../adr/0036-microservices-catalog-expansion.md)
  has a baseline ordering, but pilot answers (especially
  §8 synthesis) may reorder Waves 5 – 10 based on real
  demand.

**Ground rules for respondents.**

- Pick the sections **relevant to your deployment**. Not
  every respondent answers every section.
- Questions are **open-ended** by design. Specifics beat
  generalities — "we use Smartproxy residential at
  $X/GB, sticky sessions per domain" is signal;
  "we use proxies" is noise.
- "I don't have data on this" or "we haven't hit this
  yet" are valid answers. Gaps are signal too.
- Answer in the format that suits you (prose, bullet
  points, links to internal runbooks). The maintainer
  aggregates qualitatively.

**Confidentiality.** Answers are aggregated anonymously
by default. Attributed quotes appear in roadmap updates
only with explicit respondent permission. Provider names,
spend figures, and architecture details inside answers
stay with the maintainer unless the respondent opts in.

**No deadline.** The questionnaire stays open through
Wave 5+; the maintainer pulls answers as they come during
W5.1 review cycles.

---

## §1 — Acquisition layer (Layer 1)

Proxy infrastructure, CAPTCHA defeat, browser fingerprinting,
rate-limiting, session/auth handling — everything that
gets the scraper past the wire and into the target site
without being blocked.

### §1.1 — Proxy providers

1. Which proxy providers are you using today, and at what
   monthly spend per provider?
2. Why those providers specifically — pricing, IP pool
   diversity, geo coverage, API quality, support
   responsiveness?
3. What's the biggest day-to-day friction with your
   current proxy provider(s)?

### §1.2 — Proxy rotation + targeting

4. What rotation policies do you run — random per-request,
   sticky sessions, per-domain pinning, geo-targeted, a
   custom mix?
5. Which jobs (or target sites) require geo-specific
   exit IPs, and how often does that constraint cost you
   blocked requests?

### §1.3 — CAPTCHA + bot detection

6. How often do CAPTCHAs appear in your scrape paths
   (per N requests, roughly), and which solver do you
   pay for if any (2Captcha / Anti-Captcha / CapMonster /
   in-house)?
7. Have you hit anti-bot products (Cloudflare Turnstile,
   PerimeterX, DataDome) that defeated your current
   stack, and how did you work around them?

### §1.4 — Fingerprint + session

8. Where does browser/TLS fingerprinting concern you most
   — header consistency, TLS JA3/JA4 evasion, JS runtime
   detection, canvas/font fingerprints?
9. What does your session/auth shape look like for sites
   that require login — cookie persistence, OAuth flows,
   multi-step credential rotation, MFA handling?

---

## §2 — Workflow layer (Layer 2)

DSL primitives, multi-step navigation, pagination patterns,
conditional logic — how a scrape job is *expressed*
versus what it does at runtime.

### §2.1 — Job shape

1. What fraction of your jobs are single-page versus
   multi-page (clicks, scrolls, form fills, multi-step
   navigations)?
2. Which multi-step operations show up most often —
   login flows, filter selection, infinite scroll
   exhaustion, paginated list traversal, form
   submission with state?

### §2.2 — Pagination

3. Which pagination patterns dominate in your targets —
   numbered page links, "load more" buttons, infinite
   scroll, AJAX cursor pagination, sitemap walks?
4. Where does pagination cost you the most engineering
   time today — endless-scroll exit conditions, page-N
   discovery, dedup across overlapping pages?

### §2.3 — Conditional logic + DSL friction

5. What conditional logic do your scrape jobs need —
   if-element-exists branching, retry-with-different-driver
   fallback, schema-validated extraction with branching
   on failure?
6. What can your current scraper DSL *not* express that
   forces you to drop into raw code? Be specific about
   the use cases.
7. If you've evaluated multiple scraping DSLs (Spectre,
   Scrapy, Playwright scripts, Apify actors, in-house), what
   pushed you toward your current choice?

---

## §3 — Quality / observability layer (Layer 3)

Debugging, metrics, failure-mode taxonomy — what you
look at when a scrape job goes wrong, and what you
*wish* you could look at.

### §3.1 — Debugging spend

1. Where do you spend the most debugging time per week —
   selector breakage from site redesigns, proxy issues,
   anti-bot blocks, parsing errors, schema drift,
   something else?
2. When a job starts producing zero rows, what's your
   first diagnostic step — driver replay, raw HTML
   capture, network trace, proxy provider's dashboard,
   something else?

### §3.2 — Metrics you watch

3. Which operational metrics do you actively watch —
   success rate, rows-per-minute throughput, cost per
   row (proxy + compute), data freshness (lag from
   target update), other?
4. What thresholds or alerts do you have wired today,
   and which ones fire too often versus too rarely?

### §3.3 — Failure-mode taxonomy

5. If you had to bucket job failures, what categories
   dominate — transient network, target-side block,
   selector breakage, schema mismatch, downstream sink
   failure, dependency outage?
6. What quality signal do you wish existed but doesn't —
   per-(target, driver) success ratio, dedup collision
   ratio, extraction completeness, schema-validation
   pass rate, something else?

---

## §4 — Output / data layer (Layer 4)

Sinks, formats, enrichment, deduplication — what
happens to the rows once the scrape extracts them.

### §4.1 — Sinks + formats

1. Which output sinks do you write to in production —
   Kafka, S3 / object storage, webhooks, Postgres /
   relational, NoSQL, message queue (SQS / Pub/Sub),
   files on disk?
2. Which output formats matter — JSONL, Parquet, CSV,
   Avro, protobuf, custom binary? And do you transform
   between formats downstream?

### §4.2 — Enrichment

3. What enrichment runs on extracted rows before they
   reach the durable sink — geocoding, NLP / sentiment
   / classification, embeddings, currency conversion,
   entity resolution against existing rows?
4. Is enrichment inline (per-row in the scraper) or
   batched downstream, and what drove that choice?

### §4.3 — Dedup + schema evolution

5. How do you handle duplicate rows — natural-key dedup
   at the sink, hash-based collision detection, append-only
   with downstream resolution, no dedup needed?
6. How often does the output schema change for a given
   target, and how does that propagate to downstream
   consumers — versioned schemas, additive-only, breaking
   bumps, runtime tolerant readers?

---

## §5 — Input management layer (Layer 5)

URL queues, batch shape, lifecycle tracking — where
the URLs to scrape *come from* and how they progress
through the system.

### §5.1 — URL sourcing

1. Where do the URLs you scrape originate — manual
   lists / spreadsheets, sitemap discovery, API push
   from upstream systems, queue consumption, seeded
   crawl with link extraction, customer-supplied
   per-job?
2. What fraction of your URLs are reusable across runs
   (steady catalog) versus single-shot (new every day)?

### §5.2 — Batch shape

3. Is your scraping continuous (always-on consumers
   pulling from a queue) or batch (cron-triggered runs
   that process a finite list)? Per-batch sizes?
4. How do you prioritise URLs within a batch — FIFO,
   freshness-driven (oldest-first), priority class,
   per-tenant fairness, random?

### §5.3 — Lifecycle + scheduling

5. What's your re-queue policy on failure — immediate
   retry, exponential backoff, dead-letter after N
   attempts, manual triage?
6. How do you deduplicate already-seen URLs — Bloom
   filter, Redis set, database lookup, no dedup needed?
7. What scheduling shape drives jobs today — cron,
   event-triggered (webhook / queue message), on-demand
   (API call), data-source-aware (poll source's
   `updated_at`)?

---

## §6 — Driver abstraction layer (Layer 6)

Driver selection, capability conflicts, fallback —
how the system chooses *which* engine handles a
given scrape and what happens when it can't.

### §6.1 — Current driver picks

1. Which drivers / browser engines do you use today, and
   for what fraction of your traffic — Playwright,
   Selenium / SeleniumBase, curl / curl-impersonate,
   Puppeteer, raw HTTP, headless Chrome via CDP, other?
2. Why those picks specifically — TLS fingerprint
   accuracy, JS execution needs, throughput per worker,
   memory footprint, debugging tooling?

### §6.2 — Selection friction

3. How is driver chosen for a given job today — manual
   per-job config, heuristic (URL pattern → driver),
   automatic routing, something else? What costs you
   the most time in this choice?
4. When a job fails on driver X, what's your fallback
   path — manual rerun on driver Y, automatic
   reattempt, give-up-and-flag, escalate to engineer?

### §6.3 — Capability conflicts

5. Have you hit cases where job needs feature X (e.g.,
   request interception, TLS impersonation, full DOM
   evaluation) but the driver you picked doesn't
   support it? How did you resolve it?
6. What would you want from an *automatic* driver
   selector — purely target-domain-based routing,
   per-job capability declaration, learning from past
   success rates per (target, driver), conservative
   default with explicit overrides?

---

## §7 — Operational layer (Layer 7)

Multi-tenancy, job composition, templates, compliance —
how the platform behaves at the operational boundary
between scrape teams, customers, or regulatory
constraints.

### §7.1 — Multi-tenancy

1. Do you run a single-tenant or multi-tenant deployment?
   If multi-tenant, what tenants share (clusters,
   namespaces, queues, proxy pools, output sinks) and
   what's isolated?
2. Do you need per-tenant quotas, billing-grade usage
   metering, or cost attribution today, and how do you
   implement them if so?

### §7.2 — Job composition + templates

3. Do your jobs have dependencies (job A's output feeds
   job B's input)? How is that orchestrated —
   downstream consumer pull, upstream push, DAG
   scheduler, manual coordination?
4. Do you maintain a job library / templates —
   parametrised reusable job definitions, partial
   templates composed per run, fully ad-hoc per-job
   definitions?

### §7.3 — Compliance + governance

5. What compliance posture do you enforce in production —
   robots.txt respect, ToS-aware target allowlists, GDPR
   data-retention policies, geographic restrictions on
   exit IPs, jurisdiction-aware data residency on
   sinks?
6. Do you maintain audit trails of who ran what job
   when, against what target, with what output —
   per-job lineage records, immutable run logs,
   per-output-row provenance?

---

## §8 — Overall priorities (synthesis)

1. **Force-rank.** Across all seven layers, what are
   your top three friction points today? Be specific
   enough that an engineer reading your answer could
   propose a concrete first step.
2. **One quarter, one service.** If Spectre shipped
   exactly one new infra-service in the next quarter,
   which would it be — proxy-broker, captcha-solver,
   secret-broker, fingerprint-mixer, rate-limit-coordinator,
   audit-log, session-store, output-router, schema-validator,
   enrichment-pipeline, URL-queue-manager, dependency-orchestrator,
   template-registry, compliance-gate, observability-aggregator?
   Why that one first?
3. **Questionnaire gap.** What's missing from this
   questionnaire that you wish we'd asked — a layer
   we under-weighted, a question phrased wrong, a
   trade-off we didn't surface?
4. **Optional — deployment shape.** What does your
   spectre / scraping deployment look like —
   single-tenant or multi-tenant, self-hosted Kubernetes
   / Docker / VM / serverless, hosted SaaS, hybrid?
   This calibrates how to interpret your answers.
5. **Optional — scale signals.** Rough order-of-magnitude
   on jobs per day, URLs per day, output rows per day,
   monthly proxy spend? Helps the maintainer weight
   answers from operators at different scales.
