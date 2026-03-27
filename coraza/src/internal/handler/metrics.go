package handler

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"mamotama/internal/config"
)

func MetricsHandler(c *gin.Context) {
	semantic := GetSemanticStats()
	rate := GetRateLimitStats()
	notify := GetNotificationStatus()
	ipReputation := IPReputationStatus()
	adminRate := AdminRateLimitStatsSnapshot()
	tlsStatus := ServerTLSRuntimeStatusSnapshot()
	_, _, _, proxyHealth, _ := ProxyRulesSnapshot()

	var b strings.Builder
	writePromCounter(&b, "mamotama_rate_limit_requests_total", rate.Requests)
	writePromCounter(&b, "mamotama_rate_limit_allowed_total", rate.Allowed)
	writePromCounter(&b, "mamotama_rate_limit_blocked_total", rate.Blocked)
	writePromCounter(&b, "mamotama_rate_limit_adaptive_total", rate.AdaptiveDecisions)
	writePromCounter(&b, "mamotama_semantic_inspected_requests_total", semantic.InspectedRequests)
	writePromCounter(&b, "mamotama_semantic_scored_requests_total", semantic.ScoredRequests)
	writePromCounter(&b, "mamotama_semantic_log_only_actions_total", semantic.LogOnlyActions)
	writePromCounter(&b, "mamotama_semantic_challenge_actions_total", semantic.ChallengeActions)
	writePromCounter(&b, "mamotama_semantic_block_actions_total", semantic.BlockActions)
	writePromCounter(&b, "mamotama_notifications_attempted_total", notify.Attempted)
	writePromCounter(&b, "mamotama_notifications_sent_total", notify.Sent)
	writePromCounter(&b, "mamotama_notifications_failed_total", notify.Failed)
	writePromGauge(&b, "mamotama_notifications_active_alerts", notify.ActiveAlerts)
	writePromGauge(&b, "mamotama_ip_reputation_effective_allow_count", ipReputation.EffectiveAllowCount)
	writePromGauge(&b, "mamotama_ip_reputation_effective_block_count", ipReputation.EffectiveBlockCount)
	writePromGauge(&b, "mamotama_ip_reputation_feed_allow_count", ipReputation.FeedAllowCount)
	writePromGauge(&b, "mamotama_ip_reputation_feed_block_count", ipReputation.FeedBlockCount)
	writePromCounter(&b, "mamotama_admin_rate_limit_requests_total", adminRate.Requests)
	writePromCounter(&b, "mamotama_admin_rate_limit_allowed_total", adminRate.Allowed)
	writePromCounter(&b, "mamotama_admin_rate_limit_blocked_total", adminRate.Blocked)
	writePromGauge(&b, "mamotama_server_tls_enabled", boolGauge(tlsStatus.Enabled))
	writePromGauge(&b, "mamotama_server_tls_source_manual", boolGauge(tlsStatus.Source == "manual"))
	writePromGauge(&b, "mamotama_server_tls_source_acme", boolGauge(tlsStatus.Source == "acme"))
	writePromGauge(&b, "mamotama_server_tls_acme_enabled", boolGauge(config.ServerTLSACMEEnabled))
	writePromGauge(&b, "mamotama_server_tls_cert_not_after_unix", optionalUnixGauge(tlsStatus.CertNotAfter))
	writePromCounter(&b, "mamotama_server_tls_acme_success_total", tlsStatus.ACMESuccessTotal)
	writePromCounter(&b, "mamotama_server_tls_acme_failure_total", tlsStatus.ACMEFailureTotal)
	writePromGauge(&b, "mamotama_upstream_active_backends", proxyHealth.ActiveBackends)
	writePromGauge(&b, "mamotama_upstream_healthy_backends", proxyHealth.HealthyBackends)

	c.Data(200, "text/plain; version=0.0.4; charset=utf-8", []byte(b.String()))
}

func writePromCounter(b *strings.Builder, name string, value uint64) {
	fmt.Fprintf(b, "# TYPE %s counter\n%s %d\n", name, name, value)
}

func writePromGauge(b *strings.Builder, name string, value int) {
	fmt.Fprintf(b, "# TYPE %s gauge\n%s %d\n", name, name, value)
}

func boolGauge(v bool) int {
	if v {
		return 1
	}
	return 0
}

func optionalUnixGauge(ts string) int {
	if strings.TrimSpace(ts) == "" {
		return 0
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	return int(parsed.Unix())
}
