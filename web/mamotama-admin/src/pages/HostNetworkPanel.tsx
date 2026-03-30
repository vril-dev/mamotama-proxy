import { useCallback, useEffect, useMemo, useState } from "react";
import { apiGetJson, apiPostJson, apiPutJson } from "@/lib/api";

type HostNetworkConfig = {
  enabled: boolean;
  backend: string;
  sysctl_profile: string;
  sysctls?: Record<string, string>;
  state_file?: string;
};

type HostNetworkStatus = {
  state?: string;
  supported?: boolean;
  can_apply_now?: boolean;
  apply_required?: boolean;
  desired_count?: number;
  last_applied_at?: string;
  last_error?: string;
};

type HostNetworkDTO = {
  etag?: string;
  raw?: string;
  host_network?: HostNetworkConfig;
  status?: HostNetworkStatus;
  apply_hint?: string;
  restart_required?: boolean;
};

export default function HostNetworkPanel() {
  const [raw, setRaw] = useState("");
  const [serverRaw, setServerRaw] = useState("");
  const [etag, setEtag] = useState<string | null>(null);
  const [status, setStatus] = useState<HostNetworkStatus | null>(null);
  const [applyHint, setApplyHint] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [validating, setValidating] = useState(false);
  const [messages, setMessages] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [lastSavedAt, setLastSavedAt] = useState<number | null>(null);

  const dirty = useMemo(() => raw !== serverRaw, [raw, serverRaw]);
  const lineCount = useMemo(() => (raw ? raw.split(/\n/).length : 0), [raw]);
  const readOnly = status?.supported === false;

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await apiGetJson<HostNetworkDTO>("/host-network");
      const nextRaw = data.raw ?? JSON.stringify(data.host_network ?? {}, null, 2);
      setRaw(nextRaw);
      setServerRaw(nextRaw);
      setEtag(data.etag ?? null);
      setStatus(data.status ?? null);
      setApplyHint(data.apply_hint ?? "");
      setMessages([]);
    } catch (e: any) {
      setError(e?.message || "Failed to load");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const validate = useCallback(async () => {
    setValidating(true);
    setError(null);
    try {
      const data = await apiPostJson<HostNetworkDTO>("/host-network:validate", { raw });
      setStatus(data.status ?? null);
      setApplyHint(data.apply_hint ?? "");
      setMessages(["Validation OK."]);
    } catch (e: any) {
      setMessages([]);
      setError(e?.message || "validate failed");
    } finally {
      setValidating(false);
    }
  }, [raw]);

  const save = useCallback(async () => {
    setSaving(true);
    setError(null);
    try {
      const data = await apiPutJson<HostNetworkDTO>("/host-network", { raw }, {
        headers: etag ? { "If-Match": etag } : {},
      });
      const nextRaw = JSON.stringify(data.host_network ?? {}, null, 2);
      setRaw(nextRaw);
      setServerRaw(nextRaw);
      setEtag(data.etag ?? null);
      setStatus(data.status ?? null);
      setApplyHint(data.apply_hint ?? "");
      setLastSavedAt(Date.now());
      setMessages(["Saved. Host network settings are persisted. Apply with root before expecting enforcement."]);
    } catch (e: any) {
      setMessages([]);
      setError(e?.message || "save failed");
    } finally {
      setSaving(false);
    }
  }, [etag, raw]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const isSave = (e.key === "s" || e.key === "S") && (e.ctrlKey || e.metaKey);
      if (!isSave) {
        return;
      }
      e.preventDefault();
      if (!saving) {
        void save();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [save, saving]);

  if (loading) {
    return <div className="w-full p-4 text-gray-500">Loading host network settings...</div>;
  }

  return (
    <div className="w-full p-4 space-y-4">
      <header className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold">Host Network</h1>
          <p className="text-sm text-neutral-500">Boot-time L3/L4 baseline tuning. Save persists config only.</p>
        </div>
        <div className="flex items-center gap-2 text-xs">
          <Badge color={status?.apply_required ? "amber" : "green"}>
            {status?.apply_required ? "Apply Required" : "In Sync"}
          </Badge>
          <Badge color={status?.supported ? "green" : "red"}>
            {status?.supported ? "Supported" : "Unsupported"}
          </Badge>
          {etag ? <code className="px-2 py-0.5 bg-neutral-100 rounded">{etag}</code> : null}
        </div>
      </header>

      {error ? (
        <div className="rounded-xl border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-800">{error}</div>
      ) : null}

      {messages.length > 0 ? (
        <div className="rounded-xl border border-green-300 bg-green-50 px-3 py-2 text-sm text-green-800">
          {messages.join(" ")}
        </div>
      ) : null}

      {readOnly ? (
        <div className="rounded-xl border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900">
          This feature is currently Linux-only. Settings remain visible here, but editing and save actions are disabled on unsupported platforms.
        </div>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4 text-sm">
        <Metric label="State" value={String(status?.state ?? "-")} />
        <Metric label="Can Apply Now" value={String(status?.can_apply_now ?? "-")} />
        <Metric label="Desired Sysctls" value={String(status?.desired_count ?? "-")} />
        <Metric label="Last Applied" value={String(status?.last_applied_at ?? "-")} />
      </div>

      <div className="rounded-xl border bg-white p-4 space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="text-sm text-neutral-500">
            Apply hint:
            <code className="ml-2 px-2 py-0.5 bg-neutral-100 rounded break-all">{applyHint || "-"}</code>
          </div>
          <div className="flex items-center gap-2">
            <button className="px-3 py-1.5 rounded-xl shadow text-sm hover:bg-neutral-50 border" onClick={() => void load()}>
              Refresh
            </button>
            <button
              className="px-3 py-1.5 rounded-xl shadow text-sm hover:bg-neutral-50 border"
              onClick={() => void validate()}
              disabled={validating || readOnly}
            >
              {validating ? "Validating..." : "Validate"}
            </button>
            <button
              className="px-3 py-1.5 rounded-xl shadow text-sm bg-black text-white disabled:opacity-50"
              onClick={() => void save()}
              disabled={saving || !dirty || readOnly}
            >
              {saving ? "Saving..." : "Save"}
            </button>
          </div>
        </div>

        <textarea
          className="w-full h-[380px] p-3 border rounded-xl font-mono text-sm leading-5 outline-none focus:ring-2 focus:ring-black/20"
          value={raw}
          onChange={(e) => setRaw(e.target.value)}
          readOnly={readOnly}
          spellCheck={false}
        />

        <div className="flex items-center justify-between text-xs text-neutral-500">
          <div className="flex items-center gap-3">
            <span>Lines: {lineCount}</span>
            {lastSavedAt ? <span>Last saved: {new Date(lastSavedAt).toLocaleString()}</span> : null}
          </div>
          {status?.last_error ? <span className="text-red-700">Last error: {status.last_error}</span> : null}
        </div>
      </div>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-neutral-200 bg-neutral-50 px-3 py-2">
      <div className="text-xs uppercase tracking-wide text-neutral-500">{label}</div>
      <div className="text-sm font-medium break-all">{value}</div>
    </div>
  );
}

function Badge({ color, children }: { color: "green" | "amber" | "red"; children: string }) {
  const className =
    color === "green"
      ? "bg-green-100 text-green-800"
      : color === "amber"
      ? "bg-amber-100 text-amber-800"
      : "bg-red-100 text-red-800";
  return <span className={`px-2 py-0.5 text-xs rounded ${className}`}>{children}</span>;
}
