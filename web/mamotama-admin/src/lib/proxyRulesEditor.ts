type JSONRecord = Record<string, unknown>;

export type ProxyRouteHeaderOperations = {
  set: Record<string, string>;
  add: Record<string, string>;
  remove: string[];
};

export type ProxyRoutePathMatch = {
  type: "" | "exact" | "prefix" | "regex";
  value: string;
};

export type ProxyRoutePathRewrite = {
  prefix: string;
};

export type ProxyRouteAction = {
  upstream: string;
  hostRewrite: string;
  pathRewrite: ProxyRoutePathRewrite | null;
  requestHeaders: ProxyRouteHeaderOperations;
  responseHeaders: ProxyRouteHeaderOperations;
};

export type ProxyRoute = {
  name: string;
  enabled: boolean;
  priority: number;
  hosts: string[];
  path: ProxyRoutePathMatch | null;
  action: ProxyRouteAction;
};

export type ProxyUpstream = {
  name: string;
  url: string;
  weight: number;
  enabled: boolean;
};

export type ProxyRulesRoutingEditorState = {
  upstreamURL: string;
  upstreams: ProxyUpstream[];
  routes: ProxyRoute[];
  defaultRoute: ProxyRoute | null;
};

export type ParsedProxyRulesEditor = {
  base: JSONRecord;
  state: ProxyRulesRoutingEditorState;
};

export function createEmptyHeaderOperations(): ProxyRouteHeaderOperations {
  return {
    set: {},
    add: {},
    remove: [],
  };
}

export function createEmptyRouteAction(): ProxyRouteAction {
  return {
    upstream: "",
    hostRewrite: "",
    pathRewrite: null,
    requestHeaders: createEmptyHeaderOperations(),
    responseHeaders: createEmptyHeaderOperations(),
  };
}

export function createEmptyRoute(seed = 1): ProxyRoute {
  return {
    name: `route-${seed}`,
    enabled: true,
    priority: seed * 10,
    hosts: [],
    path: null,
    action: createEmptyRouteAction(),
  };
}

export function createEmptyDefaultRoute(): ProxyRoute {
  return {
    name: "default",
    enabled: true,
    priority: 0,
    hosts: [],
    path: null,
    action: createEmptyRouteAction(),
  };
}

export function createEmptyUpstream(seed = 1): ProxyUpstream {
  return {
    name: `upstream-${seed}`,
    url: "",
    weight: 1,
    enabled: true,
  };
}

export function stringListToMultiline(values: string[]): string {
  return values.join("\n");
}

export function multilineToStringList(value: string): string[] {
  return value
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean);
}

export function headerMapToMultiline(values: Record<string, string>): string {
  return Object.entries(values)
    .map(([key, value]) => `${key}: ${value}`)
    .join("\n");
}

export function multilineToHeaderMap(value: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const rawLine of value.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line) {
      continue;
    }
    const idx = line.indexOf(":");
    if (idx < 0) {
      out[line] = "";
      continue;
    }
    const key = line.slice(0, idx).trim();
    if (!key) {
      continue;
    }
    out[key] = line.slice(idx + 1).trimStart();
  }
  return out;
}

export function parseProxyRulesEditor(raw: string): ParsedProxyRulesEditor {
  const trimmed = raw.trim();
  const parsed = trimmed ? JSON.parse(trimmed) : {};
  if (!isRecord(parsed)) {
    throw new Error("proxy rules must be a JSON object");
  }
  const base = deepCloneRecord(parsed);
  return {
    base,
    state: {
      upstreamURL: readString(base.upstream_url),
      upstreams: readUpstreams(base.upstreams),
      routes: readRoutes(base.routes),
      defaultRoute: readDefaultRoute(base.default_route),
    },
  };
}

export function serializeProxyRulesEditor(base: JSONRecord, state: ProxyRulesRoutingEditorState): string {
  const next = deepCloneRecord(base);

  if (state.upstreamURL.trim()) {
    next.upstream_url = state.upstreamURL.trim();
  } else {
    delete next.upstream_url;
  }

  const upstreams = state.upstreams
    .map(writeUpstream)
    .filter((item): item is JSONRecord => item !== null);
  if (upstreams.length > 0) {
    next.upstreams = upstreams;
  } else {
    delete next.upstreams;
  }

  const routes = state.routes
    .map(writeRoute)
    .filter((item): item is JSONRecord => item !== null);
  if (routes.length > 0) {
    next.routes = routes;
  } else {
    delete next.routes;
  }

  const defaultRoute = state.defaultRoute ? writeDefaultRoute(state.defaultRoute) : null;
  if (defaultRoute) {
    next.default_route = defaultRoute;
  } else {
    delete next.default_route;
  }

  return `${JSON.stringify(next, null, 2)}\n`;
}

function readUpstreams(value: unknown): ProxyUpstream[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value
    .map((item, index) => {
      if (!isRecord(item)) {
        return null;
      }
      return {
        name: readString(item.name) || `upstream-${index + 1}`,
        url: readString(item.url),
        weight: normalizePositiveInt(item.weight, 1),
        enabled: readBoolean(item.enabled, true),
      } satisfies ProxyUpstream;
    })
    .filter((item): item is ProxyUpstream => item !== null);
}

function readRoutes(value: unknown): ProxyRoute[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value
    .map((item, index) => readRoute(item, `route-${index + 1}`))
    .filter((item): item is ProxyRoute => item !== null);
}

function readDefaultRoute(value: unknown): ProxyRoute | null {
  if (!isRecord(value)) {
    return null;
  }
  return readRoute(value, "default");
}

function readRoute(value: unknown, fallbackName: string): ProxyRoute | null {
  if (!isRecord(value)) {
    return null;
  }
  const match = isRecord(value.match) ? value.match : null;
  const path = match && isRecord(match.path) ? match.path : null;
  const action = isRecord(value.action) ? value.action : null;
  return {
    name: readString(value.name) || fallbackName,
    enabled: readBoolean(value.enabled, true),
    priority: normalizeInt(value.priority, 0),
    hosts: readStringList(match?.hosts),
    path: readPathMatch(path),
    action: readRouteAction(action),
  };
}

function readPathMatch(value: JSONRecord | null): ProxyRoutePathMatch | null {
  if (!value) {
    return null;
  }
  const type = readString(value.type);
  const normalizedType = type === "exact" || type === "prefix" || type === "regex" ? type : "";
  const matchValue = readString(value.value);
  if (!normalizedType && !matchValue) {
    return null;
  }
  return {
    type: normalizedType,
    value: matchValue,
  };
}

function readRouteAction(value: JSONRecord | null): ProxyRouteAction {
  if (!value) {
    return createEmptyRouteAction();
  }
  return {
    upstream: readString(value.upstream),
    hostRewrite: readString(value.host_rewrite),
    pathRewrite: readPathRewrite(value.path_rewrite),
    requestHeaders: readHeaderOperations(value.request_headers),
    responseHeaders: readHeaderOperations(value.response_headers),
  };
}

function readPathRewrite(value: unknown): ProxyRoutePathRewrite | null {
  if (!isRecord(value)) {
    return null;
  }
  const prefix = readString(value.prefix);
  if (!prefix) {
    return null;
  }
  return { prefix };
}

function readHeaderOperations(value: unknown): ProxyRouteHeaderOperations {
  if (!isRecord(value)) {
    return createEmptyHeaderOperations();
  }
  return {
    set: readStringMap(value.set),
    add: readStringMap(value.add),
    remove: readStringList(value.remove),
  };
}

function writeUpstream(upstream: ProxyUpstream): JSONRecord | null {
  if (!upstream.name.trim() && !upstream.url.trim()) {
    return null;
  }
  return {
    name: upstream.name.trim(),
    url: upstream.url.trim(),
    weight: normalizePositiveInt(upstream.weight, 1),
    enabled: upstream.enabled,
  };
}

function writeRoute(route: ProxyRoute): JSONRecord | null {
  if (!route.name.trim() && !route.action.upstream.trim() && route.hosts.length === 0 && !route.path) {
    return null;
  }
  const out: JSONRecord = {
    name: route.name.trim(),
    priority: normalizeInt(route.priority, 0),
    action: writeRouteAction(route.action),
  };
  if (!route.enabled) {
    out.enabled = false;
  }
  const match = writeRouteMatch(route);
  if (match) {
    out.match = match;
  }
  return out;
}

function writeDefaultRoute(route: ProxyRoute): JSONRecord | null {
  const action = writeRouteAction(route.action);
  if (Object.keys(action).length === 0) {
    return null;
  }
  const out: JSONRecord = {
    name: route.name.trim() || "default",
    action,
  };
  if (!route.enabled) {
    out.enabled = false;
  }
  return out;
}

function writeRouteMatch(route: ProxyRoute): JSONRecord | null {
  const match: JSONRecord = {};
  const hosts = route.hosts.map((item) => item.trim()).filter(Boolean);
  if (hosts.length > 0) {
    match.hosts = hosts;
  }
  if (route.path && route.path.type && route.path.value.trim()) {
    match.path = {
      type: route.path.type,
      value: route.path.value.trim(),
    };
  }
  return Object.keys(match).length > 0 ? match : null;
}

function writeRouteAction(action: ProxyRouteAction): JSONRecord {
  const out: JSONRecord = {};
  if (action.upstream.trim()) {
    out.upstream = action.upstream.trim();
  }
  if (action.hostRewrite.trim()) {
    out.host_rewrite = action.hostRewrite.trim();
  }
  if (action.pathRewrite && action.pathRewrite.prefix.trim()) {
    out.path_rewrite = { prefix: action.pathRewrite.prefix.trim() };
  }
  const requestHeaders = writeHeaderOperations(action.requestHeaders);
  if (requestHeaders) {
    out.request_headers = requestHeaders;
  }
  const responseHeaders = writeHeaderOperations(action.responseHeaders);
  if (responseHeaders) {
    out.response_headers = responseHeaders;
  }
  return out;
}

function writeHeaderOperations(ops: ProxyRouteHeaderOperations): JSONRecord | null {
  const out: JSONRecord = {};
  const set = writeStringMap(ops.set);
  if (set) {
    out.set = set;
  }
  const add = writeStringMap(ops.add);
  if (add) {
    out.add = add;
  }
  const remove = ops.remove.map((item) => item.trim()).filter(Boolean);
  if (remove.length > 0) {
    out.remove = remove;
  }
  return Object.keys(out).length > 0 ? out : null;
}

function writeStringMap(value: Record<string, string>): JSONRecord | null {
  const out: JSONRecord = {};
  for (const [key, entry] of Object.entries(value)) {
    const nextKey = key.trim();
    if (!nextKey) {
      continue;
    }
    out[nextKey] = entry;
  }
  return Object.keys(out).length > 0 ? out : null;
}

function readString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function readBoolean(value: unknown, fallback: boolean): boolean {
  return typeof value === "boolean" ? value : fallback;
}

function normalizeInt(value: unknown, fallback: number): number {
  return typeof value === "number" && Number.isFinite(value) ? Math.trunc(value) : fallback;
}

function normalizePositiveInt(value: unknown, fallback: number): number {
  const normalized = normalizeInt(value, fallback);
  return normalized > 0 ? normalized : fallback;
}

function readStringList(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value
    .map((item) => (typeof item === "string" ? item : ""))
    .map((item) => item.trim())
    .filter(Boolean);
}

function readStringMap(value: unknown): Record<string, string> {
  if (!isRecord(value)) {
    return {};
  }
  const out: Record<string, string> = {};
  for (const [key, entry] of Object.entries(value)) {
    if (typeof entry !== "string") {
      continue;
    }
    const nextKey = key.trim();
    if (!nextKey) {
      continue;
    }
    out[nextKey] = entry;
  }
  return out;
}

function isRecord(value: unknown): value is JSONRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function deepCloneRecord<T extends JSONRecord>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}
