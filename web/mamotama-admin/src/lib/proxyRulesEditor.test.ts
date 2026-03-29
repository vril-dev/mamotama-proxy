import assert from "node:assert/strict";
import test from "node:test";
import {
  createEmptyDefaultRoute,
  headerMapToMultiline,
  multilineToHeaderMap,
  multilineToStringList,
  parseProxyRulesEditor,
  serializeProxyRulesEditor,
  stringListToMultiline,
  type ProxyRulesRoutingEditorState,
} from "./proxyRulesEditor.js";

test("parseProxyRulesEditor reads route fields without dropping unrelated config", () => {
  const raw = `{
    "upstream_url": "http://legacy.internal:8080",
    "dial_timeout": 5,
    "upstreams": [
      {
        "name": "service-a",
        "url": "http://service-a.internal:8080",
        "weight": 2,
        "enabled": true
      }
    ],
    "routes": [
      {
        "name": "service-a-prefix",
        "priority": 10,
        "match": {
          "hosts": ["api.example.com", "*.example.net"],
          "path": { "type": "prefix", "value": "/servicea/" }
        },
        "action": {
          "upstream": "service-a",
          "canary_upstream": "http://canary.internal:8080",
          "canary_weight_percent": 15,
          "hash_policy": "header",
          "hash_key": "X-User",
          "host_rewrite": "service-a.internal",
          "path_rewrite": { "prefix": "/service-a/" },
          "request_headers": {
            "set": { "X-Service": "service-a" }
          },
          "response_headers": {
            "add": { "Cache-Control": "no-store" }
          }
        }
      }
    ],
    "default_route": {
      "name": "fallback",
      "action": {
        "upstream": "http://fallback.internal:8080"
      }
    }
  }`;

  const parsed = parseProxyRulesEditor(raw);
  assert.equal(parsed.base.dial_timeout, 5);
  assert.equal(parsed.state.upstreamURL, "http://legacy.internal:8080");
  assert.equal(parsed.state.upstreams.length, 1);
  assert.equal(parsed.state.routes.length, 1);
  assert.equal(parsed.state.routes[0]?.path?.type, "prefix");
  assert.equal(parsed.state.routes[0]?.action.hostRewrite, "service-a.internal");
  assert.equal(parsed.state.routes[0]?.action.canaryUpstream, "http://canary.internal:8080");
  assert.equal(parsed.state.routes[0]?.action.hashPolicy, "header");
  assert.equal(parsed.state.routes[0]?.action.hashKey, "X-User");
  assert.equal(parsed.state.routes[0]?.action.requestHeaders.set["X-Service"], "service-a");
  assert.equal(parsed.state.defaultRoute?.action.upstream, "http://fallback.internal:8080");
});

test("serializeProxyRulesEditor updates routing fields and preserves unrelated keys", () => {
  const initial = parseProxyRulesEditor(`{
    "dial_timeout": 5,
    "max_idle_conns": 10,
    "upstream_url": "http://legacy.internal:8080"
  }`);

  const nextState: ProxyRulesRoutingEditorState = {
    upstreamURL: "",
    upstreams: [
      {
        name: "service-a",
        url: "http://service-a.internal:8080",
        weight: 2,
        enabled: true,
      },
    ],
    routes: [
      {
        name: "service-a-prefix",
        enabled: true,
        priority: 10,
        hosts: ["api.example.com"],
        path: { type: "prefix", value: "/servicea/" },
        action: {
          upstream: "service-a",
          canaryUpstream: "service-a-canary",
          canaryWeightPercent: 20,
          hashPolicy: "cookie",
          hashKey: "session",
          hostRewrite: "",
          pathRewrite: { prefix: "/service-a/" },
          requestHeaders: {
            set: { "X-Service": "service-a" },
            add: {},
            remove: [],
          },
          responseHeaders: {
            set: {},
            add: { "Cache-Control": "no-store" },
            remove: [],
          },
        },
      },
    ],
    defaultRoute: createEmptyDefaultRoute(),
  };
  nextState.defaultRoute!.action.upstream = "http://fallback.internal:8080";

  const serialized = serializeProxyRulesEditor(initial.base, nextState);
  const reparsed = parseProxyRulesEditor(serialized);

  assert.equal(reparsed.base.dial_timeout, 5);
  assert.equal(reparsed.base.max_idle_conns, 10);
  assert.equal(reparsed.base.upstream_url, undefined);
  assert.equal(reparsed.state.upstreams[0]?.name, "service-a");
  assert.equal(reparsed.state.routes[0]?.action.canaryUpstream, "service-a-canary");
  assert.equal(reparsed.state.routes[0]?.action.hashPolicy, "cookie");
  assert.equal(reparsed.state.routes[0]?.action.hashKey, "session");
  assert.equal(reparsed.state.routes[0]?.action.pathRewrite?.prefix, "/service-a/");
  assert.equal(reparsed.state.routes[0]?.action.responseHeaders.add["Cache-Control"], "no-store");
  assert.equal(reparsed.state.defaultRoute?.action.upstream, "http://fallback.internal:8080");
});

test("serializeProxyRulesEditor removes empty routing sections", () => {
  const initial = parseProxyRulesEditor(`{
    "dial_timeout": 5,
    "routes": [{"name":"old","priority":1,"action":{"upstream":"http://old.internal"}}],
    "default_route": {"name":"default","action":{"upstream":"http://fallback.internal"}}
  }`);

  const serialized = serializeProxyRulesEditor(initial.base, {
    upstreamURL: "",
    upstreams: [],
    routes: [],
    defaultRoute: null,
  });
  const parsedJSON = JSON.parse(serialized) as Record<string, unknown>;

  assert.equal(parsedJSON.dial_timeout, 5);
  assert.equal(parsedJSON.routes, undefined);
  assert.equal(parsedJSON.default_route, undefined);
  assert.equal(parsedJSON.upstreams, undefined);
  assert.equal(parsedJSON.upstream_url, undefined);
});

test("multiline helpers preserve list and header operations", () => {
  assert.deepEqual(multilineToStringList("api.example.com\n\n*.example.net\n"), ["api.example.com", "*.example.net"]);
  assert.equal(stringListToMultiline(["a", "b"]), "a\nb");
  assert.deepEqual(multilineToHeaderMap("X-Test: one\nX-Trace\nX-Mode:  blue"), {
    "X-Test": "one",
    "X-Trace": "",
    "X-Mode": "blue",
  });
  assert.equal(headerMapToMultiline({ "X-Test": "one", "X-Trace": "" }), "X-Test: one\nX-Trace: ");
});
