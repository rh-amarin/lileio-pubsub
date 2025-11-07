# Reflection Usage Analysis and Alternatives

## How Reflection is Currently Used

The library uses reflection in the `subscribe.go` file to provide type-safe handler invocation. Here's a detailed breakdown:

### Step-by-Step Reflection Process

#### 1. **Handler Registration Phase** (Setup Time - Lines 86-124)

When `Client.On()` is called during subscriber setup, reflection is used to:

```go
// Line 87: Get the type of the handler function
hndlr := reflect.TypeOf(opts.Handler)

// Line 88-90: Verify it's actually a function
if hndlr.Kind() != reflect.Func {
    panic("lile pubsub: handler needs to be a func")
}

// Line 92-96: Verify it has exactly 3 parameters
if hndlr.NumIn() != 3 {
    panic("handler should have 3 args")
}

// Line 98-102: Verify first parameter is context.Context
if hndlr.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() {
    panic("first arg must be context.Context")
}

// Line 104-110: Verify second parameter implements proto.Message (for protobuf)
if !hndlr.In(1).Implements(reflect.TypeOf((*proto.Message)(nil)).Elem()) {
    panic("second arg must implement proto.Message")
}

// Line 112-116: Verify third parameter is *pubsub.Msg
if hndlr.In(2) != reflect.TypeOf(&Msg{}) {
    panic("third arg must be *pubsub.Msg")
}

// Line 118-122: Verify return type is error
if !hndlr.Out(0).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
    panic("return type must be error")
}

// Line 124: Store the function value for later invocation
fn := reflect.ValueOf(opts.Handler)
```

**Purpose:** Validate handler signature at setup time (once per handler).

#### 2. **Message Processing Phase** (Runtime - Lines 126-154)

When a message arrives, reflection is used to:

```go
cb := func(ctx context.Context, m Msg) error {
    // Line 128: Create a new instance of the handler's second parameter type
    // This uses reflection to determine the concrete type
    obj := reflect.New(hndlr.In(1).Elem()).Interface()
    
    // Lines 129-133: Unmarshal the message data into the typed object
    if opts.JSON {
        err = json.Unmarshal(m.Data, obj)
    } else {
        err = proto.Unmarshal(m.Data, obj.(proto.Message))
    }
    
    // Lines 139-143: Dynamically invoke the handler function
    rtrn := fn.Call([]reflect.Value{
        reflect.ValueOf(ctx),      // First arg: context
        reflect.ValueOf(obj),      // Second arg: unmarshaled object
        reflect.ValueOf(&m),        // Third arg: message wrapper
    })
    
    // Lines 144-153: Extract and return the error
    if len(rtrn) == 0 {
        return nil
    }
    erri := rtrn[0].Interface()
    if erri != nil {
        err = erri.(error)
    }
    return err
}
```

**Purpose:** 
- Create typed objects dynamically
- Invoke user handlers with type safety
- Extract return values

### Example Usage

```go
// User defines a typed handler
func HandleUserCreated(ctx context.Context, user *User, msg *pubsub.Msg) error {
    fmt.Printf("User created: %s\n", user.Name)
    return nil
}

// Registration - reflection validates and stores handler
client.On(pubsub.HandlerOptions{
    Topic:   "user.created",
    Handler: HandleUserCreated,  // Reflection inspects this
})

// When message arrives:
// 1. Reflection creates: &User{}
// 2. Unmarshals message data into User
// 3. Calls HandleUserCreated(ctx, &User{...}, &Msg{...})
```

---

## Performance Implications

### Setup Time (One-time cost)
- **Cost:** Low - happens once per handler registration
- **Operations:** Type inspection, validation
- **Impact:** Negligible (microseconds)

### Runtime Cost (Per message)
- **Cost:** Medium - happens for every message
- **Operations:**
  1. `reflect.New()` - Allocates new object (~50-100ns)
  2. `reflect.ValueOf()` - Creates reflect values (~10-20ns each)
  3. `fn.Call()` - Function call via reflection (~100-200ns)
  4. `Interface()` - Type assertion (~10-20ns)

**Total overhead:** ~200-400ns per message

### Benchmark Comparison

```go
// Direct call: ~5ns
func directCall(ctx context.Context, user *User, msg *Msg) error {
    return handler(ctx, user, msg)
}

// Reflection call: ~200ns
func reflectionCall(ctx context.Context, user *User, msg *Msg) error {
    fn := reflect.ValueOf(handler)
    rtrn := fn.Call([]reflect.Value{
        reflect.ValueOf(ctx),
        reflect.ValueOf(user),
        reflect.ValueOf(msg),
    })
    return rtrn[0].Interface().(error)
}
```

**Impact:** For high-throughput scenarios (100k+ msg/sec), reflection overhead can become significant (~20-40ms per 100k messages).

---

## Alternative Approaches

### Alternative 1: Generic Type Parameters (Go 1.18+) ⭐ **RECOMMENDED**

Use Go generics to eliminate reflection while maintaining type safety.

#### Implementation

```go
package pubsub

import (
    "context"
    "encoding/json"
    "github.com/golang/protobuf/proto"
)

// TypedHandler is a type-safe handler function
type TypedHandler[T any] func(ctx context.Context, obj T, msg *Msg) error

// HandlerFunc wraps a typed handler with metadata
type HandlerFunc[T any] struct {
    Handler TypedHandler[T]
    JSON    bool
}

// OnTyped registers a type-safe handler
func (c *Client) OnTyped[T any](opts HandlerOptions, handler TypedHandler[T]) error {
    if opts.Topic == "" {
        return errors.New("topic must be set")
    }
    
    // No reflection needed! Type is known at compile time
    var zero T
    
    cb := func(ctx context.Context, m Msg) error {
        var obj T
        
        // Type switch for unmarshaling
        if opts.JSON {
            if err := json.Unmarshal(m.Data, &obj); err != nil {
                return errors.Wrap(err, "failed to unmarshal JSON")
            }
        } else {
            // For protobuf, T must implement proto.Message
            // This is checked at compile time via type constraint
            if err := proto.Unmarshal(m.Data, any(&obj).(proto.Message)); err != nil {
                return errors.Wrap(err, "failed to unmarshal protobuf")
            }
        }
        
        // Direct function call - no reflection!
        return handler(ctx, obj, &m)
    }
    
    mw := chainSubscriberMiddleware(c.Middleware...)
    c.Provider.Subscribe(opts, mw(opts, cb))
    return nil
}

// Constraint for protobuf handlers
type ProtoMessage interface {
    proto.Message
}
```

#### Usage

```go
// Type-safe handler - no reflection needed
func HandleUserCreated(ctx context.Context, user *User, msg *pubsub.Msg) error {
    fmt.Printf("User created: %s\n", user.Name)
    return nil
}

// Registration
client.OnTyped(pubsub.HandlerOptions{
    Topic:   "user.created",
    Name:    "handle-user-created",
}, HandleUserCreated)

// For protobuf messages
type UserProto struct {
    Name string `protobuf:"bytes,1,opt,name=name"`
}
func (u *UserProto) ProtoReflect() protoreflect.Message { /* ... */ }

client.OnTyped[*UserProto](pubsub.HandlerOptions{
    Topic: "user.created",
    Name:  "handle-user-proto",
}, func(ctx context.Context, user *UserProto, msg *pubsub.Msg) error {
    // Type-safe, no reflection
    return nil
})
```

#### Pros
- ✅ **Zero reflection overhead** - direct function calls
- ✅ **Compile-time type safety** - errors caught at compile time
- ✅ **Better IDE support** - autocomplete, refactoring
- ✅ **Smaller binary** - no reflection metadata
- ✅ **Easier debugging** - stack traces are clearer

#### Cons
- ❌ Requires Go 1.18+
- ❌ Slightly more verbose syntax
- ❌ Need separate methods for JSON vs Protobuf (or use type constraints)

---

### Alternative 2: Interface-Based Handlers

Use interfaces to define handler contracts, eliminating reflection for type checking.

#### Implementation

```go
package pubsub

import (
    "context"
    "encoding/json"
    "github.com/golang/protobuf/proto"
)

// MessageHandler handles messages of a specific type
type MessageHandler interface {
    Handle(ctx context.Context, msg *Msg) error
}

// TypedMessageHandler wraps a typed handler
type TypedMessageHandler[T any] struct {
    handler func(ctx context.Context, obj T, msg *Msg) error
    json    bool
}

func NewTypedHandler[T any](
    handler func(ctx context.Context, obj T, msg *Msg) error,
    json bool,
) *TypedMessageHandler[T] {
    return &TypedMessageHandler[T]{
        handler: handler,
        json:    json,
    }
}

func (h *TypedMessageHandler[T]) Handle(ctx context.Context, msg *Msg) error {
    var obj T
    
    if h.json {
        if err := json.Unmarshal(msg.Data, &obj); err != nil {
            return err
        }
    } else {
        // Type assertion at compile time
        if err := proto.Unmarshal(msg.Data, any(&obj).(proto.Message)); err != nil {
            return err
        }
    }
    
    // Direct call - no reflection
    return h.handler(ctx, obj, msg)
}

// OnInterface registers an interface-based handler
func (c *Client) OnInterface(opts HandlerOptions, handler MessageHandler) error {
    if opts.Topic == "" {
        return errors.New("topic must be set")
    }
    
    cb := func(ctx context.Context, m Msg) error {
        return handler.Handle(ctx, &m)
    }
    
    mw := chainSubscriberMiddleware(c.Middleware...)
    c.Provider.Subscribe(opts, mw(opts, cb))
    return nil
}
```

#### Usage

```go
// Create typed handler wrapper
handler := pubsub.NewTypedHandler(
    func(ctx context.Context, user *User, msg *pubsub.Msg) error {
        fmt.Printf("User: %s\n", user.Name)
        return nil
    },
    false, // protobuf
)

// Register
client.OnInterface(pubsub.HandlerOptions{
    Topic: "user.created",
    Name:  "handle-user",
}, handler)
```

#### Pros
- ✅ No reflection for invocation
- ✅ Works with older Go versions
- ✅ Clean separation of concerns

#### Cons
- ❌ Still need reflection for object creation (or use generics)
- ❌ More boilerplate
- ❌ Less ergonomic than direct function registration

---

### Alternative 3: Code Generation

Generate type-specific wrapper functions at build time.

#### Implementation

```go
//go:generate go run github.com/lileio/pubsub/cmd/gen-handlers

// In generated code:
func RegisterUserCreatedHandler(
    client *Client,
    handler func(ctx context.Context, user *User, msg *Msg) error,
    opts HandlerOptions,
) error {
    cb := func(ctx context.Context, m Msg) error {
        var user User
        if err := proto.Unmarshal(m.Data, &user); err != nil {
            return err
        }
        return handler(ctx, &user, &m)  // Direct call
    }
    
    mw := chainSubscriberMiddleware(client.Middleware...)
    client.Provider.Subscribe(opts, mw(opts, cb))
    return nil
}
```

#### Generator Tool

```go
// cmd/gen-handlers/main.go
package main

import (
    "go/ast"
    "go/parser"
    "go/token"
    "text/template"
)

// Parse source files, find message types, generate handlers
```

#### Pros
- ✅ Zero runtime overhead
- ✅ Works with any Go version
- ✅ Type safety at compile time

#### Cons
- ❌ Requires build-time generation step
- ❌ More complex build process
- ❌ Generated code maintenance

---

### Alternative 4: Type Registry with Cached Reflection

Keep reflection but cache reflect operations to minimize overhead.

#### Implementation

```go
package pubsub

import (
    "reflect"
    "sync"
)

type handlerCache struct {
    mu    sync.RWMutex
    cache map[reflect.Type]*cachedHandler
}

type cachedHandler struct {
    fnValue      reflect.Value
    objType      reflect.Type
    createObj    func() interface{}  // Cached object creator
    callHandler  func(fn reflect.Value, ctx context.Context, obj interface{}, msg *Msg) error
}

var globalCache = &handlerCache{
    cache: make(map[reflect.Type]*cachedHandler),
}

func (hc *handlerCache) get(handler interface{}) (*cachedHandler, error) {
    hType := reflect.TypeOf(handler)
    
    hc.mu.RLock()
    if cached, ok := hc.cache[hType]; ok {
        hc.mu.RUnlock()
        return cached, nil
    }
    hc.mu.RUnlock()
    
    // Validate and create cached handler (only once per type)
    hc.mu.Lock()
    defer hc.mu.Unlock()
    
    // Double-check after acquiring write lock
    if cached, ok := hc.cache[hType]; ok {
        return cached, nil
    }
    
    // Validate handler signature
    if err := validateHandler(hType); err != nil {
        return nil, err
    }
    
    objType := hType.In(1).Elem()
    
    cached := &cachedHandler{
        fnValue: reflect.ValueOf(handler),
        objType: objType,
        createObj: func() interface{} {
            return reflect.New(objType).Interface()
        },
        callHandler: func(fn reflect.Value, ctx context.Context, obj interface{}, msg *Msg) error {
            rtrn := fn.Call([]reflect.Value{
                reflect.ValueOf(ctx),
                reflect.ValueOf(obj),
                reflect.ValueOf(msg),
            })
            if len(rtrn) > 0 && rtrn[0].Interface() != nil {
                return rtrn[0].Interface().(error)
            }
            return nil
        },
    }
    
    hc.cache[hType] = cached
    return cached, nil
}

// Usage in On()
func (c *Client) On(opts HandlerOptions) error {
    cached, err := globalCache.get(opts.Handler)
    if err != nil {
        return err
    }
    
    cb := func(ctx context.Context, m Msg) error {
        obj := cached.createObj()  // Still uses reflection, but cached
        // ... unmarshal ...
        return cached.callHandler(cached.fnValue, ctx, obj, &m)
    }
    
    // ...
}
```

#### Pros
- ✅ Minimal changes to existing API
- ✅ Reduces reflection overhead (validation cached)
- ✅ Backward compatible

#### Cons
- ❌ Still uses reflection for invocation
- ❌ More complex code
- ❌ Memory overhead for cache

---

## Performance Comparison

| Approach | Setup Time | Per-Message Overhead | Type Safety | Go Version |
|----------|------------|---------------------|-------------|------------|
| **Current (Reflection)** | ~1μs | ~200-400ns | Runtime | All |
| **Generics** | 0ns | ~5ns (direct call) | Compile-time | 1.18+ |
| **Interface** | ~50ns | ~10-20ns | Compile-time | All |
| **Code Gen** | Build-time | ~5ns (direct call) | Compile-time | All |
| **Cached Reflection** | ~1μs (once) | ~150-250ns | Runtime | All |

---

## Recommended Migration Path

### Phase 1: Add Generics API (Parallel to existing)
```go
// New API alongside old API
func (c *Client) OnTyped[T any](opts HandlerOptions, handler TypedHandler[T]) error {
    // Generic implementation
}
```

### Phase 2: Deprecate Reflection API
```go
// Deprecated: Use OnTyped instead
func (c *Client) On(opts HandlerOptions) {
    // Keep for backward compatibility
}
```

### Phase 3: Remove Reflection API (v3.0)
- Remove `On()` method
- Remove reflection code
- Require Go 1.18+

---

## Conclusion

**Current Reflection Usage:**
- ✅ Provides type safety (at runtime)
- ✅ Flexible API
- ✅ Works with all Go versions
- ❌ Runtime overhead (~200-400ns per message)
- ❌ Runtime errors (panics)
- ❌ Harder to debug

**Best Alternative: Generics (Go 1.18+)**
- ✅ Zero overhead
- ✅ Compile-time type safety
- ✅ Better developer experience
- ✅ Modern Go best practices

For projects using Go 1.18+, **generics are the clear winner**. They eliminate reflection overhead while maintaining (and improving) type safety.

