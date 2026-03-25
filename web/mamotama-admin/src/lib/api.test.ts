import assert from "node:assert/strict";
import test, { afterEach } from "node:test";
import { apiGetBinary, apiGetJson, apiPutJson, getAPIKey, setAPIKey } from "./api.js";

const originalFetch = globalThis.fetch;

afterEach(() => {
    globalThis.fetch = originalFetch;
    setAPIKey("");
});

test("apiGetJson uses default API base path", async () => {
    let requestedUrl = "";
    globalThis.fetch = async (input) => {
        requestedUrl = String(input);
        return new Response(JSON.stringify({ ok: true }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
        });
    };

    const out = await apiGetJson<{ ok: boolean }>("/status");
    assert.equal(out.ok, true);
    assert.equal(requestedUrl, "/mamotama-api/status");
});

test("apiGetJson surfaces API error payload", async () => {
    globalThis.fetch = async () => {
        return new Response(JSON.stringify({ error: "denied" }), {
            status: 403,
            headers: { "Content-Type": "application/json" },
        });
    };

    await assert.rejects(() => apiGetJson("/status"), /denied/);
});

test("apiPutJson sends JSON body and content type", async () => {
    let method = "";
    let contentType = "";
    let body = "";
    globalThis.fetch = async (_input, init) => {
        method = String(init?.method || "");
        contentType = new Headers(init?.headers).get("Content-Type") || "";
        body = String(init?.body || "");
        return new Response(JSON.stringify({ ok: true }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
        });
    };

    await apiPutJson("/rules", { a: 1 });
    assert.equal(method, "PUT");
    assert.equal(contentType, "application/json");
    assert.equal(body, JSON.stringify({ a: 1 }));
});

test("apiGetJson attaches X-API-Key when configured", async () => {
    let sentAPIKey = "";
    setAPIKey("test-api-key");
    globalThis.fetch = async (_input, init) => {
        sentAPIKey = new Headers(init?.headers).get("X-API-Key") || "";
        return new Response(JSON.stringify({ ok: true }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
        });
    };

    const out = await apiGetJson<{ ok: boolean }>("/status");
    assert.equal(out.ok, true);
    assert.equal(getAPIKey(), "test-api-key");
    assert.equal(sentAPIKey, "test-api-key");
});

test("apiGetBinary decodes RFC5987 filename", async () => {
    globalThis.fetch = async () => {
        return new Response(new Blob(["hello"]), {
            status: 200,
            headers: {
                "Content-Type": "application/gzip",
                "Content-Disposition": "attachment; filename*=UTF-8''waf%20events.ndjson.gz",
            },
        });
    };

    const out = await apiGetBinary("/logs/download?src=waf");
    assert.equal(out.contentType, "application/gzip");
    assert.equal(out.filename, "waf events.ndjson.gz");
    assert.equal(out.blob.size, 5);
});
