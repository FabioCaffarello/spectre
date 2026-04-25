# Responsible use

Spectre is a tool for automated web access. Like every tool of its
kind, it can be used for legitimate engineering work or misused.
This document sets the project's expectations for responsible use and
documents the defaults Spectre ships with.

## What Spectre is for

The project exists to support legitimate web data acquisition and
browser automation work, including:

- **QA and integration testing** of web applications.
- **Accessibility audits** at scale.
- **Compliance and content monitoring** — verifying that published
  content matches policy or contractual obligations.
- **Public-data research** — academic, journalistic, and civic
  research using openly available data.
- **Internal automation** — replacing manual workflows on systems the
  user already has authorisation to access.
- **Search-engine and dataset construction** for openly licensed data.
- **Performance and uptime monitoring** of one's own services.

These uses share two properties: the operator has a legitimate
relationship to the target, and the work is done within applicable
law, terms of service, and platform policy.

## What Spectre is not for

Spectre is not intended to facilitate:

- Account takeover, credential stuffing, or unauthorised access to
  systems.
- Fraud, including ad-click fraud, fake-review generation, or
  fabricated engagement.
- Content theft from sources that prohibit it, or large-scale
  copyright infringement.
- Violation of computer-misuse statutes (e.g. CFAA in the US,
  equivalent laws elsewhere).
- Targeting individuals for harassment, doxxing, or surveillance
  outside lawful, accountable processes.
- Circumvention of access controls on systems the operator has no
  authorisation to use.

These uses are out of scope for community support, will not be
prioritised in the roadmap, and will not be accommodated by feature
requests.

## Defaults Spectre ships with

The project takes a "responsible by default" stance. Several behaviours
are defaults rather than opt-ins:

- **`robots.txt` awareness.** The engine reads `robots.txt` for the
  target domain at session start. By default, requests to disallowed
  paths emit a warning. An explicit `robots: ignore` directive in the
  job document is required to bypass — and the directive is logged.
- **Rate limiting.** Default per-domain rate is conservative
  (configurable, but never zero). Bursts are smoothed by an exponential
  backoff on consecutive errors.
- **Identification.** The default `User-Agent` includes a Spectre
  identifier and a project URL. Operators may override, but the
  default behaviour is to be identifiable.
- **Failure-mode caution.** When a target returns a `403`, `429`, or
  CAPTCHA challenge, the engine does not silently retry indefinitely.
  It escalates after a small budget and surfaces a structured error.
- **Data minimisation.** Extraction jobs declare exactly which fields
  to capture. Spectre does not collect bodies, headers, or metadata
  that the job did not request.

Adopters can override defaults for legitimate reasons (a research
project with explicit permission from the target, a QA suite running
against one's own staging environment). The defaults are designed so
that the path of least resistance is the responsible one.

## PII handling

If your job extracts personally identifiable information, the
operator is the data controller for the purposes of GDPR, CCPA, and
similar regimes. Spectre's responsibilities and yours:

- **Spectre does not transmit job outputs anywhere.** Outputs go to
  the storage the job specifies (local files, S3, a database). No
  telemetry of extracted content leaves the operator's environment.
- **You must establish a lawful basis** for processing extracted
  PII before running the job.
- **You must apply retention and minimisation** to the outputs.
  Spectre does not retain extracted data past the job lifecycle.

If you are working with PII at scale, consult counsel.

## Authorised security testing

Spectre is suitable for authorised security testing — penetration
tests with written scope, bug bounty work within program rules, red
team exercises with internal sign-off. The same defaults apply:
identify yourself when policy requires it, respect the agreed scope,
do not exfiltrate data outside the scope.

If you are unsure whether your work is authorised, it is not.

## Reporting misuse

If you observe Spectre being used to harm individuals, violate
computer-misuse laws, or otherwise cross the lines in this document,
please open a security advisory via [SECURITY.md](../../SECURITY.md).
The maintainers cannot police downstream use, but documented misuse
informs defaults, hardening priorities, and policy.

## Why this matters

Public, sustained, well-engineered tooling for browser automation
benefits a long list of legitimate users. The community's ability to
keep building such tools depends on operators using them within the
law and the spirit of the open web. This document is the project's
small contribution to that.
