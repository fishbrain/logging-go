# AGENTS.md — logging-go

## Overview

Shared logging library for Fishbrain's Go services. Single-package Go module (`package logging`) that wraps [logrus](https://github.com/sirupsen/logrus) with Bugsnag error reporting, Sentry error reporting, Datadog trace correlation, and NSQ log-level bridging.

**Module path**: `github.com/fishbrain/logging-go`

## Commands

| Task  | Command          |
|-------|------------------|
| Build | `go build ./...` |
| Test  | `go test ./...`  |

There is no linter, formatter, or Makefile configured. CI (`go.yml`) runs `go build -v .` only — no test step in CI.

## Project Structure

```
logging.go        # All library code — types, logger init, entry helpers, Bugsnag/Sentry hooks
logging_test.go   # All tests
go.mod / go.sum   # Module definition (Go 1.24+, toolchain 1.26)
.tool-versions    # asdf version pinning (go 1.26.0)
```

This is a **single-file library** — everything lives in `logging.go` and `logging_test.go`. No subdirectories, no `cmd/`, no `internal/`.

## Architecture & Key Types

### Global singleton

`Init(LoggingConfig)` initializes the package-level `Log *Logger` variable. It is guarded by a nil check (not a `sync.Once`), so it only runs once. `TestMain` calls `Init(LoggingConfig{})` to set up the singleton before tests run.

### Type hierarchy

- **`Logger`** — wraps `*logrus.Logger`. Provides `WithField`, `WithError`, `WithDDTrace`, `NewEntry`, and `NSQLogger`.
- **`Entry`** — wraps `*logrus.Entry`. Provides domain-specific field helpers (`WithUser`, `WithEvent`, `WithChannel`, `WithDuration`, etc.) that return `*Entry` for chaining.
- **`NSQLogger`** — adaptor that implements `Output(int, string) error` so it can be passed to `nsq.SetLogger`.
- **`bugsnagHook`** — logrus hook that fires on Error/Fatal/Panic levels, forwarding to Bugsnag with metadata.
- **`sentryHook`** — logrus hook that fires on Error/Fatal/Panic levels, forwarding to Sentry with metadata and extra fields.

### Initialization flow

```
Init(config) →
  1. bugsnag.Configure(...)       — sets up Bugsnag client
  2. bugsnag.OnBeforeNotify(...)  — unwraps *fmt.wrapError to get real error class
  3. sentry.Init(...)             — sets up Sentry client (if SentryDSN is set and environment matches ErrorNotifyReleaseStages)
  4. Log = new(true, withSentry, config) — creates Logger with Bugsnag and optionally Sentry hooks attached
```

## Key Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/sirupsen/logrus` | Structured logging (JSON formatter) |
| `github.com/bugsnag/bugsnag-go/v2` | Error reporting to Bugsnag |
| `github.com/DataDog/dd-trace-go/v2` | Datadog APM trace/span ID injection |
| `github.com/getsentry/sentry-go` | Error reporting to Sentry |
| `github.com/nsqio/go-nsq` | NSQ message queue log-level bridging |
| `github.com/stretchr/testify` | Test assertions |

## Code Patterns & Conventions

### Fluent entry builder

All `With*` methods return `*Entry` to support chaining:

```go
Log.WithDDTrace(ctx).WithUser(userID).WithDuration(d).Info("processed request")
```

When adding new field helpers, follow this pattern: method on `*Entry`, return `*Entry`, delegate to `e.WithField(...)`.

### Error wrapping

Errors passed to `WithError` are wrapped with `bugsnag_errors.New(err, 1)` to capture stack traces. The `1` parameter controls stack frame skipping. The standalone `Errorf` and `ErrorWithStacktrace` functions also use this pattern.

### JSON log output

Logrus is configured with `JSONFormatter` and custom field mapping:
- `msg` → `message`
- `func` → `logger.method_name`
- `file` → `logger.name`
- `error` key → `error.message`
- Timestamp format: `RFC3339Nano`

### Log levels

The `LogLevel` config string must be uppercase: `"ERROR"`, `"WARNING"`, `"INFO"`, `"DEBUG"`. Unknown values default to `InfoLevel`.

### NSQ log bridging

`Logger.NSQLogger()` returns an `(NSQLogger, nsq.LogLevel)` tuple for plugging into `nsq.SetLogger`. The `NSQLogger.Output` method parses the 3-character prefix from NSQ log messages to route them to the correct logrus level.

## Testing

- **Framework**: stdlib `testing` + `testify/assert`
- **Setup**: `TestMain` initializes the global `Log` singleton via `Init(LoggingConfig{})`
- **Log capture**: Tests use `os.Pipe()` to capture log output by swapping `Log.Out`, then assert on the captured string content
- **Concurrency test**: `TestConcurrentUseOfEntry` verifies entries are safe for concurrent use across goroutines
- **Table-driven tests**: `TestGetLogrusLogLevel` uses a table-driven approach with a package-level test data slice
- **Sentry hook tests**: `TestSentryHookFire`, `TestSentryHookLevels`, `TestNewWithSentry`, and `TestNewWithoutSentry` cover the Sentry hook and its integration into the logger
- **Release-stage gating tests**: `TestShouldNotify` verifies the `shouldNotify` helper used for conditional Sentry/Bugsnag activation

## Gotchas

1. **No CI test step**: The GitHub Actions workflow builds but does not run tests. Running `go test ./...` locally is essential before pushing.
2. **Singleton guard is not sync.Once**: `Init` uses `if nil == Log` — safe for single-goroutine init, but not for concurrent callers. In practice this is fine since `Init` is called once at service startup.
3. **`ioutil.ReadAll` in tests**: Tests use the deprecated `io/ioutil` package. New code should use `io.ReadAll` instead.
4. **Bugsnag error unwrapping limit**: The `OnBeforeNotify` handler unwraps `*fmt.wrapError` chains up to 11 levels deep, then logs and stops.
5. **`logrus.ErrorKey` is mutated globally**: `new()` sets `logrus.ErrorKey = "error.message"` as a side effect — this affects all logrus loggers in the process, not just this one.
6. **Reversed nil check style**: The codebase uses Yoda conditions (`nil == Log`) in the `Init` function.
7. **`BugsnagNotifyReleaseStages` renamed**: The config field was renamed to `ErrorNotifyReleaseStages` and is now shared between Bugsnag and Sentry for release-stage gating.
8. **Sentry is conditional**: Sentry is only initialized when `SentryDSN` is non-empty and the current `Environment` is in `ErrorNotifyReleaseStages`. If `sentry.Init` fails, it logs to stderr and proceeds without the Sentry hook.

## Releasing

Create a GitHub Release. The module is imported by other Fishbrain Go services via its module path. Versioning follows Go module semantics (semver tags).

## Ownership

Owned by `@fishbrain/platform-team` (see `CODEOWNERS`). Dependency updates managed by Renovate (see `renovate.json`).
