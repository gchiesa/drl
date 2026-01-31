# 004-internal-api-with-authentication.md

## Goal

Implement a protected internal API using `gofiber/fiber/v2` to expose DRL's internal state. Security is enforced via a *
*SCRAM-SHA-256** handshake, with the shared secret sourced exclusively from the `DRL_PRIVATE_API_KEY` environment
variable.

## Requirements

### 1. Internal API Setup

* **Framework:** Initialize a Fiber app on a dedicated internal port (default `:8082`).
* **Endpoint:** `GET /status`
* **Response:** JSON containing `cluster_name`, `node_id`, `active_peers` (list), and `uptime`.

### 2. SCRAM Authentication Logic

* **Library:** Use `github.com/xdg/scram` to handle the complexity of the RFC 5802/7677 logic.
* **Mechanism:** Support `SCRAM-SHA-256`.
* **Credential Storage:** * DRL must **not** store the raw `DRL_PRIVATE_API_KEY`.
* On startup, compute the `StoredKey` and `ServerKey` from the environment variable and store only those in memory for
  verification.


* **Handshake Flow:** Since HTTP is stateless, the SCRAM handshake (Client First -> Server First -> Client Final ->
  Server Final) should be implemented over a multi-step exchange or a specialized header-based flow.
* *Recommendation:* Implement a simplified "one-shot" verification or a 2-step handshake using the
  `Authorization: SCRAM-SHA-256 ...` header.

### 3. Environment Variable Enforcement

* The API must fail to start if `DRL_PRIVATE_API_KEY` is not set or is shorter than 16 characters (security best
  practice).

### 4. Documentation (README.md)

Create a root-level `README.md` including:

* **Usage Examples:**

```bash
# Example of the multi-step SCRAM authentication using curl
curl -X GET http://localhost:8082/status \
     -H "Authorization: SCRAM-SHA-256 n,,n=admin,r=fyko+d2lbbFgONRv9qkxdawL"

```

* **Security Note:** Explicitly state that the internal API should be bound to `localhost` in production or protected by
  mTLS/VPN.

## Implementation Guidelines

* **Middleware:** Write a custom Fiber middleware in `internal/api/auth.go` that intercepts requests to `/status`.
* **Error Handling:** Return `401 Unauthorized` for failed handshakes, but ensure error messages do not leak if the user
  exists or if the salt was incorrect.

## Validation Criteria

1. **Auth Failure:** `curl -I http://localhost:8082/status` returns `401 Unauthorized`.
2. **Auth Success:** A valid SCRAM exchange returns a `200 OK` with the cluster JSON.
3. **No Env Var:** The application logs a fatal error and exits if `DRL_PRIVATE_API_KEY` is missing.

