import { v4 as uuidv4 } from "uuid";

// Types
interface TraceEvent {
  type: string;
  target?: string;
  x?: number;
  y?: number;
  name?: string;
  value?: string;
  url?: string;
  payload?: any;
  ts: number;
}

interface BatchPayload {
  sessionId: string;
  events: TraceEvent[];
  traceId?: string | null;
}

interface InitOptions {
  endpoint?: string;
  sessionId?: string;
  batchSize?: number;
  batchMs?: number;
}

interface TraceForgeSDK {
  track: (name: string, payload: any) => void;
}

// Global state
let sessionId: string;
let eventBuffer: TraceEvent[] = [];
let currentTraceId: string | null = null;
let flushTimer: ReturnType<typeof setTimeout> | null = null;
let batchSize = 100;
let batchMs = 100;
let endpoint = "/v1/sessions";
let initialized = false;

// Cookie helpers
function setCookie(name: string, value: string, days: number): void {
  const expires = new Date(
    Date.now() + days * 24 * 60 * 60 * 1000,
  ).toUTCString();
  document.cookie = `${name}=${value}; expires=${expires}; path=/; SameSite=Lax`;
}

function getCookie(name: string): string | null {
  const match = document.cookie.match(new RegExp("(^| )" + name + "=([^;]+)"));
  return match ? match[2] : null;
}

// Session ID management
function getSessionId(provided?: string): string {
  if (provided) {
    return provided;
  }

  // Try cookie first
  const cookieId = getCookie("tf_session");
  if (cookieId) {
    return cookieId;
  }

  // Try localStorage
  const storageId = localStorage.getItem("tf_session");
  if (storageId) {
    setCookie("tf_session", storageId, 365);
    return storageId;
  }

  // Generate new ID
  const newId = uuidv4();
  setCookie("tf_session", newId, 365);
  localStorage.setItem("tf_session", newId);
  return newId;
}

// Fetch wrapper to capture trace ID
const originalFetch = window.fetch;
function wrapFetch(): void {
  window.fetch = async function (...args): Promise<Response> {
    const response = await originalFetch.apply(this, args);

    // Check if the request had X-OTEL-TRACE-ID header
    const headers = args[1]?.headers;
    if (headers) {
      const traceId =
        headers instanceof Headers
          ? headers.get("X-OTEL-TRACE-ID")
          : (headers as Record<string, string>)["X-OTEL-TRACE-ID"];
      if (traceId) {
        currentTraceId = traceId;
      }
    }

    return response;
  };
}

// XMLHttpRequest wrapper to capture trace ID
const originalXHROpen = XMLHttpRequest.prototype.open;
const originalXHRSend = XMLHttpRequest.prototype.send;
let pendingTraceId: string | null = null;

function wrapXHR(): void {
  XMLHttpRequest.prototype.open = function (
    method: string,
    url: string,
    async: boolean = true,
    username?: string | null,
    password?: string | null,
  ) {
    (this as any)._url = url;
    return originalXHROpen.apply(this, [
      method,
      url,
      async,
      username ?? null,
      password ?? null,
    ]);
  };

  const originalSetRequestHeader = XMLHttpRequest.prototype.setRequestHeader;
  XMLHttpRequest.prototype.setRequestHeader = function (
    name: string,
    value: string,
  ) {
    if (name === "X-OTEL-TRACE-ID") {
      pendingTraceId = value;
    }
    return originalSetRequestHeader.apply(this, [name, value]);
  };

  XMLHttpRequest.prototype.send = function (body?: any) {
    if (pendingTraceId) {
      currentTraceId = pendingTraceId;
      pendingTraceId = null;
    }
    return originalXHRSend.apply(this, [body]);
  };
}

// Event listeners
function setupClickListener(): void {
  document.addEventListener("click", (e: MouseEvent) => {
    const traceEvent: TraceEvent = {
      type: "click",
      target: (e.target as Element)?.toString() || "",
      x: e.clientX,
      y: e.clientY,
      ts: Date.now(),
    };
    queueEvent(traceEvent);
  });
}

function setupInputListener(): void {
  document.addEventListener("input", (e: globalThis.Event) => {
    const target = e.target as
      | HTMLInputElement
      | HTMLTextAreaElement
      | HTMLSelectElement
      | null;
    if (
      target &&
      (target.tagName === "INPUT" ||
        target.tagName === "TEXTAREA" ||
        target.tagName === "SELECT")
    ) {
      const traceEvent: TraceEvent = {
        type: "input",
        name: target.name || "",
        value: target.value || "",
        ts: Date.now(),
      };
      queueEvent(traceEvent);
    }
  });
}

function setupNavigationListener(): void {
  const recordNavigation = () => {
    const traceEvent: TraceEvent = {
      type: "navigation",
      url: location.href,
      ts: Date.now(),
    };
    queueEvent(traceEvent);
  };

  window.addEventListener("popstate", recordNavigation);
  window.addEventListener("hashchange", recordNavigation);
}

// Event batching
function queueEvent(event: TraceEvent): void {
  eventBuffer.push(event);

  // Start timer on first event
  if (eventBuffer.length === 1) {
    flushTimer = setTimeout(flushEvents, batchMs);
  }

  // Flush if batch is full
  if (eventBuffer.length >= batchSize) {
    flushEvents();
  }
}

// Flush events to backend
async function flushEvents(): Promise<void> {
  if (eventBuffer.length === 0) {
    return;
  }

  if (flushTimer) {
    clearTimeout(flushTimer);
    flushTimer = null;
  }

  const eventsToSend = [...eventBuffer];
  eventBuffer = [];

  const payload: BatchPayload = {
    sessionId,
    events: eventsToSend,
    traceId: currentTraceId,
  };

  // Reset trace ID after using it
  currentTraceId = null;

  // Send with retry logic
  let attempts = 0;
  const maxAttempts = 3;

  while (attempts < maxAttempts) {
    try {
      await fetch(endpoint, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(payload),
      });
      break;
    } catch (error) {
      attempts++;
      if (attempts < maxAttempts) {
        await new Promise((resolve) => setTimeout(resolve, 1000));
      }
    }
  }
}

// Public API
export function init(opts: InitOptions = {}): TraceForgeSDK {
  if (initialized) {
    console.warn("TraceForgeSDK already initialized");
    return { track };
  }

  // Configure options
  endpoint = opts.endpoint || "/v1/sessions";
  batchSize = opts.batchSize || 100;
  batchMs = opts.batchMs || 100;
  sessionId = getSessionId(opts.sessionId);

  // Wrap fetch and XHR to capture trace IDs
  wrapFetch();
  wrapXHR();

  // Setup event listeners
  setupClickListener();
  setupInputListener();
  setupNavigationListener();

  initialized = true;

  return { track };
}

export function track(name: string, payload: any): void {
  if (!initialized) {
    console.warn("TraceForgeSDK not initialized. Call init() first.");
    return;
  }

  const traceEvent: TraceEvent = {
    type: "custom",
    name,
    payload,
    ts: Date.now(),
  };
  queueEvent(traceEvent);
}

// Export for UMD
if (typeof window !== "undefined") {
  (window as any).TraceForgeSDK = { init, track };
}

export default { init, track };
