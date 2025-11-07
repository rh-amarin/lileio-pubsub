# Analyze library architecture and improvements
_Exported on 11/7/2025 at 07:33:21 GMT+1 from Cursor (2.0.64)_

---

**User**

analyze this library and create a report about its architectural patterns used, what things are wrong, or could be improved. how extensible is it?

---

**Cursor**

I'll start by mapping the core abstractions and APIs to understand the architecture. Then I'll review provider and middleware structures in subsequent steps.

I’ve mapped the core API and client/middleware flow. Next I’ll review several provider implementations to understand patterns and lifecycle handling.

Now I'll scan the middleware implementations to understand the interceptor chain and semantics.

I’ve completed the API, providers, middleware, lifecycle, and docs/tests review. I’ll compile the key findings and prioritized recommendations now.

### High-level overview
- **Purpose**: Small facade over multiple Pub/Sub backends with a consistent consumer handler signature and pluggable middleware for publish and subscribe paths.
- **Core abstractions**:
  - `Provider` interface (adapter pattern): Publish/Subscribe/Shutdown.

```38:43:/Users/amarin/work/workspaces/experiments/lileo-pubsub/pubsub.go
type Provider interface {
	Publish(ctx context.Context, topic string, m *Msg) error
	Subscribe(opts HandlerOptions, handler MsgHandler)
	Shutdown()
}
```

  - `Client` + global registry: a default client used for subscribers and a list of publisher clients for fan-out.

```11:15:/Users/amarin/work/workspaces/experiments/lileo-pubsub/pubsub.go
var (
	clients          = []*Client{&Client{Provider: NoopProvider{}}}
	publishWaitGroup sync.WaitGroup
)
```

```23:31:/Users/amarin/work/workspaces/experiments/lileo-pubsub/pubsub.go
// SetClient sets the global pubsub client, useful in tests
func SetClient(cli *Client) {
	clients = []*Client{cli}
}

// GetClient get the global pubsub client, useful in tests
func GetClient() *Client {
	return clients[0]
}
```

  - **Middleware chain** (decorator/interceptor) for both subscribe and publish paths.

```105:114:/Users/amarin/work/workspaces/experiments/lileo-pubsub/publish.go
func chainPublisherMiddleware(mw ...Middleware) func(serviceName string, next PublishHandler) PublishHandler {
	return func(serviceName string, final PublishHandler) PublishHandler {
		return func(ctx context.Context, topic string, m *Msg) error {
			last := final
			for i := len(mw) - 1; i >= 0; i-- {
				last = mw[i].PublisherMsgInterceptor(serviceName, last)
			}
			return last(ctx, topic, m)
		}
	}
}
```

- **Handler binding**: Subscriber handler signature enforced via reflection at runtime.

```86:116:/Users/amarin/work/workspaces/experiments/lileo-pubsub/subscribe.go
// Reflection is slow, but this is done only once on subscriber setup
hndlr := reflect.TypeOf(opts.Handler)
if hndlr.Kind() != reflect.Func {
	panic("lile pubsub: handler needs to be a func")
}

if hndlr.NumIn() != 3 {
	panic(`lile pubsub: handler should be of format
		func(ctx context.Context, obj *proto.Message, msg *Msg) error
		but didn't receive enough args`)
}

if hndlr.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() {
	panic(`lile pubsub: handler should be of format
		func(ctx context.Context, obj *proto.Message, msg *Msg) error
		but first arg was not context.Context`)
}
```

### Architectural patterns used
- **Adapter/Provider pattern**: Each backend implements `Provider`, allowing substitution (Google Pub/Sub v2, NATS Streaming, RabbitMQ, Kafka experimental, memory).
- **Middleware pipeline**: Symmetric interceptors for publishers and subscribers (logging, tracing, metrics, recovery, audit). Good separation of cross-cutting concerns.
- **Facade + global registry**: Simple top-level `Publish`, `PublishJSON`, `Subscribe`, and `Shutdown` wrappers with a global default client and additional publishing-only clients for fan-out.
- **Message abstraction**: `Msg` provides common fields and closures for `Ack`/`Nack`.
- **Concurrency managed in providers**: e.g., Google v2 uses a semaphore and `ReceiveSettings` to bound concurrent handling.

### Extensibility assessment
- **Providers**: High. Small interface; implementations vary backend-specific details internally (topic/queue creation, ack mode, concurrency). Adding a provider is straightforward.
- **Middleware**: High. Minimal interface with two hooks; easy to add new cross-cutting concerns.
- **Subscriber handlers**: Medium. Reflection-based runtime checks; no compile-time guarantees. Adding overloads or variant signatures is awkward.
- **Configuration**: Medium. `HandlerOptions` covers core knobs, but provider-specific tuning leaks into providers (e.g., RabbitMQ exchange/queue semantics). No provider-specific options channelled through the core API.
- **Composition**: Global client model makes multi-tenant or per-module topologies clunky. It’s flexible for simple apps but limits DI-friendly designs.

### Notable gaps and smells
- **Global mutable state and fan-out design**
  - Global `clients` slice with implicit index zero for subscribers; appends for publishers; no synchronization. Hard to test in parallel and unsafe if mutated after start.
  - Global `wait` channel for shutdown signaling couples lifecycle to package-level state.

- **Error handling and panics in library code**
  - Provider implementations panic on setup failures instead of returning errors (e.g., google v2 ensure topic/subscription paths):

```116:121:/Users/amarin/work/workspaces/experiments/lileo-pubsub/providers/google2/google2.go
// Check if subscription exists and create if needed
err = g.ensureSubscription(ctx, subscriptionName, topicName, opts.Deadline)
if err != nil {
	logrus.Panicf("Can't ensure subscription %s: %s", subName, err.Error())
}
```

- **Data race and inconsistent publish result semantics**
  - `PublishJSON` concurrently writes to `pr.Err` without synchronization; only the last write survives, potential race:

```79:92:/Users/amarin/work/workspaces/experiments/lileo-pubsub/publish.go
go func(c *Client) {
	defer publishWaitGroup.Done()
	defer wg.Done()
	err := c.Publish(ctx, topic, obj, true)
	if err != nil {
		pr.Err = err
	}
}(c)
```

  - `Publish` uses `errgroup.Group` and aggregates errors properly; `PublishJSON` uses a `WaitGroup` and loses error aggregation parity.

- **Inconsistent ack semantics across providers**
  - RabbitMQ uses `auto-ack = opts.AutoAck` at consume time, which bypasses manual `Ack/Nack` paths when true; yet code still calls `msg.Ack()` later redundantly:

```198:206:/Users/amarin/work/workspaces/experiments/lileo-pubsub/providers/rabbitmq/rabbitmq.go
msgs, err := ch.Consume(
	queue.Name, // queue
	"",         // consumer tag
	opts.AutoAck, // auto-ack
	false,        // exclusive
	false,        // no-local
	false,        // no-wait
	nil,          // arguments
)
```

- **Context handling is inconsistent**
  - Several providers use `context.Background()` internally for long-running ops or publish `Get()` calls, ignoring the caller’s context (e.g., Google Cloud Run publish intentionally ignores context; multiple subscribe paths do the same). This undermines cancellation/timeout behavior.

- **Runtime reflection for handler typing**
  - All signature validation and instantiation via reflection; misuse leads to panics. Modern Go generics could provide compile-time safety and avoid reflection overhead.

- **Telemetry duplication and defaults**
  - Both OpenTracing and OpenTelemetry middleware exist; only OpenTracing and others are in defaults; OTel not in defaults. Potential duplication/confusion.

- **Serialization choices**
  - Core abstracts typed decode/encode, but wire format consistency across providers differs:
    - Google v2 uses attributes and data natively.
    - NATS/RabbitMQ wrap into a `MessageWrapper` protobuf to carry metadata and publish time.
    - Kafka doesn’t preserve metadata/publish time by default.
  - This is fine for in-process consumers (since each provider translates back to `Msg`) but external/system-of-record consumers will see inconsistent payload shapes.

- **Thread safety of provider-owned maps**
  - Some providers guard maps with locks (google2, rabbitmq), others do not (nats `topics` map). Potential races on subscribe/shutdown.

- **Deprecated protobuf APIs**
  - Code uses `github.com/golang/protobuf` instead of `google.golang.org/protobuf` in core `publish.go` and providers.

- **Lifecycle API shape**
  - `Subscribe` is fire-and-forget; no handle is returned to allow targeted unsubscription or shutdown coordination. Shutdown has no context and returns no error.

### Evidence snippets
- Middleware pipeline for OpenTelemetry context propagation and span handling:

```67:80:/Users/amarin/work/workspaces/experiments/lileo-pubsub/middleware/opentelemetry/opentelemetry.go
func (o Middleware) PublisherMsgInterceptor(serviceName string, next pubsub.PublishHandler) pubsub.PublishHandler {
	return func(ctx context.Context, topic string, m *pubsub.Msg) error {
		if m.Metadata == nil {
			m.Metadata = map[string]string{}
		}

		// First extract baggage
		prop := propagation.TextMapPropagator(propagation.Baggage{})
		prop.Inject(ctx, MetadataCarrier(m.Metadata))

		// Then extract standard context using specified propagator
		otel.GetTextMapPropagator().Inject(ctx, MetadataCarrier(m.Metadata))
```

- Google v2 subscribe concurrency control and handler invocation with `Ack/Nack` closures:

```137:190:/Users/amarin/work/workspaces/experiments/lileo-pubsub/providers/google2/google2.go
// Use semaphore to control concurrency
sem := semaphore.NewWeighted(int64(concurrency))
// ...
err = subscriber.Receive(subCtx, func(ctx context.Context, msg *pubsub.Message) {
	// Acquire semaphore to control concurrency
	if err := sem.Acquire(ctx, 1); err != nil {
		// ...
		return
	}
	defer sem.Release(1)
	// Create a context with deadline for this message
	msgCtx, cancel := context.WithTimeout(ctx, opts.Deadline)
	defer cancel()
	// Convert pubsub.Message to ps.Msg
	publishTime := msg.PublishTime
	psMsg := ps.Msg{
		ID:          msg.ID,
		Metadata:    msg.Attributes,
		Data:        msg.Data,
		PublishTime: &publishTime,
		Ack: func() { msg.Ack() },
		Nack: func() { msg.Nack() },
	}
	// Call the handler
	err := h(msgCtx, psMsg)
	// ...
})
```

### Prioritized recommendations
1. Replace global state with a composable manager
   - Provide a `Manager` that holds named clients and exposes `Subscribe`/`Publish` without globals. Make it concurrency-safe, DI-friendly, and testable. Keep a legacy global for back-compat but mark deprecated.

2. Make `Subscribe` return a handle
   - Change `Provider.Subscribe` to return `(Subscription, error)` where `Subscription.Close(ctx)` gracefully cancels. Propagate errors instead of panicking during ensure/create. Add `Shutdown(ctx) error`.

3. Fix publish error semantics and race
   - Use `errgroup.Group` for both `Publish` and `PublishJSON`. Aggregate errors (return first or combine). Protect `PublishResult.Err` or expose `[]error`.

4. Adopt generics for handler typing
   - Add typed APIs:
     - `OnProto[T proto.Message](opts, fn func(ctx context.Context, *T, *Msg) error)`
     - `OnJSON[T any](opts, fn func(ctx context.Context, *T, *Msg) error)`
   - Keep reflective path for back-compat but deprecate.

5. Normalize ack semantics
   - For RabbitMQ, always consume with manual acks and rely on `AutoAck` in core to call `Ack()` automatically after successful handler return. This keeps behavior uniform with NATS/Google/Kafka.

6. Context correctness
   - Stop using `context.Background()` for long operations unless justified; thread the caller’s context (or document the async separation and use a derived context with sane timeouts). Accept contexts on `Shutdown`/`Close`.

7. Standardize envelope and serialization
   - Define a core envelope with metadata and timestamp and mandate providers to map from transport to `Msg`. If a provider lacks headers, use the envelope. Publish time should always be set on `Msg`.
   - Provide a pluggable serializer interface to support Proto/JSON/Avro with content-type metadata.

8. Migrate to `google.golang.org/protobuf`
   - Update all marshalling paths to new API; remove old proto module to reduce duplication.

9. Strengthen provider thread safety and lifecycle
   - Guard provider state maps universally; add tests for concurrent subscribe/shutdown.

10. Observability and defaults
   - Prefer OpenTelemetry middleware in defaults; optionally keep OpenTracing behind a separate import path. Provide a metrics middleware that records errors distinctly and includes labels for handler names.

11. Testing and CI
   - Add google v2 provider tests (emulator-backed). Add race-detector runs and data race tests for publish fan-out. Add middleware chain order tests and end-to-end ack behavior tests across providers.

12. Documentation
   - Document provider-specific behavior (e.g., RabbitMQ exchange type/topic, dead-letter support for Cloud Run). Clarify concurrency knobs mapping to provider settings and the implications of `AutoAck`.

### Extensibility verdict
- **Overall**: The library is extensible where it matters (providers and middleware are easy to plug in). The global state and reflective subscriber API limit composability in larger systems. With the recommended changes (typed handlers, handle-returning subscriptions, contextful lifecycle, and standardized serialization), it would scale better for complex deployments and enable safer integration patterns.

- **Adding a provider**: Easy today; just implement `Provider`. But prepare to manage your own topic/queue creation, ack, concurrency, and metadata mapping—these are not abstracted by the core.

- **Adding middleware**: Very easy; the chain is clean. Order matters; defaults enforce one ordering.

I’m ready to help implement any of the above (e.g., typed `OnProto`/`OnJSON`, publish error handling, RabbitMQ manual ack normalization, or replacing the global client with a manager).

- Completed core abstractions, providers, middleware, lifecycle, and docs/tests reviews. I compiled the prioritized recommendations.

```go
type Provider interface {
	Publish(ctx context.Context, topic string, m *Msg) error
	Subscribe(opts HandlerOptions, handler MsgHandler)
	Shutdown()
}
```

```go
var (
	clients          = []*Client{&Client{Provider: NoopProvider{}}}
	publishWaitGroup sync.WaitGroup
)
```

```go
// SetClient sets the global pubsub client, useful in tests
func SetClient(cli *Client) {
	clients = []*Client{cli}
}

// GetClient get the global pubsub client, useful in tests
func GetClient() *Client {
	return clients[0]
}
```

```go
func chainPublisherMiddleware(mw ...Middleware) func(serviceName string, next PublishHandler) PublishHandler {
	return func(serviceName string, final PublishHandler) PublishHandler {
		return func(ctx context.Context, topic string, m *Msg) error {
			last := final
			for i := len(mw) - 1; i >= 0; i-- {
				last = mw[i].PublisherMsgInterceptor(serviceName, last)
			}
			return last(ctx, topic, m)
		}
	}
}
```

```go
// Reflection is slow, but this is done only once on subscriber setup
hndlr := reflect.TypeOf(opts.Handler)
if hndlr.Kind() != reflect.Func {
	panic("lile pubsub: handler needs to be a func")
}

if hndlr.NumIn() != 3 {
	panic(`lile pubsub: handler should be of format
		func(ctx context.Context, obj *proto.Message, msg *Msg) error
		but didn't receive enough args`)
}

if hndlr.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() {
	panic(`lile pubsub: handler should be of format
		func(ctx context.Context, obj *proto.Message, msg *Msg) error
		but first arg was not context.Context`)
}
```

```go
// Check if subscription exists and create if needed
err = g.ensureSubscription(ctx, subscriptionName, topicName, opts.Deadline)
if err != nil {
	logrus.Panicf("Can't ensure subscription %s: %s", subName, err.Error())
}
```

```go
go func(c *Client) {
	defer publishWaitGroup.Done()
	defer wg.Done()
	err := c.Publish(ctx, topic, obj, true)
	if err != nil {
		pr.Err = err
	}
}(c)
```

```go
msgs, err := ch.Consume(
	queue.Name, // queue
	"",         // consumer tag
	opts.AutoAck, // auto-ack
	false,        // exclusive
	false,        // no-local
	false,        // no-wait
	nil,          // arguments
)
```

```go
func (o Middleware) PublisherMsgInterceptor(serviceName string, next pubsub.PublishHandler) pubsub.PublishHandler {
	return func(ctx context.Context, topic string, m *pubsub.Msg) error {
		if m.Metadata == nil {
			m.Metadata = map[string]string{}
		}

		// First extract baggage
		prop := propagation.TextMapPropagator(propagation.Baggage{})
		prop.Inject(ctx, MetadataCarrier(m.Metadata))

		// Then extract standard context using specified propagator
		otel.GetTextMapPropagator().Inject(ctx, MetadataCarrier(m.Metadata))
```

```go
// Use semaphore to control concurrency
sem := semaphore.NewWeighted(int64(concurrency))
// ...
err = subscriber.Receive(subCtx, func(ctx context.Context, msg *pubsub.Message) {
	// Acquire semaphore to control concurrency
	if err := sem.Acquire(ctx, 1); err != nil {
		// ...
		return
	}
	defer sem.Release(1)
	// Create a context with deadline for this message
	msgCtx, cancel := context.WithTimeout(ctx, opts.Deadline)
	defer cancel()
	// Convert pubsub.Message to ps.Msg
	publishTime := msg.PublishTime
	psMsg := ps.Msg{
		ID:          msg.ID,
		Metadata:    msg.Attributes,
		Data:        msg.Data,
		PublishTime: &publishTime,
		Ack: func() { msg.Ack() },
		Nack: func() { msg.Nack() },
	}
	// Call the handler
	err := h(msgCtx, psMsg)
	// ...
})
```

---

**User**

write this analysis to architecture-analysis.md

---

**Cursor**

I'll add a new `architecture-analysis.md` at the repo root with the findings and recommendations.

```markdown
*** Begin Patch
*** Add File: /Users/amarin/work/workspaces/experiments/lileo-pubsub/architecture-analysis.md
## Architecture Analysis

### Overview
- **Purpose**: Facade over multiple Pub/Sub backends with a consistent consumer handler signature and pluggable middleware for publish/subscribe paths.
- **Scope**: Providers for Google Pub/Sub (v1/v2 + Cloud Run), NATS Streaming, RabbitMQ, experimental Kafka, and an in-memory provider. Built-in middleware: logging, tracing, metrics, recovery, audit.

### Core abstractions and flow
- **Provider interface** (adapter): small surface enabling backend substitution.

```go
type Provider interface {
    Publish(ctx context.Context, topic string, m *Msg) error
    Subscribe(opts HandlerOptions, handler MsgHandler)
    Shutdown()
}
```

- **Client** with global registry: a single default client for subscribers and a slice of publisher clients for fan-out through global `Publish` helpers. Middleware chains decorate both publish and subscribe pipelines.

```go
func chainPublisherMiddleware(mw ...Middleware) func(serviceName string, next PublishHandler) PublishHandler {
    return func(serviceName string, final PublishHandler) PublishHandler {
        return func(ctx context.Context, topic string, m *Msg) error {
            last := final
            for i := len(mw) - 1; i >= 0; i-- {
                last = mw[i].PublisherMsgInterceptor(serviceName, last)
            }
            return last(ctx, topic, m)
        }
    }
}
```

- **Subscriber handlers**: user functions validated/reflected at runtime; decoding via protobuf by default or JSON when `HandlerOptions.JSON` is true.
- **Message abstraction**: `Msg` carries `ID`, `Metadata`, `Data`, `PublishTime`, and closures `Ack`/`Nack`.

### Architectural patterns
- **Adapter pattern**: `Provider` implementations encapsulate backend specifics (topics/queues, acks, concurrency, metadata mapping).
- **Interceptor/decorator**: Middleware chain for both publish and subscribe with clean, symmetric hooks.
- **Facade + global state**: Top-level `Publish`, `PublishJSON`, `Subscribe`, `Shutdown` functions wrap a global client model.
- **Best-effort convergence of transport**: Providers translate transport-specific messages to a unified `Msg` for handlers.

### Extensibility
- **Providers**: High. Implementing `Provider` is straightforward; lifecycle and mapping decisions remain provider-owned.
- **Middleware**: High. Two simple hooks make cross-cutting concerns easy to add and order.
- **Subscriber API**: Medium. Reflection-based typing yields runtime panics and no compile-time guarantees.
- **Configuration**: Medium. `HandlerOptions` covers core knobs; provider-specific tuning is embedded in providers rather than abstracted at the core level.
- **Composition**: The global client model is simple but less DI-friendly for larger systems or multi-tenant apps.

### Strengths
- Clean separation between core, providers, and middleware.
- Small, stable interfaces; easy to plug new providers and middleware.
- Sensible defaults for concurrency/deadlines and convenient JSON/Proto support.
- Observability built-in (Prometheus, tracing, logging, recovery).

### Issues and improvement opportunities
1. Global mutable state and lifecycle coupling
   - Global `clients` slice and a blocking `wait` channel introduce shared mutable state, complicate tests, and limit composition.
   - No synchronization on `clients` mutations; subscribers implicitly use index 0.

2. Error handling via panics
   - Provider setup paths panic on ensure/create failures (e.g., Google v2 topic/subscription ensure), which is unfriendly for libraries.

3. Publish error semantics and data race
   - `PublishJSON` writes to a shared `PublishResult.Err` from multiple goroutines without synchronization; diverges from `Publish` which uses `errgroup`.

4. Inconsistent ack semantics across providers
   - RabbitMQ consumes with `auto-ack` when `AutoAck` is true, bypassing manual `Ack`/`Nack` control and diverging from other providers that consistently use manual acks and let core logic auto-ack on success.

5. Context handling inconsistencies
   - Several paths ignore caller context (`context.Background()` for long ops or publish `Get()`), weakening cancellation/timeout guarantees.

6. Reflection-based handler typing
   - Runtime reflection panics for signature mismatches; overhead at bind time; limited ergonomics compared to generics.

7. Serialization and envelope divergence
   - Providers differ on wire format (native vs protobuf wrapper), making external system consumers see different payloads. Internally this is masked by the `Msg` abstraction but complicates interop.

8. Thread safety gaps in providers
   - Some providers guard internal maps with locks; others (e.g., NATS) expose potential races (maps updated on subscribe/shutdown paths).

9. Legacy protobuf API usage
   - Library uses `github.com/golang/protobuf` in several places; modern `google.golang.org/protobuf` is preferred.

10. Lifecycle API shape
   - `Subscribe` doesn’t return a handle for targeted unsubscription; `Shutdown` has no context and no error reporting.

### Prioritized recommendations
1. Replace global state with a composable manager
   - Introduce a `Manager` (or `Bus`) type that holds named clients, is concurrency-safe, and exposes `Publish`/`Subscribe`. Keep global functions for back-compat but mark deprecated.

2. Make `Subscribe` return a handle and propagate errors
   - Change `Provider.Subscribe` to return `(Subscription, error)` where `Subscription.Close(ctx)` cancels/awaits shutdown. Remove panics in setup; return errors to callers. Add `Shutdown(ctx) error` semantics.

3. Fix publish concurrency and error aggregation
   - Use `errgroup` for both `Publish` and `PublishJSON`. Aggregate and expose errors deterministically (first error, all errors, or a combined error type). Ensure thread-safe writes to results.

4. Adopt generics for handler typing
   - Add typed helpers: `OnProto[T proto.Message](opts, fn func(context.Context, *T, *Msg) error)` and `OnJSON[T any](...)`, avoiding reflection and providing compile-time safety. Keep reflective API for compatibility and deprecate over time.

5. Normalize ack behavior
   - Consume with manual acks across providers. Let core’s `AutoAck` invoke `Ack()` after successful handler return, ensuring consistent semantics.

6. Thread contexts through operations
   - Avoid `context.Background()` for long or blocking operations unless strictly required; derive contexts with timeouts for async publishes. Accept contexts on `Shutdown`/`Close` to bound cleanup time.

7. Standardize envelope and serialization
   - Define a canonical envelope (data + metadata + timestamps) and a serializer interface (Proto/JSON/Avro). Providers should map to/from transport specifics while core remains consistent. Always set `PublishTime` on `Msg`.

8. Migrate to modern protobuf API
   - Replace `github.com/golang/protobuf` with `google.golang.org/protobuf` throughout the codebase.

9. Harden provider thread safety and lifecycle
   - Guard provider maps with mutexes consistently; add concurrent subscribe/shutdown tests; verify cleanup of unique/temporary subscriptions.

10. Observability defaults
   - Prefer OpenTelemetry middleware in defaults; keep OpenTracing as an optional, non-default path. Expand metrics with labeled error counters and handler names.

11. Testing and CI
   - Add emulator-backed tests for Google v2; race detector runs; publish fan-out error aggregation tests; middleware order tests; cross-provider ack-behavior parity tests.

12. Documentation
   - Document provider-specific behavior (RabbitMQ exchange/queue model, Cloud Run push + dead-letter policies). Clarify how `Concurrency`, `Deadline`, `AutoAck`, and `Unique` map to provider settings.

### Extensibility verdict
- **Providers**: High—small surface and clear responsibilities.
- **Middleware**: High—simple, symmetric hooks; order-sensitive but explicit.
- **Handlers/configuration**: Medium—runtime reflection and global client state constrain ergonomics and DI; moving to generics and a manager improves this significantly.

### Suggested migration path (incremental)
- v2.x: Add `Manager`, typed `OnProto/OnJSON`, manual-ack normalization for new providers, OTel in defaults. Keep globals and reflective API.
- v3: Deprecate globals; require handle-returning `Subscribe`. Complete protobuf migration; standardized envelope and serializer interface.

*** End Patch
```

```markdown
## Architecture Analysis

### Overview
- **Purpose**: Facade over multiple Pub/Sub backends with a consistent consumer handler signature and pluggable middleware for publish/subscribe paths.
- **Scope**: Providers for Google Pub/Sub (v1/v2 + Cloud Run), NATS Streaming, RabbitMQ, experimental Kafka, and an in-memory provider. Built-in middleware: logging, tracing, metrics, recovery, audit.

### Core abstractions and flow
- **Provider interface** (adapter): small surface enabling backend substitution.

```go
type Provider interface {
    Publish(ctx context.Context, topic string, m *Msg) error
    Subscribe(opts HandlerOptions, handler MsgHandler)
    Shutdown()
}
```

- **Client** with global registry: a single default client for subscribers and a slice of publisher clients for fan-out through global `Publish` helpers. Middleware chains decorate both publish and subscribe pipelines.

```go
func chainPublisherMiddleware(mw ...Middleware) func(serviceName string, next PublishHandler) PublishHandler {
    return func(serviceName string, final PublishHandler) PublishHandler {
        return func(ctx context.Context, topic string, m *Msg) error {
            last := final
            for i := len(mw) - 1; i >= 0; i-- {
                last = mw[i].PublisherMsgInterceptor(serviceName, last)
            }
            return last(ctx, topic, m)
        }
    }
}
```

- **Subscriber handlers**: user functions validated/reflected at runtime; decoding via protobuf by default or JSON when `HandlerOptions.JSON` is true.
- **Message abstraction**: `Msg` carries `ID`, `Metadata`, `Data`, `PublishTime`, and closures `Ack`/`Nack`.

### Architectural patterns
- **Adapter pattern**: `Provider` implementations encapsulate backend specifics (topics/queues, acks, concurrency, metadata mapping).
- **Interceptor/decorator**: Middleware chain for both publish and subscribe with clean, symmetric hooks.
- **Facade + global state**: Top-level `Publish`, `PublishJSON`, `Subscribe`, `Shutdown` functions wrap a global client model.
- **Best-effort convergence of transport**: Providers translate transport-specific messages to a unified `Msg` for handlers.

### Extensibility
- **Providers**: High. Implementing `Provider` is straightforward; lifecycle and mapping decisions remain provider-owned.
- **Middleware**: High. Two simple hooks make cross-cutting concerns easy to add and order.
- **Subscriber API**: Medium. Reflection-based typing yields runtime panics and no compile-time guarantees.
- **Configuration**: Medium. `HandlerOptions` covers core knobs; provider-specific tuning is embedded in providers rather than abstracted at the core level.
- **Composition**: The global client model is simple but less DI-friendly for larger systems or multi-tenant apps.

### Strengths
- Clean separation between core, providers, and middleware.
- Small, stable interfaces; easy to plug new providers and middleware.
- Sensible defaults for concurrency/deadlines and convenient JSON/Proto support.
- Observability built-in (Prometheus, tracing, logging, recovery).

### Issues and improvement opportunities
1. Global mutable state and lifecycle coupling
   - Global `clients` slice and a blocking `wait` channel introduce shared mutable state, complicate tests, and limit composition.
   - No synchronization on `clients` mutations; subscribers implicitly use index 0.

2. Error handling via panics
   - Provider setup paths panic on ensure/create failures (e.g., Google v2 topic/subscription ensure), which is unfriendly for libraries.

3. Publish error semantics and data race
   - `PublishJSON` writes to a shared `PublishResult.Err` from multiple goroutines without synchronization; diverges from `Publish` which uses `errgroup`.

4. Inconsistent ack semantics across providers
   - RabbitMQ consumes with `auto-ack` when `AutoAck` is true, bypassing manual `Ack`/`Nack` control and diverging from other providers that consistently use manual acks and let core logic auto-ack on success.

5. Context handling inconsistencies
   - Several paths ignore caller context (`context.Background()`) for long ops or publish `Get()`, weakening cancellation/timeout guarantees.

6. Reflection-based handler typing
   - Runtime reflection panics for signature mismatches; overhead at bind time; limited ergonomics compared to generics.

7. Serialization and envelope divergence
   - Providers differ on wire format (native vs protobuf wrapper), making external system consumers see different payloads. Internally this is masked by the `Msg` abstraction but complicates interop.

8. Thread safety gaps in providers
   - Some providers guard internal maps with locks; others (e.g., NATS) expose potential races (maps updated on subscribe/shutdown paths).

9. Legacy protobuf API usage
   - Library uses `github.com/golang/protobuf` in several places; modern `google.golang.org/protobuf` is preferred.

10. Lifecycle API shape
   - `Subscribe` doesn’t return a handle for targeted unsubscription; `Shutdown` has no context and no error reporting.

### Prioritized recommendations
1. Replace global state with a composable manager
   - Introduce a `Manager` (or `Bus`) type that holds named clients, is concurrency-safe, and exposes `Publish`/`Subscribe`. Keep global functions for back-compat but mark deprecated.

2. Make `Subscribe` return a handle and propagate errors
   - Change `Provider.Subscribe` to return `(Subscription, error)` where `Subscription.Close(ctx)` cancels/awaits shutdown. Remove panics in setup; return errors to callers. Add `Shutdown(ctx) error` semantics.

3. Fix publish concurrency and error aggregation
   - Use `errgroup` for both `Publish` and `PublishJSON`. Aggregate and expose errors deterministically (first error, all errors, or a combined error type). Ensure thread-safe writes to results.

4. Adopt generics for handler typing
   - Add typed helpers: `OnProto[T proto.Message](opts, fn func(context.Context, *T, *Msg) error)` and `OnJSON[T any](...)`, avoiding reflection and providing compile-time safety. Keep reflective API for compatibility and deprecate over time.

5. Normalize ack behavior
   - Consume with manual acks across providers. Let core’s `AutoAck` invoke `Ack()` after successful handler return, ensuring consistent semantics.

6. Thread contexts through operations
   - Avoid `context.Background()` for long or blocking operations unless strictly required; derive contexts with timeouts for async publishes. Accept contexts on `Shutdown`/`Close` to bound cleanup time.

7. Standardize envelope and serialization
   - Define a canonical envelope (data + metadata + timestamps) and a serializer interface (Proto/JSON/Avro). Providers should map to/from transport specifics while core remains consistent. Always set `PublishTime` on `Msg`.

8. Migrate to modern protobuf API
   - Replace `github.com/golang/protobuf` with `google.golang.org/protobuf` throughout the codebase.

9. Harden provider thread safety and lifecycle
   - Guard provider maps with mutexes consistently; add concurrent subscribe/shutdown tests; verify cleanup of unique/temporary subscriptions.

10. Observability defaults
   - Prefer OpenTelemetry middleware in defaults; keep OpenTracing as an optional, non-default path. Expand metrics with labeled error counters and handler names.

11. Testing and CI
   - Add emulator-backed tests for Google v2; race detector runs; publish fan-out error aggregation tests; middleware order tests; cross-provider ack-behavior parity tests.

12. Documentation
   - Document provider-specific behavior (RabbitMQ exchange/queue model, Cloud Run push + dead-letter policies). Clarify how `Concurrency`, `Deadline`, `AutoAck`, and `Unique` map to provider settings.

### Extensibility verdict
- **Providers**: High—small surface and clear responsibilities.
- **Middleware**: High—simple, symmetric hooks; order-sensitive but explicit.
- **Handlers/configuration**: Medium—runtime reflection and global client state constrain ergonomics and DI; moving to generics and a manager improves this significantly.

### Suggested migration path (incremental)
- v2.x: Add `Manager`, typed `OnProto/OnJSON`, manual-ack normalization for new providers, OTel in defaults. Keep globals and reflective API.
- v3: Deprecate globals; require handle-returning `Subscribe`. Complete protobuf migration; standardized envelope and serializer interface.


```

Added `architecture-analysis.md` at the repo root with the full architecture review and prioritized recommendations.

- The report covers core abstractions, patterns, extensibility, issues, and a migration path.
- Key fixes recommended: remove globals, return subscription handles, unify ack semantics, adopt generics, standardize serialization, modernize protobuf, and improve context/lifecycle handling.

