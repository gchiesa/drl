DRL Distributed Rate Limiter — Private Management API (port 8082).
Provides cluster status, blocklist management, accounting statistics, and configuration access.

## Authentication

All management endpoints (except OpenAPI docs) require authentication via one of:

- **HTTP Digest Authentication (SHA-256)** — for CLI / curl access
- **Bearer Token (ECDH session)** — for browser SPA encrypted communication

## Digest Auth (CLI / Management access)

The SHA-256 Digest challenge-response flow never transmits the password on the wire:

```
Client → GET /v1/status
Server → 401 WWW-Authenticate: Digest realm="DRL Internal API", nonce="...", algorithm=SHA-256
Client computes:
  A1 = SHA256(username:realm:password)
  A2 = SHA256(method:uri)
  response = SHA256(A1:nonce:nc:cnonce:qop:A2)
Client → GET /v1/status Authorization: Digest username="...", response="..."
Server → 200 OK
```

Example (curl):

```bash
curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/status
```

## Bearer Token (ECDH Session — browser SPA)

The browser SPA performs an ECDH P-256 key exchange to establish an encrypted session:

1. `GET /v1/ui/get-token` (Digest auth) → `{"bootstrap_token": "..."}`
2. `POST /v1/ui/exchange` `{ clientPublicKey, bootstrapToken }` → encrypted session token
3. All subsequent requests carry `Authorization: DRL-Session <session_token>`.
   Responses are AES-256-GCM encrypted using the ECDH-derived shared key.
