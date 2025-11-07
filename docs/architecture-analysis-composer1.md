# Architectural Analysis Report: lileo-pubsub

## Executive Summary

This library provides a pub/sub abstraction layer for Go applications, supporting multiple backend providers (Google Cloud Pub/Sub, NATS, RabbitMQ, Kafka, Memory) with middleware support for observability and cross-cutting concerns. While the library demonstrates good use of design patterns for extensibility, it suffers from several architectural issues that impact maintainability, testability, and production readiness.

---

## 1. Architectural Patterns Used

### 1.1 Strategy Pattern ✅
**Implementation:** The `Provider` interface abstracts different pub/sub implementations.

```go
type Provider interface {
    Publish(ctx context.Context, topic string, m *Msg) error
    Subscribe(opts HandlerOptions, handler MsgHandler)
    Shutdown()
}
```

**Assessment:** Well-implemented. Makes it easy to swap providers without changing application code.

### 1.2 Middleware Pattern (Chain of Responsibility) ✅
**Implementation:** Middleware interface allows intercepting both publish and subscribe operations.

```go
type Middleware interface {
    SubscribeInterceptor(opts HandlerOptions, next MsgHandler) MsgHandler
    PublisherMsgInterceptor(serviceName string, next PublishHandler) PublishHandler
}
```

**Assessment:** Excellent pattern implementation. Allows cross-cutting concerns (logging, tracing, metrics, recovery) to be composed flexibly.

### 1.3 Adapter Pattern ✅
**Implementation:** Each provider (Google, NATS, RabbitMQ, etc.) adapts its native API to the common `Provider` interface.

**Assessment:** Good separation of concerns. Each provider encapsulates its specific implementation details.

### 1.4 Reflection-Based Handler Invocation ⚠️
**Implementation:** Uses `reflect` package to dynamically invoke user-defined handlers with type-safe unmarshaling.

**Assessment:** 
- **Pros:** Provides type safety at compile time for handler signatures
- **Cons:** Runtime overhead, less idiomatic Go, harder to debug

### 1.5 Global State Pattern ❌
**Implementation:** Global `clients` slice and `publishWaitGroup` for managing publishers.

```go
var (
    clients          = []*Client{&Client{Provider: NoopProvider{}}}
    publishWaitGroup sync.WaitGroup
)
```

**Assessment:** **Major architectural flaw.** Creates several problems:
- Race conditions in concurrent scenarios
- Difficult to test (requires global state manipulation)
- Cannot have multiple isolated instances
- Thread-safety concerns

---

## 2. Critical Issues

### 2.1 Race Conditions in Global State ❌

**Location:** `publish.go:56-62`, `publish.go:82-92`

**Problem:** The `clients` slice is accessed without proper synchronization. Multiple goroutines can read/write concurrently.

```go
// In Publish() and PublishJSON()
for _, c := range clients {  // No mutex protection
    // ...
}
```

**Impact:** Data races, potential panics, undefined behavior.

**Severity:** HIGH

### 2.2 Error Handling Race Condition in PublishJSON ❌

**Location:** `publish.go:78-97`

**Problem:** Multiple goroutines write to `pr.Err` without synchronization.

```go
go func(c *Client) {
    // ...
    if err != nil {
        pr.Err = err  // Race condition!
    }
}(c)
```

**Impact:** Lost errors, incorrect error reporting.

**Severity:** HIGH

### 2.3 Panic-Based Validation ❌

**Location:** `subscribe.go:60-122`

**Problem:** Uses `panic()` for validation errors instead of returning errors.

```go
if opts.Topic == "" {
    panic("lile pubsub: topic must be set")
}
```

**Impact:** 
- Not idiomatic Go (should return errors)
- Cannot be handled gracefully
- Crashes entire application
- Harder to test

**Severity:** MEDIUM-HIGH

### 2.4 Context Propagation Issues ⚠️

**Location:** Multiple providers

**Problem:** Some providers create new contexts instead of using provided ones, losing cancellation and timeout propagation.

**Examples:**
- `providers/google/google.go:88` - Uses `context.Background()` instead of passed `ctx`
- `providers/nats/nats.go:120` - Uses `context.Background()` instead of passed context
- `providers/rabbitmq/rabbitmq.go:285` - Uses `context.Background()`

**Impact:** Cannot cancel operations, timeouts don't work properly.

**Severity:** MEDIUM

### 2.5 Memory Provider Implementation Issues ❌

**Location:** `providers/memory/memory.go`

**Problems:**
1. **Synchronous processing:** `Subscribe()` processes all messages synchronously in a single call, not asynchronously like real providers.
2. **No message delivery guarantee:** Messages are processed immediately on subscribe, not as they arrive.
3. **Race condition:** `mutex.RLock()` is held during message processing, blocking all publishes.

**Impact:** Memory provider doesn't accurately simulate real pub/sub behavior, making tests unreliable.

**Severity:** MEDIUM

### 2.6 Reflection Performance Overhead ⚠️

**Location:** `subscribe.go:86-143`

**Problem:** Heavy use of reflection for handler invocation:
- Type checking at runtime
- Dynamic value creation
- Function call via `reflect.Value.Call()`

**Impact:** Performance overhead, especially for high-throughput scenarios.

**Severity:** LOW-MEDIUM (acceptable trade-off for type safety)

### 2.7 Inconsistent Error Handling ⚠️

**Problem:** Different providers handle errors differently:
- Some log and continue
- Some return errors
- Some panic (Google provider has `logrus.Panicf` calls)

**Impact:** Unpredictable behavior, harder to debug.

**Severity:** MEDIUM

### 2.8 Hardcoded Dependencies ⚠️

**Location:** `subscribe.go:11`, `pubsub.go:24`

**Problem:** Direct dependency on `logrus` in core package.

```go
import "github.com/sirupsen/logrus"
```

**Impact:** Forces all users to depend on logrus, even if they use a different logger.

**Severity:** LOW-MEDIUM

### 2.9 No Dependency Injection ❌

**Problem:** Global functions (`Publish`, `PublishJSON`, `Subscribe`) make it impossible to inject dependencies for testing or multiple instances.

**Impact:** 
- Hard to test
- Cannot have multiple pub/sub instances in one application
- Tight coupling

**Severity:** HIGH

### 2.10 Missing Error Returns in Provider Interface ⚠️

**Location:** `pubsub.go:41`

**Problem:** `Subscribe()` doesn't return an error, making it impossible to know if subscription failed.

```go
Subscribe(opts HandlerOptions, handler MsgHandler)  // No error return
```

**Impact:** Silent failures, hard to debug subscription issues.

**Severity:** MEDIUM

---

## 3. Code Quality Issues

### 3.1 Debug Logging Left in Production Code ❌

**Location:** `providers/google/google.go:131-144`

```go
log.Printf("topic " + opts.Topic)
log.Printf("subName " + subName)
log.Printf("sub is nil")
log.Printf("sub is not nil")
log.Printf("exists? %v", exists)
```

**Impact:** Clutters logs, unprofessional.

**Severity:** LOW

### 3.2 Inconsistent Logging ⚠️

**Problem:** Mix of `logrus`, `logr`, and standard `log` package across codebase.

**Impact:** Inconsistent log formats, harder to configure.

**Severity:** LOW

### 3.3 Dead Code / Unused Variables ⚠️

**Location:** `providers/nats/nats.go:61-62`

```go
if err != nil {
    logr.WithCtx(ctx).Error(errors.Wrap(err, "couldn't publish to nats"))
} else {
    // ...
}
```

**Problem:** Error check happens after the error is already returned.

**Severity:** LOW

### 3.4 Global Mutex in Google Provider ⚠️

**Location:** `providers/google/google.go:29`, `providers/google2/google2.go:23`

```go
var mutex = &sync.Mutex{}
```

**Problem:** Package-level mutex shared across all instances. If multiple `GoogleCloud` instances exist, they'll contend unnecessarily.

**Severity:** MEDIUM

---

## 4. Extensibility Analysis

### 4.1 Provider Extensibility ✅ **EXCELLENT**

**Strengths:**
- Simple, well-defined interface
- Easy to implement new providers
- Good examples (Memory, NATS, RabbitMQ, Google)
- Provider-specific options can be passed during construction

**Example:** Adding a new provider requires implementing 3 methods:
```go
type MyProvider struct {}

func (p *MyProvider) Publish(ctx context.Context, topic string, m *Msg) error { ... }
func (p *MyProvider) Subscribe(opts HandlerOptions, h MsgHandler) { ... }
func (p *MyProvider) Shutdown() { ... }
```

**Rating:** 9/10

### 4.2 Middleware Extensibility ✅ **EXCELLENT**

**Strengths:**
- Clean interface
- Composable (can chain multiple middleware)
- Separate interceptors for publish/subscribe
- Good examples (Prometheus, Logrus, OpenTracing, Recovery)

**Example:** Adding custom middleware:
```go
type MyMiddleware struct {}

func (m MyMiddleware) SubscribeInterceptor(opts HandlerOptions, next MsgHandler) MsgHandler {
    return func(ctx context.Context, m Msg) error {
        // Pre-processing
        err := next(ctx, m)
        // Post-processing
        return err
    }
}
```

**Rating:** 9/10

### 4.3 Handler Extensibility ⚠️ **MODERATE**

**Strengths:**
- Type-safe handlers via reflection
- Supports both Protobuf and JSON
- Flexible handler signatures

**Weaknesses:**
- Reflection overhead
- Limited to specific signature: `func(ctx, obj, msg) error`
- Cannot easily add custom handler types
- Validation happens at runtime (panics)

**Rating:** 6/10

### 4.4 Client Extensibility ❌ **POOR**

**Weaknesses:**
- Global state prevents multiple instances
- No dependency injection
- Hard to extend Client behavior
- Tight coupling to global functions

**Rating:** 3/10

---

## 5. Recommendations for Improvement

### 5.1 High Priority Fixes

#### 5.1.1 Remove Global State
**Action:** Refactor to instance-based API

```go
// Instead of:
pubsub.Publish(ctx, topic, msg)

// Use:
client := pubsub.NewClient(provider, middleware...)
client.Publish(ctx, topic, msg)
```

**Benefits:**
- Eliminates race conditions
- Enables multiple instances
- Makes testing easier
- Better for dependency injection

#### 5.1.2 Fix Race Conditions
**Action:** Add proper synchronization

```go
type ClientManager struct {
    mu      sync.RWMutex
    clients []*Client
}

func (cm *ClientManager) AddClient(c *Client) {
    cm.mu.Lock()
    defer cm.mu.Unlock()
    cm.clients = append(cm.clients, c)
}
```

#### 5.1.3 Replace Panics with Errors
**Action:** Return errors instead of panicking

```go
func (c *Client) On(opts HandlerOptions) error {
    if opts.Topic == "" {
        return errors.New("topic must be set")
    }
    // ...
}
```

#### 5.1.4 Fix PublishJSON Error Handling
**Action:** Use proper synchronization or channels

```go
type PublishResult struct {
    mu    sync.Mutex
    errs  []error
    Ready chan struct{}
}

func (p *PublishResult) addError(err error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.errs = append(p.errs, err)
}
```

### 5.2 Medium Priority Improvements

#### 5.2.1 Add Context Propagation
**Action:** Ensure contexts are properly propagated through all provider calls

#### 5.2.2 Improve Provider Interface
**Action:** Add error return to Subscribe

```go
type Provider interface {
    Publish(ctx context.Context, topic string, m *Msg) error
    Subscribe(opts HandlerOptions, handler MsgHandler) error  // Add error return
    Shutdown()
}
```

#### 5.2.3 Fix Memory Provider
**Action:** Make it behave like real providers (async, goroutine-based)

#### 5.2.4 Remove Hardcoded Dependencies
**Action:** Use interface-based logging

```go
type Logger interface {
    Debug(msg string)
    Error(msg string, err error)
}
```

### 5.3 Low Priority Improvements

#### 5.3.1 Remove Debug Logging
**Action:** Clean up debug `log.Printf` statements

#### 5.3.2 Standardize Logging
**Action:** Choose one logging library or use interfaces

#### 5.3.3 Improve Documentation
**Action:** Add godoc examples, architecture diagrams

#### 5.3.4 Add Metrics Interface
**Action:** Abstract Prometheus behind an interface for flexibility

---

## 6. Testing Considerations

### Current State
- Basic tests exist (`subscribe_test.go`)
- Uses Memory provider for testing
- Memory provider doesn't accurately simulate real behavior

### Issues
1. Global state makes it hard to test in parallel
2. Panics make it hard to test error cases
3. Memory provider doesn't test async behavior

### Recommendations
1. Refactor to instance-based API (enables parallel tests)
2. Replace panics with errors (enables error testing)
3. Improve Memory provider or use mocks
4. Add integration tests for each provider

---

## 7. Overall Assessment

### Strengths ✅
1. **Excellent extensibility** for providers and middleware
2. **Clean separation** of concerns
3. **Good pattern usage** (Strategy, Middleware)
4. **Type-safe handlers** via reflection
5. **Multiple provider support**

### Weaknesses ❌
1. **Global state** creates race conditions and testing issues
2. **Panic-based validation** is not idiomatic Go
3. **Race conditions** in error handling
4. **Context propagation** issues
5. **No dependency injection** support

### Extensibility Score: 7/10

**Breakdown:**
- Provider extensibility: 9/10
- Middleware extensibility: 9/10
- Handler extensibility: 6/10
- Client extensibility: 3/10
- Overall architecture: 6/10

### Production Readiness: 6/10

**Issues preventing production use:**
- Race conditions need fixing
- Global state needs refactoring
- Error handling needs improvement

---

## 8. Conclusion

The library demonstrates good understanding of design patterns and provides excellent extensibility for providers and middleware. However, critical issues with global state, race conditions, and error handling prevent it from being production-ready without significant refactoring.

**Priority Actions:**
1. Remove global state → instance-based API
2. Fix race conditions → proper synchronization
3. Replace panics → return errors
4. Fix context propagation → use provided contexts

With these fixes, the library would be a solid, extensible pub/sub abstraction layer suitable for production use.

