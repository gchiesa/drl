# 016v3-separate-authentication-get-token.md

## Goal

Modify the UI authentication flow to remove the embedded bootstrap token from the initial HTML delivery. Instead, the UI
must prompt the user for a token via a modal. This token must be retrieved out-of-band using an authenticated request to
a new backend endpoint.

## Requirements

### 1. Backend: Token Issuance & Security

* **New Endpoint**: Implement `GET /drl/ui/get-token`.
* **Authentication**: Protect this endpoint using **Digest Authentication** (consistent with the existing administrative
  access controls).
* **Payload**: Return a JSON object containing the `bootstrap_token`.
* **UI Delivery**: Update `ui_handlers.go` to stop injecting the token into the `<meta name="drl-bootstrap">` tag. The
  tag should now only contain non-sensitive metadata (e.g., node version, cluster name).

### 2. Frontend: Modal & Handshake Logic

* **Blocking Modal**: If no session is active and no token is in memory, display a non-dismissible modal requesting
  the "Access Token".
* **Session Transition**: Once the user provides the token, the UI should proceed with the existing Diffie-Hellman (
  ECDH) handshake logic to establish the encrypted session.
* **Persistence**: Ensure the token is stored only in session memory (not `localStorage`) to require re-entry if the tab
  is closed, aligned with the "High Security" profile of the DRL.

### 3. Documentation

* Update `docs/content/docs/ui.md` to explain the new access flow.
* Include a `curl` example demonstrating how to retrieve the token:
    ```bash
    curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://localhost:8082/drl/ui/get-token
    ```

## Validation Criteria

1. **Unauthorized Access**: Navigating to `http://localhost:8082/drl/ui/` loads the Svelte shell but shows a login/token
   modal immediately.

### Implementation Notes for the AI

* **File to Modify**: `internal/ui/ui_handlers.go` for the Go backend.
* **File to Modify**: `ui/src/lib/auth.js` (or equivalent) and `App.svelte` for the login modal logic.
* **Consistency**: Ensure the `dualAuthMiddleware` still allows the UI to proxy requests once the session is established
  via the manual token.

