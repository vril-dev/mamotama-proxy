import React, { useCallback, useEffect, useMemo, useState } from "react";
import { apiGetJson, apiPostJson, apiPutJson } from "@/lib/api";
import {
  createEmptyDefaultRoute,
  createEmptyRoute,
  createEmptyUpstream,
  headerMapToMultiline,
  multilineToHeaderMap,
  multilineToStringList,
  parseProxyRulesEditor,
  serializeProxyRulesEditor,
  stringListToMultiline,
  type ProxyRoute,
  type ProxyRouteAction,
  type ProxyRouteHeaderOperations,
  type ProxyRulesRoutingEditorState,
  type ProxyUpstream,
} from "@/lib/proxyRulesEditor";

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

type ProxyRuntimeView = Record<string, unknown>;

type DryRunResult = {
  source?: string;
  route_name?: string;
  original_host?: string;
  original_path?: string;
  rewritten_host?: string;
  rewritten_path?: string;
  selected_upstream?: string;
  selected_upstream_url?: string;
  final_url?: string;
};

type GetResponse = {
  etag?: string;
  raw?: string;
  proxy?: ProxyRuntimeView;
  health?: HealthStatus;
  rollback_depth?: number;
};

type RoutePathType = "" | "exact" | "prefix" | "regex";

export default function ProxyRulesPanel() {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [dryRunning, setDryRunning] = useState(false);
  const [raw, setRaw] = useState("");
  const [serverRaw, setServerRaw] = useState("");
  const [etag, setEtag] = useState("");
  const [proxy, setProxy] = useState<ProxyRuntimeView | null>(null);
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [rollbackDepth, setRollbackDepth] = useState(0);
  const [messages, setMessages] = useState<string[]>([]);
  const [probeMessage, setProbeMessage] = useState("");
  const [error, setError] = useState("");
  const [structuredError, setStructuredError] = useState("");
  const [routingState, setRoutingState] = useState<ProxyRulesRoutingEditorState>({
    upstreamURL: "",
    upstreams: [],
    routes: [],
    defaultRoute: null,
  });
  const [routingBase, setRoutingBase] = useState<Record<string, unknown>>({});
  const [dryRunHost, setDryRunHost] = useState("");
  const [dryRunPath, setDryRunPath] = useState("/servicea/users");
  const [dryRunResult, setDryRunResult] = useState<DryRunResult | null>(null);
  const [dryRunMessages, setDryRunMessages] = useState<string[]>([]);

  const dirty = useMemo(() => raw !== serverRaw, [raw, serverRaw]);

  const syncStructuredFromRaw = useCallback((nextRaw: string) => {
    try {
      const parsed = parseProxyRulesEditor(nextRaw);
      setRoutingBase(parsed.base);
      setRoutingState(parsed.state);
      setStructuredError("");
    } catch (e: any) {
      setStructuredError(e?.message || "invalid raw JSON");
    }
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    setProbeMessage("");
    setDryRunMessages([]);
    try {
      const data = await apiGetJson<GetResponse>("/proxy-rules");
      const nextRaw = data.raw || "";
      setRaw(nextRaw);
      setServerRaw(nextRaw);
      setEtag(data.etag || "");
      setProxy(data.proxy || null);
      setHealth(data.health || null);
      setRollbackDepth(typeof data.rollback_depth === "number" ? data.rollback_depth : 0);
      setMessages([]);
      syncStructuredFromRaw(nextRaw);
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setLoading(false);
    }
  }, [syncStructuredFromRaw]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleRawChange = useCallback(
    (nextRaw: string) => {
      setRaw(nextRaw);
      syncStructuredFromRaw(nextRaw);
    },
    [syncStructuredFromRaw]
  );

  const applyStructuredChange = useCallback(
    (updater: (current: ProxyRulesRoutingEditorState) => ProxyRulesRoutingEditorState) => {
      const nextState = updater(routingState);
      const nextRaw = serializeProxyRulesEditor(routingBase, nextState);
      setRaw(nextRaw);
      syncStructuredFromRaw(nextRaw);
    },
    [routingBase, routingState, syncStructuredFromRaw]
  );

  const validate = useCallback(async () => {
    setError("");
    try {
      const out = await apiPostJson<{ ok: boolean; messages?: string[]; proxy?: ProxyRuntimeView }>("/proxy-rules:validate", { raw });
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
      const out = await apiPostJson<{ ok: boolean; probe?: { address?: string; latency_ms?: number; timeout_ms?: number } }>(
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
      const out = await apiPutJson<{ ok: boolean; etag?: string; proxy?: ProxyRuntimeView }>(
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

  const runDryRun = useCallback(async () => {
    setDryRunning(true);
    setDryRunMessages([]);
    try {
      const out = await apiPostJson<{ ok: boolean; dry_run?: DryRunResult; messages?: string[] }>("/proxy-rules:dry-run", {
        raw,
        host: dryRunHost,
        path: dryRunPath,
      });
      setDryRunResult(out.dry_run || null);
      setDryRunMessages(Array.isArray(out.messages) ? out.messages : []);
    } catch (e: any) {
      setDryRunResult(null);
      setDryRunMessages([e?.message || "dry-run failed"]);
    } finally {
      setDryRunning(false);
    }
  }, [dryRunHost, dryRunPath, raw]);

  return (
    <div className="w-full p-4 space-y-4">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold">Proxy Rules</h1>
          <p className="text-sm text-neutral-500">Edit route-aware upstream selection with a structured builder, then keep raw JSON for the rest of the proxy transport knobs.</p>
        </div>
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <Badge color={structuredError ? "red" : "green"}>{structuredError ? "Raw JSON out of sync" : "Structured editor synced"}</Badge>
          {dirty ? <Badge color="amber">Unsaved</Badge> : <Badge color="gray">Saved</Badge>}
          <span className="px-2 py-0.5 rounded bg-neutral-100 text-neutral-700">rollback: {rollbackDepth}</span>
          {etag ? <MonoTag label="ETag" value={etag} /> : null}
        </div>
      </header>

      {error ? <Alert kind="error" title="Error" message={error} onClose={() => setError("")} /> : null}
      {structuredError ? (
        <Alert
          kind="warn"
          title="Structured editor warning"
          message="Raw JSON is currently invalid. The structured editor is still showing the last valid routing snapshot. Any structured edit will regenerate raw JSON from that snapshot."
          onClose={() => setStructuredError("")}
        />
      ) : null}

      <div className="grid gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(320px,1fr)]">
        <div className="space-y-4">
          <SectionCard
            title="Workflow"
            subtitle="Validate, probe, save, and rollback stay on the same raw proxy-rules backend API. Structured edits only touch routing fields."
            actions={
              <div className="flex flex-wrap items-center gap-2">
                <ActionButton onClick={() => void load()} disabled={loading || saving}>
                  Refresh
                </ActionButton>
                <ActionButton onClick={() => void validate()} disabled={loading || saving}>
                  Validate
                </ActionButton>
                <ActionButton onClick={() => void probe()} disabled={loading || saving}>
                  Probe
                </ActionButton>
                <PrimaryButton onClick={() => void save()} disabled={loading || saving || !dirty}>
                  {saving ? "Saving..." : "Save"}
                </PrimaryButton>
                <ActionButton onClick={() => void rollback()} disabled={loading || saving || rollbackDepth <= 0}>
                  Rollback
                </ActionButton>
              </div>
            }
          >
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
              <Field label="Legacy upstream URL" hint="Used when routes/default route do not override it.">
                <input
                  className={inputClass}
                  value={routingState.upstreamURL}
                  onChange={(e) =>
                    applyStructuredChange((current) => ({
                      ...current,
                      upstreamURL: e.target.value,
                    }))
                  }
                  placeholder="http://app.internal:8080"
                />
              </Field>
              <StatBox label="Upstreams" value={String(routingState.upstreams.length)} />
              <StatBox label="Routes" value={String(routingState.routes.length)} />
              <StatBox label="Default route" value={routingState.defaultRoute ? "enabled" : "none"} />
            </div>
          </SectionCard>

          <SectionCard
            title="Upstreams"
            subtitle="Named upstreams are the preferred route targets. A route action can point to one of these names or an absolute http(s) URL."
            actions={
              <ActionButton
                onClick={() =>
                  applyStructuredChange((current) => ({
                    ...current,
                    upstreams: [...current.upstreams, createEmptyUpstream(current.upstreams.length + 1)],
                  }))
                }
              >
                Add upstream
              </ActionButton>
            }
          >
            <div className="space-y-3">
              {routingState.upstreams.length === 0 ? (
                <EmptyState>No named upstreams configured. Routes can still use the legacy `upstream_url` or absolute action URLs.</EmptyState>
              ) : null}
              {routingState.upstreams.map((upstream, index) => (
                <div key={`upstream-${index}`} className="rounded-xl border border-neutral-200 bg-neutral-50 p-4 space-y-3">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <div className="font-medium">Upstream #{index + 1}</div>
                      <div className="text-xs text-neutral-500">Name is referenced from route action.upstream.</div>
                    </div>
                    <button
                      type="button"
                      className="text-sm text-red-700 underline"
                      onClick={() =>
                        applyStructuredChange((current) => ({
                          ...current,
                          upstreams: current.upstreams.filter((_, candidateIndex) => candidateIndex !== index),
                        }))
                      }
                    >
                      Remove
                    </button>
                  </div>
                  <div className="grid gap-3 md:grid-cols-4">
                    <Field label="Name">
                      <input
                        className={inputClass}
                        value={upstream.name}
                        onChange={(e) => updateUpstream(index, { ...upstream, name: e.target.value })}
                        placeholder="service-a"
                      />
                    </Field>
                    <Field label="URL">
                      <input
                        className={inputClass}
                        value={upstream.url}
                        onChange={(e) => updateUpstream(index, { ...upstream, url: e.target.value })}
                        placeholder="http://service-a.internal:8080"
                      />
                    </Field>
                    <Field label="Weight">
                      <input
                        className={inputClass}
                        type="number"
                        min={1}
                        value={upstream.weight}
                        onChange={(e) => updateUpstream(index, { ...upstream, weight: Number(e.target.value || 1) })}
                      />
                    </Field>
                    <Field label="Enabled">
                      <label className="flex items-center gap-2 text-sm">
                        <input
                          type="checkbox"
                          checked={upstream.enabled}
                          onChange={(e) => updateUpstream(index, { ...upstream, enabled: e.target.checked })}
                        />
                        <span>{upstream.enabled ? "Enabled" : "Disabled"}</span>
                      </label>
                    </Field>
                  </div>
                </div>
              ))}
            </div>
          </SectionCard>

          <SectionCard
            title="Routes"
            subtitle="Routes are evaluated in ascending priority. The first match wins, then default_route, then the legacy upstream fallback."
            actions={
              <ActionButton
                onClick={() =>
                  applyStructuredChange((current) => ({
                    ...current,
                    routes: [...current.routes, createEmptyRoute(current.routes.length + 1)],
                  }))
                }
              >
                Add route
              </ActionButton>
            }
          >
            <div className="space-y-4">
              {routingState.routes.length === 0 ? <EmptyState>No route rules configured. Traffic falls through to `default_route` or the legacy upstream settings.</EmptyState> : null}
              {routingState.routes.map((route, index) => (
                <RouteEditorCard
                  key={`route-${index}`}
                  title={`Route #${index + 1}`}
                  route={route}
                  onChange={(next) => updateRoute(index, next)}
                  onRemove={() =>
                    applyStructuredChange((current) => ({
                      ...current,
                      routes: current.routes.filter((_, candidateIndex) => candidateIndex !== index),
                    }))
                  }
                  allowMatch
                />
              ))}
            </div>
          </SectionCard>

          <SectionCard
            title="Default Route"
            subtitle="Used only when no route matches. If absent, the legacy upstream settings are used."
            actions={
              routingState.defaultRoute ? (
                <ActionButton
                  onClick={() =>
                    applyStructuredChange((current) => ({
                      ...current,
                      defaultRoute: null,
                    }))
                  }
                >
                  Remove default route
                </ActionButton>
              ) : (
                <ActionButton
                  onClick={() =>
                    applyStructuredChange((current) => ({
                      ...current,
                      defaultRoute: createEmptyDefaultRoute(),
                    }))
                  }
                >
                  Add default route
                </ActionButton>
              )
            }
          >
            {routingState.defaultRoute ? (
              <RouteEditorCard
                title="Default route"
                route={routingState.defaultRoute}
                onChange={(next) =>
                  applyStructuredChange((current) => ({
                    ...current,
                    defaultRoute: next,
                  }))
                }
                allowMatch={false}
              />
            ) : (
              <EmptyState>Configure a default route when you want a distinct fallback before the legacy upstream behavior.</EmptyState>
            )}
          </SectionCard>

          <SectionCard
            title="Dry Run"
            subtitle="Confirm which route would win and which final upstream URL would be used without changing live traffic."
            actions={
              <PrimaryButton onClick={() => void runDryRun()} disabled={loading || dryRunning}>
                {dryRunning ? "Running..." : "Run dry-run"}
              </PrimaryButton>
            }
          >
            <div className="grid gap-3 md:grid-cols-2">
              <Field label="Host" hint="Optional. Leave empty to simulate host-agnostic routing.">
                <input className={inputClass} value={dryRunHost} onChange={(e) => setDryRunHost(e.target.value)} placeholder="api.example.com" />
              </Field>
              <Field label="Path">
                <input className={inputClass} value={dryRunPath} onChange={(e) => setDryRunPath(e.target.value)} placeholder="/servicea/users" />
              </Field>
            </div>
            {dryRunMessages.length > 0 ? (
              <div className="rounded-xl border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800">
                {dryRunMessages.map((message, index) => (
                  <div key={`dry-run-message-${index}`}>{message}</div>
                ))}
              </div>
            ) : null}
            {dryRunResult ? (
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4 text-sm">
                <StatBox label="Source" value={dryRunResult.source || "-"} />
                <StatBox label="Route" value={dryRunResult.route_name || "-"} />
                <StatBox label="Upstream" value={dryRunResult.selected_upstream || "-"} />
                <StatBox label="Final URL" value={dryRunResult.final_url || "-"} />
              </div>
            ) : null}
          </SectionCard>

          <SectionCard title="Raw JSON" subtitle="Keep using the existing raw editor for transport and low-level fields that are not part of the structured routing editor.">
            <textarea
              className="w-full h-[420px] p-3 border rounded-xl font-mono text-sm leading-5 outline-none focus:ring-2 focus:ring-black/20"
              value={raw}
              onChange={(e) => handleRawChange(e.target.value)}
              spellCheck={false}
            />
          </SectionCard>
        </div>

        <div className="space-y-4">
          {messages.length > 0 ? (
            <SectionCard title="Validate Messages">
              <div className="rounded border border-neutral-300 bg-neutral-50 p-2 text-xs">
                {messages.map((m, idx) => (
                  <div key={`${m}-${idx}`}>{m}</div>
                ))}
              </div>
            </SectionCard>
          ) : null}

          {probeMessage ? (
            <SectionCard title="Probe Result">
              <div className="rounded border border-blue-300 bg-blue-50 p-2 text-xs">{probeMessage}</div>
            </SectionCard>
          ) : null}

          <SectionCard title="Runtime Proxy">
            <pre className="overflow-x-auto text-xs">{JSON.stringify(proxy, null, 2)}</pre>
          </SectionCard>

          <SectionCard title="Upstream Health">
            <pre className="overflow-x-auto text-xs">{JSON.stringify(health, null, 2)}</pre>
          </SectionCard>

          <SectionCard title="Dry Run Result">
            <pre className="overflow-x-auto text-xs">{JSON.stringify(dryRunResult, null, 2)}</pre>
          </SectionCard>
        </div>
      </div>
    </div>
  );

  function updateRoute(index: number, next: ProxyRoute) {
    applyStructuredChange((current) => ({
      ...current,
      routes: current.routes.map((route, routeIndex) => (routeIndex === index ? next : route)),
    }));
  }

  function updateUpstream(index: number, next: ProxyUpstream) {
    applyStructuredChange((current) => ({
      ...current,
      upstreams: current.upstreams.map((upstream, upstreamIndex) => (upstreamIndex === index ? next : upstream)),
    }));
  }
}

function RouteEditorCard({
  title,
  route,
  onChange,
  onRemove,
  allowMatch,
}: {
  title: string;
  route: ProxyRoute;
  onChange: (next: ProxyRoute) => void;
  onRemove?: () => void;
  allowMatch: boolean;
}) {
  const pathType = route.path?.type || "";
  const pathValue = route.path?.value || "";

  const setAction = (nextAction: ProxyRouteAction) => onChange({ ...route, action: nextAction });

  return (
    <div className="rounded-xl border border-neutral-200 bg-neutral-50 p-4 space-y-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <div className="font-medium">{title}</div>
          <div className="text-xs text-neutral-500">Priority decides order. The first matching route wins.</div>
        </div>
        {onRemove ? (
          <button type="button" className="text-sm text-red-700 underline" onClick={onRemove}>
            Remove
          </button>
        ) : null}
      </div>

      <div className="grid gap-3 md:grid-cols-3">
        <Field label="Name">
          <input className={inputClass} value={route.name} onChange={(e) => onChange({ ...route, name: e.target.value })} placeholder="service-a-prefix" />
        </Field>
        {allowMatch ? (
          <Field label="Priority">
            <input
              className={inputClass}
              type="number"
              value={route.priority}
              onChange={(e) => onChange({ ...route, priority: Number(e.target.value || 0) })}
            />
          </Field>
        ) : (
          <Field label="Priority">
            <div className="flex h-10 items-center rounded-xl border border-dashed border-neutral-300 px-3 text-sm text-neutral-500">Not used for default route</div>
          </Field>
        )}
        <Field label="Enabled">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={route.enabled} onChange={(e) => onChange({ ...route, enabled: e.target.checked })} />
            <span>{route.enabled ? "Enabled" : "Disabled"}</span>
          </label>
        </Field>
      </div>

      {allowMatch ? (
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="Hosts" hint="One host per line. Leave empty to match any host.">
            <textarea
              className={textAreaClass}
              value={stringListToMultiline(route.hosts)}
              onChange={(e) => onChange({ ...route, hosts: multilineToStringList(e.target.value) })}
              spellCheck={false}
              placeholder={"api.example.com\n*.example.net"}
            />
          </Field>
          <div className="grid gap-3">
            <Field label="Path match type">
              <select
                className={inputClass}
                value={pathType}
                onChange={(e) => {
                  const nextType = e.target.value as RoutePathType;
                  if (!nextType) {
                    onChange({
                      ...route,
                      path: null,
                      action: route.action.pathRewrite ? { ...route.action, pathRewrite: null } : route.action,
                    });
                    return;
                  }
                  onChange({
                    ...route,
                    path: { type: nextType, value: pathValue || "/" },
                    action: nextType === "regex" ? { ...route.action, pathRewrite: null } : route.action,
                  });
                }}
              >
                <option value="">any path</option>
                <option value="exact">exact</option>
                <option value="prefix">prefix</option>
                <option value="regex">regex</option>
              </select>
            </Field>
            <Field label="Path match value" hint={pathType === "regex" ? "Regex runs against request path only." : "Exact and prefix values should start with /."}>
              <input
                className={inputClass}
                value={pathValue}
                onChange={(e) => onChange({ ...route, path: route.path ? { ...route.path, value: e.target.value } : { type: "prefix", value: e.target.value } })}
                placeholder={pathType === "regex" ? "^/servicea/(users|orders)/[0-9]+$" : "/servicea/"}
                disabled={!pathType}
              />
            </Field>
          </div>
        </div>
      ) : null}

      <div className="grid gap-3 md:grid-cols-3">
        <Field label="Action upstream" hint="Upstream name or absolute http(s) URL.">
          <input
            className={inputClass}
            value={route.action.upstream}
            onChange={(e) => setAction({ ...route.action, upstream: e.target.value })}
            placeholder="service-a or http://service-a.internal:8080"
          />
        </Field>
        <Field label="Host rewrite" hint="Optional outbound Host header override.">
          <input
            className={inputClass}
            value={route.action.hostRewrite}
            onChange={(e) => setAction({ ...route.action, hostRewrite: e.target.value })}
            placeholder="service-a.internal"
          />
        </Field>
        <Field label="Path rewrite prefix" hint={pathType === "regex" ? "Disabled for regex path routes." : "Optional. Example: /service-a/"}>
          <input
            className={inputClass}
            value={route.action.pathRewrite?.prefix || ""}
            onChange={(e) =>
              setAction({
                ...route.action,
                pathRewrite: e.target.value ? { prefix: e.target.value } : null,
              })
            }
            placeholder="/service-a/"
            disabled={!allowMatch || pathType === "regex"}
          />
        </Field>
      </div>

      <div className="grid gap-3 md:grid-cols-4">
        <Field label="Canary upstream" hint="Optional secondary upstream for weighted canary routing.">
          <input
            className={inputClass}
            value={route.action.canaryUpstream}
            onChange={(e) => setAction({ ...route.action, canaryUpstream: e.target.value })}
            placeholder="service-a-canary"
          />
        </Field>
        <Field label="Canary %" hint="1-99 when canary is set.">
          <input
            className={inputClass}
            type="number"
            min={1}
            max={99}
            value={route.action.canaryWeightPercent}
            onChange={(e) => setAction({ ...route.action, canaryWeightPercent: Number(e.target.value || 10) })}
            disabled={!route.action.canaryUpstream.trim()}
          />
        </Field>
        <Field label="Hash policy" hint="Optional sticky/hash routing policy for this route.">
          <select
            className={inputClass}
            value={route.action.hashPolicy}
            onChange={(e) =>
              setAction({
                ...route.action,
                hashPolicy: e.target.value as "" | "client_ip" | "header" | "cookie" | "jwt_sub",
                hashKey: e.target.value === "header" || e.target.value === "cookie" ? route.action.hashKey : "",
              })
            }
          >
            <option value="">none</option>
            <option value="client_ip">client_ip</option>
            <option value="header">header</option>
            <option value="cookie">cookie</option>
            <option value="jwt_sub">jwt_sub</option>
          </select>
        </Field>
        <Field label="Hash key" hint="Required only for header/cookie hash.">
          <input
            className={inputClass}
            value={route.action.hashKey}
            onChange={(e) => setAction({ ...route.action, hashKey: e.target.value })}
            placeholder={route.action.hashPolicy === "cookie" ? "session" : "X-User"}
            disabled={route.action.hashPolicy !== "header" && route.action.hashPolicy !== "cookie"}
          />
        </Field>
      </div>

      <div className="grid gap-3 xl:grid-cols-2">
        <HeaderOperationsEditor
          title="Request header operations"
          description="set/add/remove are applied after proxy forwarding headers are prepared. Restricted hop-by-hop and X-Forwarded-* names are still rejected by validation."
          ops={route.action.requestHeaders}
          onChange={(next) => setAction({ ...route.action, requestHeaders: next })}
        />
        <HeaderOperationsEditor
          title="Response header operations"
          description="set/add/remove are applied on the upstream response before it is returned to the client."
          ops={route.action.responseHeaders}
          onChange={(next) => setAction({ ...route.action, responseHeaders: next })}
        />
      </div>
    </div>
  );
}

function HeaderOperationsEditor({
  title,
  description,
  ops,
  onChange,
}: {
  title: string;
  description: string;
  ops: ProxyRouteHeaderOperations;
  onChange: (next: ProxyRouteHeaderOperations) => void;
}) {
  return (
    <div className="rounded-xl border border-neutral-200 bg-white p-3 space-y-3">
      <div>
        <div className="font-medium text-sm">{title}</div>
        <div className="text-xs text-neutral-500">{description}</div>
      </div>
      <Field label="Set" hint="One `Header: value` pair per line.">
        <textarea
          className={textAreaClass}
          value={headerMapToMultiline(ops.set)}
          onChange={(e) =>
            onChange({
              ...ops,
              set: multilineToHeaderMap(e.target.value),
            })
          }
          spellCheck={false}
          placeholder={"X-Service: service-a\nCache-Control: no-store"}
        />
      </Field>
      <Field label="Add" hint="One `Header: value` pair per line.">
        <textarea
          className={textAreaClass}
          value={headerMapToMultiline(ops.add)}
          onChange={(e) =>
            onChange({
              ...ops,
              add: multilineToHeaderMap(e.target.value),
            })
          }
          spellCheck={false}
          placeholder="X-Route: service-a-prefix"
        />
      </Field>
      <Field label="Remove" hint="One header name per line.">
        <textarea
          className={textAreaClass}
          value={stringListToMultiline(ops.remove)}
          onChange={(e) =>
            onChange({
              ...ops,
              remove: multilineToStringList(e.target.value),
            })
          }
          spellCheck={false}
          placeholder={"X-Debug\nX-Internal-Trace"}
        />
      </Field>
    </div>
  );
}

function SectionCard({
  title,
  subtitle,
  actions,
  children,
}: {
  title: string;
  subtitle?: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-2xl border border-neutral-200 bg-white p-4 shadow-sm space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold">{title}</h2>
          {subtitle ? <p className="text-sm text-neutral-500">{subtitle}</p> : null}
        </div>
        {actions ? <div>{actions}</div> : null}
      </div>
      {children}
    </section>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="grid gap-1">
      <span className="text-sm font-medium">{label}</span>
      {children}
      {hint ? <span className="text-xs text-neutral-500">{hint}</span> : null}
    </label>
  );
}

function StatBox({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-neutral-200 bg-neutral-50 px-3 py-2">
      <div className="text-xs uppercase tracking-wide text-neutral-500">{label}</div>
      <div className="text-sm font-medium break-all">{value}</div>
    </div>
  );
}

function EmptyState({ children }: { children: React.ReactNode }) {
  return <div className="rounded-xl border border-dashed border-neutral-300 bg-neutral-50 px-4 py-6 text-sm text-neutral-500">{children}</div>;
}

function Badge({ color, children }: { color: "gray" | "green" | "red" | "amber"; children: React.ReactNode }) {
  const cls =
    color === "green"
      ? "bg-green-100 text-green-800"
      : color === "red"
        ? "bg-red-100 text-red-800"
        : color === "amber"
          ? "bg-amber-100 text-amber-800"
          : "bg-neutral-100 text-neutral-700";
  return <span className={`px-2 py-0.5 text-xs rounded ${cls}`}>{children}</span>;
}

function MonoTag({ label, value }: { label: string; value: string }) {
  return (
    <div className="hidden md:flex items-center gap-1 text-xs">
      <span className="text-neutral-500">{label}:</span>
      <code className="px-2 py-0.5 bg-neutral-100 rounded max-w-[420px] truncate">{value}</code>
    </div>
  );
}

function Alert({
  kind,
  title,
  message,
  onClose,
}: {
  kind: "error" | "warn";
  title: string;
  message: string;
  onClose: () => void;
}) {
  const classes = kind === "error" ? "border-red-200 bg-red-50 text-red-800" : "border-amber-200 bg-amber-50 text-amber-900";
  return (
    <div className={`rounded-xl border px-4 py-3 ${classes}`}>
      <div className="flex items-start justify-between gap-4">
        <div>
          <div className="font-medium">{title}</div>
          <div className="text-sm whitespace-pre-wrap">{message}</div>
        </div>
        <button type="button" className="text-sm underline" onClick={onClose}>
          close
        </button>
      </div>
    </div>
  );
}

function ActionButton({
  children,
  disabled,
  onClick,
}: {
  children: React.ReactNode;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button type="button" className="px-3 py-1.5 rounded-xl shadow text-sm hover:bg-neutral-50 border disabled:opacity-50" onClick={onClick} disabled={disabled}>
      {children}
    </button>
  );
}

function PrimaryButton({
  children,
  disabled,
  onClick,
}: {
  children: React.ReactNode;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button type="button" className="px-3 py-1.5 rounded-xl shadow text-sm bg-black text-white disabled:opacity-50" onClick={onClick} disabled={disabled}>
      {children}
    </button>
  );
}

const inputClass = "w-full rounded-xl border px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-black/20";
const textAreaClass = "w-full min-h-24 rounded-xl border px-3 py-2 font-mono text-sm outline-none focus:ring-2 focus:ring-black/20";
