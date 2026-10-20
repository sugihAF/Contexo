---
schema: ctx.page.v1
slug: retry-policy
type: concept
author: alice
agent: claude-code
created: 2026-10-21T10:04:00Z
updated: 2026-10-30T14:20:00Z
parent_sha: ""
sources:
  - 2026-10-21-psp-timeouts
related:
  - idempotency-keys
tags:
  - payments
  - retries
reasoning_summary: Bounded, jittered retries keep PSP outages from turning into duplicate charges.
---
# Retry policy

Outbound calls to payment service providers (PSPs) are retried with
exponential backoff and full jitter.

## Limits

- Maximum attempts: 8
- Initial delay: 500 ms
- Backoff ceiling: 10 minutes
- Retry only on network errors, HTTP 429 and HTTP 5xx

## Idempotency

Every retry reuses the original idempotency key, so a PSP that already
processed the first attempt returns the stored result instead of charging
twice. See [[idempotency-keys]].
