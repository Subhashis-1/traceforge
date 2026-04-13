# @traceforge/sdk-js

A lightweight, framework-agnostic TypeScript SDK for session tracking and event collection in Traceforge.

## Installation

```bash
npm install @traceforge/sdk-js
```

## Usage

### Basic Initialization

```typescript
import { init, track } from "@traceforge/sdk-js";

// Initialize the SDK
const sdk = init({ 
  endpoint: "http://localhost:8080/v1/sessions" 
});

// Track custom events
document.getElementById("myBtn")?.addEventListener("click", () => {
  track("myCustomEvent", { foo: "bar" });
});
```

### Configuration Options

```typescript
init({
  endpoint: "http://localhost:8080/v1/sessions", // Backend endpoint (default: /v1/sessions)
  sessionId: "custom-session-id",                // Optional custom session ID
  batchSize: 100,                                // Flush after N events (default: 100)
  batchMs: 100                                   // Flush after N milliseconds (default: 100)
});
```

### Automatic Event Collection

The SDK automatically collects:

- **Click events**: Records click coordinates and target element
- **Input events**: Records input field name and value changes
- **Navigation events**: Records URL changes (popstate, hashchange)

### Custom Event Tracking

```typescript
// Track a custom event with payload
track("user_action", {
  action: "purchase",
  productId: "12345",
  amount: 99.99
});
```

### Session Management

The SDK automatically:

- Generates a UUID v4 session ID on first initialization
- Persists the session ID in a cookie (`tf_session`) and localStorage
- Reuses the same session ID across page reloads

### Trace Context Propagation

If your application makes HTTP requests with the `X-OTEL-TRACE-ID` header, the SDK automatically captures and associates the trace ID with event batches.

```typescript
// The SDK will capture this trace ID
fetch("/api/data", {
  headers: {
    "X-OTEL-TRACE-ID": "abc123..."
  }
});
```

## Building from Source

```bash
# Install dependencies
npm install

# Build the UMD bundle
npm run build

# Lint TypeScript
npm run lint
```

## Output

The build produces a single UMD bundle at `dist/traceforge-sdk.js` that can be:

- Imported as an ES module
- Required in CommonJS environments
- Used directly in the browser via `<script>` tag

```html
<script src="traceforge-sdk.js"></script>
<script>
  const sdk = TraceForgeSDK.init({ endpoint: "/v1/sessions" });
  TraceForgeSDK.track("page_view", { url: window.location.href });
</script>
```

## License

Apache-2.0
