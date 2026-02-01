# Updated Milestone: 004v2-internal-api-with-authentication.md

## Goal

Update the protected api. Remove SCRAM authentication and instead implement the changes so that the security is enforced
via **Digest Authentication (SHA-256)**, allowing for easy testing with standard tools while keeping the
`DRL_PRIVATE_API_KEY` masked.

## Requirements

### 1. Internal API & Digest Logic

* **Framework**: Use Fiber on port `:8082`.
* **Mechanism**: Use `Digest` authentication.
* **Algorithm**: Must support `SHA-256` (as per RFC 7616).
* **Note**: Since you only have a global API Key, use a static `username` (e.g., `admin`) or allow an empty username
  with the password.


* **Security**: On the server, store the `A1` hash (`SHA256(username:realm:password)`) instead of the raw key to ensure
  the password is never in memory as plain text after startup.

### 2. Implementation with Fiber

Use the official (or a well-regarded) Digest middleware.

**AI Instruction**: Ensure the `WWW-Authenticate` header includes `algorithm=SHA-256` and a unique `nonce`.

### 3. README.md Updates

Document the `curl` command for developers:

```bash
# Testing the Private API with Digest
curl --digest -u ":$DRL_PRIVATE_API_KEY" http://localhost:8082/status

```

## Validation Criteria

1. **Simple One-Liner**: `curl --digest -u :Test5ecretPrivateAPIKey ...` returns the cluster status.
2. **Replay Protection**: Ensure the middleware uses a `nonce` that changes or has a limited lifetime.
3. **Security**: Verify via `tcpdump` or Wireshark that the password `Test5ecretPrivateAPIKey` does not appear in the
   HTTP stream.

