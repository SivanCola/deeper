import { createHandler, validateReport } from "./handler.ts";

const token = "test-ingest-token-".repeat(4);
const report = {
  eventId: "a".repeat(32),
  dedupKey: "b".repeat(64),
  kind: "feedback",
  schemaVersion: 4,
  version: "v1.0.0",
  os: "darwin",
  arch: "arm64",
  message: "测试反馈",
};
const env = (name: string) =>
  ({
    SUPABASE_URL: "https://example.supabase.co",
    SUPABASE_SECRET_KEYS: JSON.stringify({ default: "sb_secret_test" }),
  } as Record<string, string>)[name];
function assert(value: unknown, message = "assertion failed"): asserts value {
  if (!value) throw new Error(message);
}
function request(value: unknown = report, suppliedToken = token): Request {
  return new Request("https://example.supabase.co/functions/v1/report-ingest", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Deepzora-Report-Token": suppliedToken,
    },
    body: JSON.stringify(value),
  });
}
const mustNotFetch: typeof fetch = () => {
  throw new Error("invalid request reached storage");
};

Deno.test("desktop feedback and CLI crash keep the existing wire format", async () => {
  const handler = createHandler({
    env,
    fetch: (url, options) => {
      assert(
        url ===
          "https://example.supabase.co/rest/v1/rpc/submit_deepzora_report",
      );
      const headers = new Headers(options?.headers);
      assert(headers.get("apikey") === "sb_secret_test");
      assert(!headers.has("Authorization"));
      assert(!headers.has("X-Deepzora-Report-Token"));
      const body = JSON.parse(String(options?.body));
      assert(/^[0-9a-f]{64}$/.test(body.p_token_hash));
      assert(!String(options?.body).includes(token));
      assert(body.p_report.message === report.message);
      assert(!("apiKey" in body.p_report));
      assert(
        body.p_report.device === undefined ||
          body.p_report.device.distroId === "ubuntu",
      );
      return Promise.resolve(Response.json({ status: "accepted" }));
    },
  });
  for (
    const input of [
      {
        ...report,
        apiKey: "must not persist",
        device: { cores: 8, distroId: "ubuntu", secret: "drop" },
      },
      {
        ...report,
        kind: "crash",
        schemaVersion: 2,
        source: "cli.go",
        stack: "run.go:42",
      },
    ]
  ) {
    const result = await handler(request(input));
    assert(result.status === 202);
    assert((await result.json()).accepted === true);
    assert(result.headers.get("Cache-Control") === "no-store");
  }
});

Deno.test("collector fails closed without configuration or a valid token", async () => {
  const handler = createHandler({ env, fetch: mustNotFetch });
  for (const supplied of ["", "wrong", "x".repeat(257)]) {
    assert((await handler(request(report, supplied))).status === 401);
  }
  for (
    const missing of [
      "SUPABASE_URL",
      "SUPABASE_SECRET_KEYS",
    ]
  ) {
    const unconfigured = createHandler({
      env: (name) => name === missing ? undefined : env(name),
      fetch: mustNotFetch,
    });
    assert((await unconfigured(request())).status === 503);
  }
  assert((await handler(new Request("https://example.com"))).status === 405);
});

Deno.test("malformed reports are rejected before database access", async () => {
  const handler = createHandler({ env, fetch: mustNotFetch });
  for (
    const bad of [
      null,
      [],
      { ...report, eventId: "bad" },
      { ...report, dedupKey: "bad" },
      { ...report, kind: "unknown" },
      { ...report, schemaVersion: "4" },
      { ...report, schemaVersion: 5 },
      { ...report, message: " " },
      { ...report, stack: "x".repeat(8193) },
      { ...report, device: { cores: -1 } },
      { ...report, diagnostics: { exitCode: "1" } },
      { ...report, breadcrumbs: Array(31).fill({ t: 1, cat: "ui" }) },
    ]
  ) assert((await handler(request(bad))).status === 400);
  const malformed = request();
  assert((await handler(new Request(malformed, { body: "{" }))).status === 400);
  const wrongType = request();
  wrongType.headers.set("Content-Type", "text/plain");
  assert((await handler(wrongType)).status === 415);
});

Deno.test("streamed bodies are bounded even without Content-Length", async () => {
  const handler = createHandler({ env, fetch: mustNotFetch });
  const headers = request().headers;
  const huge = new Request("https://example.com", {
    method: "POST",
    headers,
    body: "x".repeat(512 * 1024 + 1),
  });
  assert((await handler(huge)).status === 413);
  headers.set("Content-Length", String(512 * 1024 + 1));
  assert(
    (await handler(
      new Request("https://example.com", {
        method: "POST",
        headers,
        body: "{}",
      }),
    )).status === 413,
  );
});

Deno.test("durable retries, throttling and storage failures have distinct HTTP responses", async () => {
  for (
    const [stored, status] of [
      ["accepted", 202],
      ["duplicate", 200],
      [
        "rate_limited",
        429,
      ],
      ["unauthorized", 401],
      ["not_configured", 503],
      ["unknown", 503],
    ] as const
  ) {
    const handler = createHandler({
      env,
      fetch: () => Promise.resolve(Response.json({ status: stored })),
    });
    const result = await handler(request());
    assert(result.status === status);
    if (status === 429) assert(result.headers.get("Retry-After") === "60");
  }
  for (
    const send of [
      () =>
        Promise.resolve(
          new Response("private database details", { status: 500 }),
        ),
      () => Promise.reject(new Error("database credential")),
    ]
  ) {
    const result = await createHandler({ env, fetch: send })(request());
    assert(
      result.status === 503 && !((await result.text()).includes("private")),
    );
  }
});

Deno.test("modern server keys use apikey rather than a JWT bearer header", async () => {
  const handler = createHandler({
    env: (name) =>
      name === "SUPABASE_SECRET_KEYS"
        ? JSON.stringify({ default: "sb_secret_server_only" })
        : env(name),
    fetch: (_url, options) => {
      const headers = new Headers(options?.headers);
      assert(
        headers.get("apikey") === "sb_secret_server_only" &&
          !headers.has("Authorization"),
      );
      return Promise.resolve(Response.json({ status: "accepted" }));
    },
  });
  assert((await handler(request())).status === 202);
});

Deno.test("invalid current keys never fall back to a legacy server key", async () => {
  for (
    const keys of [
      undefined,
      "{",
      "null",
      "{}",
      '{"default":42}',
      '{"default":""}',
    ]
  ) {
    const handler = createHandler({
      env: (name) =>
        name === "SUPABASE_SECRET_KEYS"
          ? keys
          : name === "SUPABASE_SERVICE_ROLE_KEY"
          ? "legacy-key"
          : env(name),
      fetch: mustNotFetch,
    });
    const result = await handler(request());
    assert(result.status === 503);
    assert((await result.json()).error === "collector_not_configured");
  }
});

Deno.test("native diagnostics and breadcrumbs are preserved without extra fields", () => {
  const cleaned = validateReport({
    ...report,
    kind: "crash",
    diagnostics: {
      processRole: "service",
      exitCode: 1,
      signal: 9,
      runId: "run-1",
      extra: "drop",
    },
    breadcrumbs: [{ t: 123, cat: "startup", msg: "ready", extra: "drop" }],
  });
  assert(
    JSON.stringify(cleaned).includes("run-1") &&
      !JSON.stringify(cleaned).includes("extra"),
  );
});
