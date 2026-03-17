# PayFlow

## Service Set (vNext)
- api-gateway
- auth-service
- user-kyc-service
- wallet-account-service
- transaction-orchestrator
- ledger-service
- payment-connector-service
- notification-service
- audit-compliance-service

## Core reliability guarantees
- Idempotency keys for transfer/refund APIs.
- Saga orchestration for transfer lifecycle.
- Ledger-first consistency checks.
- Outbox events for notification + audit.
