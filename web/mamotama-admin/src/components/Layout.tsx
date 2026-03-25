import { useEffect, useState } from "react";
import { Link, Outlet, useLocation } from "react-router-dom";
import { getAPIBasePath, getAPIKey, setAPIKey } from "@/lib/api";

type NavItem = {
  to: string;
  label: string;
  hint: string;
};

const navItems: NavItem[] = [
  { to: "/status", label: "Status", hint: "runtime health" },
  { to: "/logs", label: "Logs", hint: "events and traces" },
  { to: "/rules", label: "Rules", hint: "base directives" },
  { to: "/rule-sets", label: "Rule Sets", hint: "CRS toggles" },
  { to: "/bypass", label: "Bypass Rules", hint: "path overrides" },
  { to: "/country-block", label: "Country Block", hint: "country deny list" },
  { to: "/rate-limit", label: "Rate Limit", hint: "traffic policies" },
  { to: "/bot-defense", label: "Bot Defense", hint: "ua challenge policy" },
  { to: "/semantic", label: "Semantic Security", hint: "heuristic anomaly scoring" },
  { to: "/fp-tuner", label: "FP Tuner", hint: "propose and apply exclusions" },
  { to: "/cache", label: "Cache Rules", hint: "edge cache behavior" },
  { to: "/proxy-rules", label: "Proxy Rules", hint: "upstream and transport tuning" },
];

function isActive(pathname: string, to: string) {
  return pathname === to || pathname.startsWith(`${to}/`);
}

export default function Layout() {
  const { pathname } = useLocation();
  const current = navItems.find((item) => isActive(pathname, item.to));
  const [apiKey, setApiKeyState] = useState(() => getAPIKey());

  useEffect(() => {
    setAPIKey(apiKey);
  }, [apiKey]);

  return (
    <div className="app-shell">
      <aside className="app-sidebar">
        <div className="app-brand">
          <p className="app-brand-tag">MAMOTAMA</p>
          <h1>Control Room</h1>
          <p className="app-brand-sub">Coraza + CRS Security Gateway</p>
        </div>

        <nav className="app-nav" aria-label="primary">
          {navItems.map((item) => {
            const active = isActive(pathname, item.to);
            return (
              <Link key={item.to} to={item.to} className={active ? "app-nav-link active" : "app-nav-link"}>
                <span className="app-nav-label">{item.label}</span>
                <span className="app-nav-hint">{item.hint}</span>
              </Link>
            );
          })}
        </nav>
      </aside>

      <main className="app-main">
        <header className="app-topbar">
          <div>
            <p className="app-kicker">Current Panel</p>
            <h2>{current?.label ?? "Dashboard"}</h2>
          </div>
          <div className="app-top-meta" style={{ alignItems: "flex-end" }}>
            <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
              <span style={{ fontSize: 11, color: "#6b7280" }}>X-API-Key</span>
              <input
                type="password"
                value={apiKey}
                onChange={(e) => setApiKeyState(e.target.value)}
                placeholder="paste admin API key"
                style={{
                  fontSize: 12,
                  padding: "6px 8px",
                  minWidth: 220,
                  borderRadius: 8,
                  border: "1px solid #d4d4d8",
                  background: "#fff",
                }}
              />
            </label>
            <span className="app-pill">Admin UI</span>
            <code>{getAPIBasePath()}</code>
            <code>{pathname}</code>
          </div>
        </header>

        <section className="app-content">
          <Outlet />
        </section>
      </main>
    </div>
  );
}
