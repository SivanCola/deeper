const MAX_BODY_BYTES = 512 * 1024;
const TOKEN_HEADER = "X-Deepzora-Report-Token";
const encoder = new TextEncoder();
const kinds = new Set(["crash", "exception", "feedback", "performance", "bot"]);
type ObjectValue = Record<string, unknown>;
type Dependencies = {
  env: (name: string) => string | undefined;
  fetch?: typeof fetch;
};

class InvalidReport extends Error {}

function response(status: number, body: ObjectValue, headers = {}): Response {
  return Response.json(body, {
    status,
    headers: { "Cache-Control": "no-store", ...headers },
  });
}

function object(value: unknown): ObjectValue {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new InvalidReport();
  }
  return value as ObjectValue;
}

// Keep only fields emitted by the existing desktop/CLI protocol. Unknown
// fields are not persisted, including arbitrary credentials attached by callers.
function stringFields(
  input: ObjectValue,
  limits: Record<string, number>,
): ObjectValue {
  const output: ObjectValue = {};
  for (const [key, maxBytes] of Object.entries(limits)) {
    const value = input[key];
    if (value === undefined) continue;
    if (typeof value !== "string" || encoder.encode(value).length > maxBytes) {
      throw new InvalidReport();
    }
    output[key] = value;
  }
  return output;
}

export function validateReport(value: unknown): ObjectValue {
  const input = object(value);
  const output = stringFields(input, {
    eventId: 32,
    dedupKey: 64,
    installId: 64,
    kind: 32,
    version: 128,
    os: 32,
    arch: 32,
    message: 16384,
    source: 32,
    label: 64,
    errorType: 128,
    errorMessage: 4096,
    errorFamily: 128,
    stack: 8192,
    componentStack: 8192,
    topFrame: 300,
    fingerprintHint: 300,
    buildCommit: 64,
    channel: 32,
    language: 64,
    view: 200,
    occurredAt: 64,
  });
  if (
    !/^[0-9a-f]{32}$/.test(String(output.eventId)) ||
    !/^[0-9a-f]{64}$/.test(String(output.dedupKey)) ||
    !kinds.has(String(output.kind)) ||
    ![2, 3, 4].includes(Number(input.schemaVersion)) ||
    typeof input.schemaVersion !== "number"
  ) {
    throw new InvalidReport();
  }
  for (const key of ["version", "os", "arch", "message"]) {
    if (typeof output[key] !== "string" || !output[key].trim()) {
      throw new InvalidReport();
    }
  }
  output.schemaVersion = input.schemaVersion;
  if (input.device !== undefined) {
    const device = object(input.device);
    const cleaned = stringFields(device, {
      osVersion: 128,
      distroId: 128,
      distroVersion: 128,
      kernelVersion: 128,
      sessionType: 128,
      cpu: 128,
    });
    for (const key of ["osBuild", "osRevision", "cores", "ramGb"]) {
      if (device[key] === undefined) continue;
      if (!Number.isSafeInteger(device[key]) || Number(device[key]) < 0) {
        throw new InvalidReport();
      }
      cleaned[key] = device[key];
    }
    output.device = cleaned;
  }
  if (input.diagnostics !== undefined && input.diagnostics !== null) {
    const diagnostics = object(input.diagnostics);
    const cleaned = stringFields(diagnostics, {
      subjectVersion: 128,
      subjectBuildCommit: 64,
      subjectChannel: 32,
      observerVersion: 128,
      observerBuildCommit: 64,
      runId: 128,
      incidentId: 128,
      processRole: 64,
      lastPhase: 128,
      lastPhaseAt: 64,
      observedAt: 64,
      terminationReason: 128,
      evidence: 128,
      cleanupOutcome: 128,
      category: 128,
    });
    if (diagnostics.exitCode !== undefined) {
      if (!Number.isSafeInteger(diagnostics.exitCode)) {
        throw new InvalidReport();
      }
      cleaned.exitCode = diagnostics.exitCode;
    }
    if (diagnostics.signal !== undefined) {
      if (!Number.isSafeInteger(diagnostics.signal)) throw new InvalidReport();
      cleaned.signal = diagnostics.signal;
    }
    output.diagnostics = cleaned;
  }
  if (input.breadcrumbs !== undefined) {
    if (!Array.isArray(input.breadcrumbs) || input.breadcrumbs.length > 30) {
      throw new InvalidReport();
    }
    output.breadcrumbs = input.breadcrumbs.map((value) => {
      const crumb = object(value);
      if (!Number.isSafeInteger(crumb.t)) throw new InvalidReport();
      return { ...stringFields(crumb, { cat: 64, msg: 240 }), t: crumb.t };
    });
  }
  return output;
}

async function tokenHash(token: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", encoder.encode(token));
  return Array.from(
    new Uint8Array(digest),
    (byte) => byte.toString(16).padStart(2, "0"),
  ).join("");
}

async function readBody(req: Request): Promise<unknown> {
  if (!req.body) throw new InvalidReport();
  const reader = req.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error("body_timeout")), 5000);
  });
  try {
    while (true) {
      const { value, done } = await Promise.race([reader.read(), timeout]);
      if (done) break;
      size += value.byteLength;
      if (size > MAX_BODY_BYTES) throw new Error("body_too_large");
      chunks.push(value);
    }
    const bytes = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) {
      bytes.set(chunk, offset);
      offset += chunk.byteLength;
    }
    return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
  } finally {
    clearTimeout(timer);
    // Do not wait for an untrusted stream's cancellation callback.
    void reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

function databaseKey(env: Dependencies["env"]): string | undefined {
  const keys = env("SUPABASE_SECRET_KEYS");
  if (keys) {
    try {
      const value = JSON.parse(keys).default;
      if (typeof value === "string" && value) return value;
    } catch { /* Fall back to the injected legacy key on older projects. */ }
  }
  return env("SUPABASE_SERVICE_ROLE_KEY");
}

export function createHandler({ env, fetch: send = fetch }: Dependencies) {
  return async (req: Request): Promise<Response> => {
    if (req.method !== "POST") {
      return response(405, { error: "method_not_allowed" }, { Allow: "POST" });
    }
    const url = env("SUPABASE_URL");
    const serviceKey = databaseKey(env);
    if (!url || !serviceKey) {
      return response(503, { error: "collector_not_configured" });
    }
    const supplied = (req.headers.get(TOKEN_HEADER) ?? "").trim();
    if (supplied.length < 32 || supplied.length > 256) {
      return response(401, { error: "unauthorized" });
    }
    if (
      req.headers.get("Content-Type")?.split(";")[0].trim().toLowerCase() !==
        "application/json" ||
      ![null, "identity"].includes(req.headers.get("Content-Encoding"))
    ) {
      return response(415, { error: "unsupported_media_type" });
    }
    if (Number(req.headers.get("Content-Length")) > MAX_BODY_BYTES) {
      return response(413, { error: "report_too_large" });
    }
    let report: ObjectValue;
    try {
      report = validateReport(await readBody(req));
    } catch (error) {
      if (error instanceof Error && error.message === "body_too_large") {
        return response(413, { error: "report_too_large" });
      }
      if (error instanceof Error && error.message === "body_timeout") {
        return response(408, { error: "request_timeout" });
      }
      return response(400, { error: "invalid_report" });
    }
    try {
      const headers: Record<string, string> = {
        "Content-Type": "application/json",
        apikey: serviceKey,
      };
      if (!serviceKey.startsWith("sb_secret_")) {
        headers.Authorization = `Bearer ${serviceKey}`;
      }
      const result = await send(
        `${url.replace(/\/$/, "")}/rest/v1/rpc/submit_deepzora_report`,
        {
          method: "POST",
          headers,
          body: JSON.stringify({
            p_report: report,
            p_token_hash: await tokenHash(supplied),
          }),
          signal: AbortSignal.timeout(5000),
          redirect: "error",
        },
      );
      if (!result.ok) {
        await result.body?.cancel();
        return response(503, { error: "storage_unavailable" });
      }
      const stored = await result.json();
      if (stored.status === "unauthorized") {
        return response(401, { error: "unauthorized" });
      }
      if (stored.status === "not_configured") {
        return response(503, { error: "collector_not_configured" });
      }
      if (stored.status === "rate_limited") {
        return response(429, { error: "rate_limited" }, {
          "Retry-After": "60",
        });
      }
      if (stored.status !== "accepted" && stored.status !== "duplicate") {
        return response(503, { error: "storage_unavailable" });
      }
      return response(stored.status === "duplicate" ? 200 : 202, {
        accepted: true,
        duplicate: stored.status === "duplicate",
        eventId: report.eventId,
      });
    } catch {
      // Never echo database errors, request payloads, or credentials.
      return response(503, { error: "storage_unavailable" });
    }
  };
}
