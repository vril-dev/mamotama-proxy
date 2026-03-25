import { useMemo, useState } from "react";
import type { FormEvent } from "react";
import { apiGetJson, getAPIKey, setAPIKey } from "@/lib/api";

export default function SettingsPanel() {
  const [apiKeyInput, setApiKeyInput] = useState(() => getAPIKey());
  const [notice, setNotice] = useState<string>("");
  const [error, setError] = useState<string>("");
  const [checking, setChecking] = useState(false);

  const isConfigured = useMemo(() => apiKeyInput.trim().length > 0, [apiKeyInput]);

  function onSave(e: FormEvent) {
    e.preventDefault();
    setAPIKey(apiKeyInput);
    setError("");
    setNotice("Saved. This API key is now used for /mamotama-api requests.");
  }

  function onClear() {
    setApiKeyInput("");
    setAPIKey("");
    setError("");
    setNotice("Cleared.");
  }

  async function onVerify() {
    setChecking(true);
    setError("");
    setNotice("");

    try {
      setAPIKey(apiKeyInput);
      await apiGetJson("/status");
      setNotice("Verified. /status accepted this API key.");
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      setError(`Verification failed: ${message}`);
    } finally {
      setChecking(false);
    }
  }

  return (
    <div className="w-full p-4 space-y-4">
      <header className="flex items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">Settings</h1>
        <span className={`px-2 py-0.5 text-xs rounded ${isConfigured ? "bg-green-100 text-green-800" : "bg-amber-100 text-amber-800"}`}>
          API key {isConfigured ? "configured" : "not set"}
        </span>
      </header>

      <section className="rounded-xl border bg-white p-4 space-y-3">
        <p className="text-sm text-neutral-600">
          Manage the admin API key used in the browser. The value is stored in localStorage and sent as
          <code className="ml-1">X-API-Key</code>.
        </p>

        <form onSubmit={onSave} className="space-y-3">
          <div className="space-y-1">
            <label className="block text-xs text-neutral-600" htmlFor="api-key-input">
              X-API-Key
            </label>
            <input
              id="api-key-input"
              type="password"
              value={apiKeyInput}
              onChange={(e) => setApiKeyInput(e.target.value)}
              className="w-full rounded border px-3 py-2 text-sm bg-white"
              placeholder="paste admin API key"
            />
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <button type="submit">Save</button>
            <button type="button" onClick={onVerify} disabled={checking}>
              {checking ? "Verifying..." : "Save & Verify"}
            </button>
            <button type="button" onClick={onClear}>
              Clear
            </button>
          </div>
        </form>

        {notice ? (
          <div className="rounded border border-green-300 bg-green-50 px-3 py-2 text-xs text-green-900">{notice}</div>
        ) : null}
        {error ? (
          <div className="rounded border border-red-300 bg-red-50 px-3 py-2 text-xs text-red-900">{error}</div>
        ) : null}
      </section>
    </div>
  );
}
