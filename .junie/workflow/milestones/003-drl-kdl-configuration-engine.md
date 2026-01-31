# 003-drl-kdl-configuration-engine

## Goal

Replace hardcoded defaults with a robust configuration engine that supports KDL files, with the ability to override any
setting via environment variables (following the 12-Factor App methodology).

## Requirements

### 1. KDL Schema Definition

Define a KDL structure that mirrors the current requirements. Example `config.kdl`:

```kdl
listen {
    grpc ":8081"
    metrics ":9091"
}

membership {
    service-name "drl"
    port 7946
    // Optional: add join-timeout "30s"
}

logging {
    level "info"
    format "json"
}

```

### 2. Configuration Package (`internal/config`)

* **Parser:** Use `github.com/sblinch/kdl-go` to unmarshal the file into a Go struct.
* **Environment Overrides:** Implement a "Merge" strategy.
* **Precedence:** Environment Variables > KDL File > Internal Defaults.
* *Recommendation:* Use a library like `spf13/viper` or a simple reflection-based mapper to map environment variables (
  e.g., `DRL_MEMBERSHIP_SERVICE_NAME`) to the config struct.

### 3. CLI Integration

* Implement the `--config` (or `-c`) flag using `spf13/cobra` or the standard `flag` package.
* If the flag is provided but the file is missing, the application should exit with a clear error message.

### 4. Docker & CI/CD Update

* Update `docker-compose.yaml` to mount a `config.kdl` file into the containers. Move the current environment config to
  a default `config.kdl`
* Update `MISE` tasks to ensure tests pass even when no config file is present (falling back to defaults).

## Implementation Guidelines

* **Validation:** Add a `Validate()` method to your configuration struct. The app should fail fast if, for example, the
  `service-name` is empty or the `port` is out of range.
* **Logging:** On startup, the DRL node should log *where* it loaded its configuration from (e.g.,
  `"Loaded config from /etc/drl/config.kdl"`).

## Validation Criteria

1. **CLI Test:** `drl --config non-existent.kdl` returns an error.
2. **Override Test:** Set `DRL_LOGGING_LEVEL=debug` in the environment while the KDL file says `info`. Verify the app
   logs at `debug` level.
3. **KDL Syntax:** Verify the parser correctly handles KDL v1 features like nodes, properties, and comments.

---
