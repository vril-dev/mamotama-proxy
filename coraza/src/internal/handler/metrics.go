package handler

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
)

func MetricsHandler(c *gin.Context) {
	semantic := GetSemanticStats()
	rate := GetRateLimitStats()
	notify := GetNotificationStatus()

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

	c.Data(200, "text/plain; version=0.0.4; charset=utf-8", []byte(b.String()))
}

func writePromCounter(b *strings.Builder, name string, value uint64) {
	fmt.Fprintf(b, "# TYPE %s counter\n%s %d\n", name, name, value)
}

func writePromGauge(b *strings.Builder, name string, value int) {
	fmt.Fprintf(b, "# TYPE %s gauge\n%s %d\n", name, name, value)
}
