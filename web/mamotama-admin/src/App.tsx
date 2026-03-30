import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import Layout from './components/Layout';
import Status from './pages/Status';
import Logs from './pages/Logs';
import Rules from './pages/Rules';
import RuleSets from './pages/RuleSets';
import CacheRulePanel from './pages/CacheRulesPanel';
import BypassRulesPanel from './pages/BypassRulesPanel';
import CountryBlockPanel from './pages/CountryBlockPanel';
import RateLimitPanel from './pages/RateLimitPanel';
import IPReputationPanel from './pages/IPReputationPanel';
import NotificationsPanel from './pages/NotificationsPanel';
import BotDefensePanel from './pages/BotDefensePanel';
import SemanticPanel from './pages/SemanticPanel';
import FPTunerPanel from './pages/FPTunerPanel';
import ProxyRulesPanel from './pages/ProxyRulesPanel';
import SettingsPanel from './pages/SettingsPanel';

function App() {
  return (
    <BrowserRouter basename="/mamotama-ui">
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Navigate to="/status" />} />
          <Route path="status" element={<Status />} />
          <Route path="logs" element={<Logs />} />
          <Route path="rules" element={<Rules />} />
          <Route path="rule-sets" element={<RuleSets />} />
          <Route path="bypass" element={<BypassRulesPanel />} />
          <Route path="country-block" element={<CountryBlockPanel />} />
          <Route path="rate-limit" element={<RateLimitPanel />} />
          <Route path="ip-reputation" element={<IPReputationPanel />} />
          <Route path="notifications" element={<NotificationsPanel />} />
          <Route path="bot-defense" element={<BotDefensePanel />} />
          <Route path="semantic" element={<SemanticPanel />} />
          <Route path="fp-tuner" element={<FPTunerPanel />} />
          <Route path="cache" element={<CacheRulePanel />} />
          <Route path="proxy-rules" element={<ProxyRulesPanel />} />
          <Route path="settings" element={<SettingsPanel />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

export default App;
