import { useCallback, useEffect, useMemo, useState } from "react";
import { apiGetJson, apiPostJson, apiPutJson } from "@/lib/api";

type ProxyConfig = {
  upstream_url: string;
  dial_timeout: number;
  response_header_timeout: number;
  idle_conn_timeout: number;
  max_idle_conns: number;
  max_idle_conns_per_host: number;
  max_conns_per_host: number;
  force_http2: boolean;
  disable_compression: boolean;
  expect_continue_timeout: number;
  tls_insecure_skip_verify: boolean;
  tls_client_cert: string;
  tls_client_key: string;
  buffer_request_body: boolean;
  max_response_buffer_bytes: number;
  flush_interval_ms: number;
  health_check_path: string;
  health_check_interval_sec: number;
  health_check_timeout_sec: number;
};

type HealthStatus = {
  status?: string;
  endpoint?: string;
  checked_at?: string;
  last_success_at?: string;
  last_failure_at?: string;
  consecutive_failures?: number;
  last_error?: string;
  last_status_code?: number;
  last_latency_ms?: number;
};

type GetResponse = {
  etag?: string;
  raw?: string;
  proxy?: ProxyConfig;
  health?: HealthStatus;
  rollback_depth?: number;
};

export default function ProxyRulesPanel() {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [raw, setRaw] = useState("");
  const [serverRaw, setServerRaw] = useState("");
  const [etag, setEtag] = useState("");
  const [proxy, setProxy] = useState<ProxyConfig | null>(null);
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [rollbackDepth, setRollbackDepth] = useState(0);
  const [messages, setMessages] = useState<string[]>([]);
  const [probeMessage, setProbeMessage] = useState("");
  const [error, setError] = useState("");

  const dirty = useMemo(() => raw !== serverRaw, [raw, serverRaw]);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    setProbeMessage("");
    try {
      const data = await apiGetJson<GetResponse>("/proxy-rules");
      setRaw(data.raw || "");
      setServerRaw(data.raw || "");
      setEtag(data.etag || "");
      setProxy(data.proxy || null);
      setHealth(data.health || null);
      setRollbackDepth(typeof data.rollback_depth === "number" ? data.rollback_depth : 0);
      setMessages([]);
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const validate = useCallback(async () => {
    setError("");
    try {
      const out = await apiPostJson<{ ok: boolean; messages?: string[]; proxy?: ProxyConfig }>("/proxy-rules:validate", { raw });
      setMessages(Array.isArray(out.messages) ? out.messages : []);
      if (out.proxy) {
        setProxy(out.proxy);
      }
    } catch (e: any) {
      setMessages([e?.message || "validate failed"]);
    }
  }, [raw]);

  const probe = useCallback(async () => {
    setProbeMessage("");
    setError("");
    try {
      const out = await apiPostJson<{ ok: boolean; probe?: { address?: string; latency_ms?: number; timeout_ms?: number }; messages?: string[] }>(
        "/proxy-rules:probe",
        { raw, timeout_ms: 2000 }
      );
      if (out.probe) {
        setProbeMessage(`probe ok: ${out.probe.address || "-"} latency=${out.probe.latency_ms ?? "-"}ms timeout=${out.probe.timeout_ms ?? "-"}ms`);
      } else {
        setProbeMessage("probe ok");
      }
    } catch (e: any) {
      setProbeMessage(`probe failed: ${e?.message || String(e)}`);
    }
  }, [raw]);

  const save = useCallback(async () => {
    setSaving(true);
    setError("");
    try {
      const out = await apiPutJson<{ ok: boolean; etag?: string; proxy?: ProxyConfig }>(
        "/proxy-rules",
        { raw },
        { headers: etag ? { "If-Match": etag } : {} }
      );
      if (!out.ok) {
        throw new Error("save failed");
      }
      setServerRaw(raw);
      if (out.etag) {
        setEtag(out.etag);
      }
      if (out.proxy) {
        setProxy(out.proxy);
      }
      await load();
    } catch (e: any) {
      setError(e?.message || "save failed");
    } finally {
      setSaving(false);
    }
  }, [etag, load, raw]);

  const rollback = useCallback(async () => {
    setSaving(true);
    setError("");
    try {
      await apiPostJson("/proxy-rules:rollback", {});
      await load();
    } catch (e: any) {
      setError(e?.message || "rollback failed");
    } finally {
      setSaving(false);
    }
  }, [load]);

  return (
    <div className="w-full p-4 space-y-4">
      <header className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Proxy Rules</h1>
        <div className="flex items-center gap-2 text-xs">
          <span className="px-2 py-0.5 rounded bg-neutral-100 text-neutral-700">rollback: {rollbackDepth}</span>
          {etag ? <code className="px-2 py-0.5 rounded bg-neutral-100">{etag}</code> : null}
        </div>
      </header>

      {error ? <div className="rounded border border-red-300 bg-red-50 p-2 text-sm">{error}</div> : null}

      <div className="grid gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <button type="button" className="px-3 py-1.5 rounded-xl shadow text-sm border" onClick={() => void load()} disabled={loading || saving}>
            Refresh
          </button>
          <button type="button" className="px-3 py-1.5 rounded-xl shadow text-sm border" onClick={() => void validate()} disabled={loading || saving}>
            Validate
          </button>
          <button type="button" className="px-3 py-1.5 rounded-xl shadow text-sm border" onClick={() => void probe()} disabled={loading || saving}>
            Probe
          </button>
          <button
            type="button"
            className="px-3 py-1.5 rounded-xl shadow text-sm bg-black text-white disabled:opacity-50"
            onClick={() => void save()}
            disabled={loading || saving || !dirty}
          >
            {saving ? "Saving..." : "Save"}
          </button>
          <button
            type="button"
            className="px-3 py-1.5 rounded-xl shadow text-sm border disabled:opacity-50"
            onClick={() => void rollback()}
            disabled={loading || saving || rollbackDepth <= 0}
          >
            Rollback
          </button>
        </div>

        <textarea
          className="w-full h-[420px] p-3 border rounded-xl font-mono text-sm leading-5 outline-none focus:ring-2 focus:ring-black/20"
          value={raw}
          onChange={(e) => setRaw(e.target.value)}
          spellCheck={false}
        />

        {messages.length > 0 ? (
          <div className="rounded border border-neutral-300 bg-neutral-50 p-2 text-xs">
            {messages.map((m, idx) => (
              <div key={`${m}-${idx}`}>{m}</div>
            ))}
          </div>
        ) : null}

        {probeMessage ? <div className="rounded border border-blue-300 bg-blue-50 p-2 text-xs">{probeMessage}</div> : null}
      </div>

      <div className="grid gap-3 md:grid-cols-2 text-xs">
        <div className="rounded border bg-white p-3">
          <div className="font-semibold mb-1">Runtime Proxy</div>
          <pre className="overflow-x-auto">{JSON.stringify(proxy, null, 2)}</pre>
        </div>
        <div className="rounded border bg-white p-3">
          <div className="font-semibold mb-1">Upstream Health</div>
          <pre className="overflow-x-auto">{JSON.stringify(health, null, 2)}</pre>
        </div>
      </div>
    </div>
  );
}
