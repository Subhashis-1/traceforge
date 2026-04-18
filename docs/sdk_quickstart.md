# Traceforge JavaScript SDK - Quick Start Guide

A lightweight, framework-agnostic SDK for session tracking and event collection. Works with any Single Page Application (SPA).

## Installation

### From npm (Production)

```bash
npm install @traceforge/sdk-js
```

### From Local Source (Development)

```bash
npm install ../pkg/sdk-js
```

### CDN (Optional)

```html
<script src="https://cdn.jsdelivr.net/npm/@traceforge/sdk-js/dist/traceforge-sdk.js"></script>
```

---

## Initialization

### Basic Setup

```typescript
import { init, track } from "@traceforge/sdk-js";

// Initialize the SDK
const sdk = init({
  endpoint: "http://localhost:8080/v1/sessions",
});

// Track custom events
document.getElementById("myBtn")?.addEventListener("click", () => {
  track("myCustomEvent", { foo: "bar" });
});
```

### With Custom Configuration

```typescript
init({
  endpoint: "https://your-api.com/v1/sessions", // Backend endpoint
  sessionId: "custom-session-id", // Optional: override auto-generated session ID
  batchSize: 50, // Flush after N events (default: 100)
  batchMs: 200, // Flush after N milliseconds (default: 100)
});
```

### Framework Examples

#### React

```tsx
import { useEffect } from "react";
import { init, track } from "@traceforge/sdk-js";

function App() {
  useEffect(() => {
    init({ endpoint: "/v1/sessions" });
  }, []);

  const handleClick = () => {
    track("button_click", { buttonId: "submit" });
  };

  return <button onClick={handleClick}>Submit</button>;
}
```

#### Vue

```vue
<script setup>
import { onMounted } from "vue";
import { init, track } from "@traceforge/sdk-js";

onMounted(() => {
  init({ endpoint: "/v1/sessions" });
});

const handleAction = () => {
  track("user_action", { action: "purchase" });
};
</script>
```

#### Angular

```typescript
import { Component, OnInit } from "@angular/core";
import { init, track } from "@traceforge/sdk-js";

@Component({
  selector: "app-root",
  template: `<button (click)="onClick()">Click me</button>`,
})
export class AppComponent implements OnInit {
  ngOnInit() {
    init({ endpoint: "/v1/sessions" });
  }

  onClick() {
    track("button_click", { label: "main_cta" });
  }
}
```

---

## Automatic Trace-ID Propagation

The SDK automatically injects OpenTelemetry trace correlation headers into outgoing HTTP requests.

### How It Works

- The SDK inspects all outgoing `fetch()` and `XMLHttpRequest` calls
- If a trace ID is present (from OpenTelemetry or generated), it adds the `X-OTEL-TRACE-ID` header
- This enables end-to-end distributed tracing from frontend to backend

### Example

```typescript
// No extra code needed - tracing is automatic
fetch("/api/users")
  .then((res) => res.json())
  .then((data) => console.log(data));

// The request will include:
// X-OTEL-TRACE-ID: <trace-id>
// X-OTEL-SESSION-ID: <session-id>
```

### Manual Trace Context (Advanced)

```typescript
import { setTraceId } from "@traceforge/sdk-js";

// Set trace ID manually (e.g., from backend response)
setTraceId("abc123def456");

// Subsequent requests will use this trace ID
```

---

## Custom Events

Track user interactions, business events, or custom metrics:

```typescript
import { track } from "@traceforge/sdk-js";

// Simple event
track("page_view");

// Event with metadata
track("purchase_completed", {
  productId: "SKU-123",
  amount: 99.99,
  currency: "USD",
});

// Nested objects supported
track("form_submission", {
  formId: "checkout",
  fields: {
    name: "John",
    email: "john@example.com",
  },
  validationErrors: [],
});
```

### Best Practices

- Use **snake_case** or **camelCase** consistently for event names
- Keep payload size reasonable (< 1KB per event)
- Include contextual metadata (user ID, page URL, etc.)

---

## Batching & Payload Size

### Defaults

The SDK batches events before sending to optimize network requests:

- **Batch Size**: 100 events (flushes when buffer reaches 100 events)
- **Batch Time**: 100ms (flushes every 100ms regardless of event count)

### Override Defaults

```typescript
init({
  endpoint: "/v1/sessions",
  batchSize: 50, // Flush after 50 events
  batchMs: 500, // Or after 500ms (whichever comes first)
});
```

### Manual Flush (Advanced)

```typescript
import { flush } from "@traceforge/sdk-js";

// Force immediate flush (e.g., before page unload)
window.addEventListener("beforeunload", () => {
  flush();
});
```

### Payload Size Limits

- Each event: ~200-500 bytes (depending on metadata)
- Default batch: ~20-50KB total
- Recommended maximum: 100KB per batch
- Large payloads are automatically split

---

## Troubleshooting

### Cookie Warnings

#### "Failed to set cookie" in Console

**Cause**: Browser blocking third-party cookies or running in incognito/private mode.

**Solution**:

```typescript
init({
  endpoint: "/v1/sessions",
  sessionId: "manual-session-id", // Provide session ID explicitly
});
```

#### "Cannot read property 'cookie' of undefined"

**Cause**: SDK running in non-browser environment (Node.js, SSR).

**Solution**: Only initialize SDK on client-side:

```typescript
if (typeof window !== "undefined") {
  init({ endpoint: "/v1/sessions" });
}
```

### Events Not Sending

**Check**:

1. Network tab - are requests reaching `/v1/sessions`?
2. CORS headers on backend - is `Access-Control-Allow-Origin` set?
3. Endpoint URL - is it correct and accessible?

**Debug Mode**:

```typescript
init({
  endpoint: "/v1/sessions",
  // Enable verbose logging (if available in your version)
  debug: true,
});
```

### Session ID Not Persisting

**Cause**: Cookies being cleared or SameSite policy blocking.

**Solution**: Ensure backend sets cookies with:

```
Set-Cookie: session_id=xyz; SameSite=Lax; Path=/
```

### Trace ID Not Propagating

**Check**:

1. Is OpenTelemetry initialized in your app?
2. Are you using `fetch` or `XMLHttpRequest`? (WebSocket not supported yet)
3. Check browser dev tools - Network tab - request headers for `X-OTEL-TRACE-ID`

---

## API Reference

### `init(options: InitOptions): TraceForgeSDK`

Initializes the SDK with configuration.

| Option      | Type     | Default        | Description                  |
| ----------- | -------- | -------------- | ---------------------------- |
| `endpoint`  | `string` | `/v1/sessions` | Backend ingestion endpoint   |
| `sessionId` | `string` | auto-generated | Custom session ID            |
| `batchSize` | `number` | `100`          | Events per batch             |
| `batchMs`   | `number` | `100`          | Milliseconds between flushes |

### `track(name: string, payload?: any): void`

Records a custom event.

### `flush(): void`

Forces immediate batch flush.

### `setTraceId(traceId: string): void`

Manually set the current trace ID.

---

## Next Steps

- [Backend Integration Guide](./backend_integration.md)
- [OpenTelemetry Setup](./otel_setup.md)
- [API Reference](../pkg/sdk-js/README.md)

## License

Apache 2.0 - See [LICENSE](../LICENSE) for details.
