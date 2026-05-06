# 017-private-api-versioned-openapi-doc.md

## Goal

Modernize the Private API (Port `8082`) by introducing versioning (`v1`), updating the Svelte UI to support this new
structure, and exposing dynamically generated OpenAPI documentation. This ensures the API is self-documenting and
follows RESTful best practices.

## Requirements

### 1. API Versioning & Routing

* **Namespace Transition**: Move all existing private endpoints under the `/v1` prefix.
    * `GET /status` $\to$ `GET /v1/status`
    * `GET /accounting/stats` $\to$ `GET /v1/accounting/stats`
    * `GET /blocked-entity` $\to$ `GET /v1/blocked-entity`
    * `POST /accounting/load` $\to$ `GET /v1/accounting/load`
    * `GET /configuration/static/:section` $\to$ `GET /v1/configuration/static/:section`
    * `GET /drl/ui/get-token` $\to$ `GET /v1/drl/ui/get-token`
* **UI Update**: Update the Svelte frontend (`ui/src/lib/auth.ts` and API stores) to prepend `/v1` to all fetch
  requests.
* **Backward Compatibility**: the project is still in alpha so discard backward-compatibility breaking changes.

### 2. OpenAPI Documentation (`/v1/apidocs` & `/v1/swagger.json`)

* **Dynamic Generation**: Use a library like `gofiber/swagger` or `swaggo/swag` to generate the OpenAPI 3.0
  specification from Go struct tags and comments.
* **Exempt Endpoints**: The following endpoints **must not** be protected by Digest Authentication or ECDH middleware:
    * `GET /v1/swagger.json`: Returns the raw JSON specification.
    * `GET /v1/apidocs`: Serves the Swagger UI (dist) to visualize and interact with the API.
* **Authentication Documentation**: The OpenAPI spec must explicitly document:
    * **Digest Auth (SHA-256)** for CLI/Management access.
    * **Bearer Token (ECDH Session)** for UI-based encrypted communication.

### 3. Response & Request Modeling

* **Typed Schemas**: Refactor handlers to use explicit Go structs for all requests and responses (instead of
  `fiber.Map`). Create a `models` package inside `api` for shared structs.
* **Validation Tags**: Use `validate` tags to ensure incoming data (like `POST /v1/blocked-entity`) is checked before
  processing.
* **Standardized Errors**: Implement a global error response model:
  ```json
  {
    "error": "Short description",
    "code": 400,
    "details": "Technical details for debugging"
  }
  ```

### 4. Convergence with `internal-api.md`

* **Handshake Examples**: Integrate the authentication handshake logic (Digest and ECDH) directly into the OpenAPI "
  Description" fields or the Swagger UI "Introduction" section.
* **Internal API Guide**: Update `docs/content/docs/internal-api.md` to reflect the `/v1` paths and the availability of
  the interactive Swagger docs.

## Implementation Guidelines

* **Fiber Grouping**: Use `app.Group("/v1")` in `internal/api/api.go` to cleanly mount versioned routes.
* **Swagger Annotations**: Add standard declarative comments to every handler function (e.g., `@Summary`, `@Router`,
  `@Success`).
