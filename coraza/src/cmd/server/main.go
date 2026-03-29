package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"mamotama/internal/cacheconf"
	"mamotama/internal/config"
	"mamotama/internal/handler"
	"mamotama/internal/middleware"
	"mamotama/internal/observability"
	"mamotama/internal/waf"
)

func main() {
	config.LoadEnv()
	if config.RuntimeGOMAXPROCS > 0 {
		prev := runtime.GOMAXPROCS(config.RuntimeGOMAXPROCS)
		log.Printf("[RUNTIME] GOMAXPROCS set to %d (previous=%d)", config.RuntimeGOMAXPROCS, prev)
	}
	if config.RuntimeMemoryLimitMB > 0 {
		limitBytes := int64(config.RuntimeMemoryLimitMB) * 1024 * 1024
		prev := debug.SetMemoryLimit(limitBytes)
		log.Printf("[RUNTIME] memory limit set to %d MB (previous=%d MB)", config.RuntimeMemoryLimitMB, prev/(1024*1024))
	}
	if err := handler.InitProxyRuntime(config.ProxyConfigFile, config.ProxyRollbackMax); err != nil {
		log.Fatalf("[FATAL] failed to initialize proxy runtime: %v", err)
	}
	if err := handler.InitLogsStatsStoreWithBackend(
		config.StorageBackend,
		config.DBDriver,
		config.DBPath,
		config.DBDSN,
		config.DBRetentionDays,
	); err != nil {
		log.Printf("[DB][INIT][WARN] failed to initialize db store (fallback=file): %v", err)
	} else if config.DBEnabled {
		log.Printf("[DB][INIT] db store enabled (backend=%s driver=%s path=%s retention_days=%d)", config.StorageBackend, config.DBDriver, config.DBPath, config.DBRetentionDays)
	} else {
		log.Printf("[DB][INIT] storage backend=%s", config.StorageBackend)
	}
	if err := handler.SyncProxyStorage(); err != nil {
		log.Printf("[PROXY][DB][WARN] sync failed (fallback=file): %v", err)
	}
	if err := handler.SyncRuleFilesStorage(); err != nil {
		log.Printf("[RULES][DB][WARN] sync failed (fallback=file): %v", err)
	}
	waf.InitWAF()
	if err := handler.SyncCRSDisabledStorage(); err != nil {
		log.Printf("[CRS][DB][WARN] sync failed (fallback=file): %v", err)
	}
	if err := handler.SyncBypassStorage(); err != nil {
		log.Printf("[BYPASS][DB][WARN] sync failed (fallback=file): %v", err)
	}
	if err := handler.InitCountryBlock(config.CountryBlockFile); err != nil {
		log.Printf("[COUNTRY_BLOCK][INIT][ERR] %v (path=%s)", err, config.CountryBlockFile)
	} else {
		if err := handler.SyncCountryBlockStorage(); err != nil {
			log.Printf("[COUNTRY_BLOCK][DB][WARN] sync failed (fallback=file): %v", err)
		}
		log.Printf("[COUNTRY_BLOCK][INIT] loaded %d countries", len(handler.GetBlockedCountries()))
	}
	if err := handler.InitRateLimit(config.RateLimitFile); err != nil {
		log.Printf("[RATE_LIMIT][INIT][ERR] %v (path=%s)", err, config.RateLimitFile)
	} else {
		if err := handler.SyncRateLimitStorage(); err != nil {
			log.Printf("[RATE_LIMIT][DB][WARN] sync failed (fallback=file): %v", err)
		}
		log.Printf("[RATE_LIMIT][INIT] loaded")
	}
	if err := handler.InitBotDefense(config.BotDefenseFile); err != nil {
		log.Printf("[BOT_DEFENSE][INIT][ERR] %v (path=%s)", err, config.BotDefenseFile)
	} else {
		if err := handler.SyncBotDefenseStorage(); err != nil {
			log.Printf("[BOT_DEFENSE][DB][WARN] sync failed (fallback=file): %v", err)
		}
		log.Printf("[BOT_DEFENSE][INIT] loaded")
	}
	if err := handler.InitSemantic(config.SemanticFile); err != nil {
		log.Printf("[SEMANTIC][INIT][ERR] %v (path=%s)", err, config.SemanticFile)
	} else {
		if err := handler.SyncSemanticStorage(); err != nil {
			log.Printf("[SEMANTIC][DB][WARN] sync failed (fallback=file): %v", err)
		}
		log.Printf("[SEMANTIC][INIT] loaded")
	}
	handler.SetNotificationProductLabel("proxy")
	if err := handler.InitNotifications(config.NotificationFile); err != nil {
		log.Printf("[NOTIFY][INIT][ERR] %v (path=%s)", err, config.NotificationFile)
	} else {
		if err := handler.SyncNotificationStorage(); err != nil {
			log.Printf("[NOTIFY][DB][WARN] sync failed (fallback=file): %v", err)
		}
		log.Printf("[NOTIFY][INIT] loaded")
	}
	if err := handler.InitIPReputation(config.IPReputationFile); err != nil {
		log.Printf("[IP_REPUTATION][INIT][ERR] %v (path=%s)", err, config.IPReputationFile)
	} else {
		if err := handler.SyncIPReputationStorage(); err != nil {
			log.Printf("[IP_REPUTATION][DB][WARN] sync failed (fallback=file): %v", err)
		}
		log.Printf("[IP_REPUTATION][INIT] loaded")
	}
	if err := handler.InitAdminGuards(); err != nil {
		log.Fatalf("[ADMIN][FATAL] failed to initialize admin guards: %v", err)
	}
	shutdownTracing, err := observability.SetupTracing(context.Background(), observability.TracingConfig{
		Enabled:      config.TracingEnabled,
		ServiceName:  config.TracingServiceName,
		OTLPEndpoint: config.TracingOTLPEndpoint,
		Insecure:     config.TracingInsecure,
		SampleRatio:  config.TracingSampleRatio,
	})
	if err != nil {
		log.Fatalf("[TRACING][FATAL] initialize tracing: %v", err)
	}
	defer func() {
		if err := shutdownTracing(context.Background()); err != nil {
			log.Printf("[TRACING][WARN] shutdown tracing: %v", err)
		}
	}()

	_, _, proxyCfg, _, _ := handler.ProxyRulesSnapshot()
	log.Println("[INFO] WAF upstream target:", proxyCfg.UpstreamURL)

	r := gin.Default()
	r.Use(observability.GinTracingMiddleware())

	// Never trust client-sent forwarding headers unless explicitly configured.
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Fatalf("failed to configure trusted proxies: %v", err)
	}

	// Lightweight unauthenticated probe for container health checks.
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	if config.ServerMaxConcurrentReqs > 0 {
		r.Use(middleware.ConcurrencyLimit(config.ServerMaxConcurrentReqs, "global"))
		log.Printf("[SERVER] global concurrency guard enabled max=%d", config.ServerMaxConcurrentReqs)
	}
	proxyConcurrencyGuard := middleware.NewConcurrencyGuard(config.ServerMaxConcurrentProxy, "proxy")
	if proxyConcurrencyGuard != nil {
		log.Printf("[SERVER] proxy concurrency guard enabled max=%d", config.ServerMaxConcurrentProxy)
	}

	if len(config.APICORSOrigins) > 0 {
		r.Use(cors.New(cors.Config{
			AllowOrigins: config.APICORSOrigins,
			AllowMethods: []string{"GET", "POST", "PUT", "OPTIONS"},
			AllowHeaders: []string{"Origin", "Content-Type", "Accept", "X-API-Key", "If-Match"},
		}))
		log.Printf("[SECURITY] CORS enabled for origins: %s", strings.Join(config.APICORSOrigins, ","))
	} else {
		log.Println("[SECURITY] CORS disabled (same-origin only)")
	}

	api := r.Group(
		config.APIBasePath,
		handler.AdminAccessMiddleware("api"),
		handler.AdminRateLimitMiddleware(),
		middleware.APIKeyAuth(),
	)
	{
		api.GET("/", func(c *gin.Context) {
			c.JSON(200, gin.H{
				"message": "mamotama-admin API",
				"endpoints": []string{
					config.APIBasePath + "/status",
					config.APIBasePath + "/logs",
					config.APIBasePath + "/rules",
					config.APIBasePath + "/crs-rule-sets",
					config.APIBasePath + "/bypass-rules",
					config.APIBasePath + "/cache-rules",
					config.APIBasePath + "/cache-store",
					config.APIBasePath + "/cache-store:clear",
					config.APIBasePath + "/country-block-rules",
					config.APIBasePath + "/rate-limit-rules",
					config.APIBasePath + "/notifications",
					config.APIBasePath + "/notifications/status",
					config.APIBasePath + "/ip-reputation",
					config.APIBasePath + "/ip-reputation:validate",
					config.APIBasePath + "/bot-defense-rules",
					config.APIBasePath + "/semantic-rules",
					config.APIBasePath + "/proxy-rules",
					config.APIBasePath + "/proxy-rules:validate",
					config.APIBasePath + "/proxy-rules:probe",
					config.APIBasePath + "/proxy-rules:rollback",
					config.APIBasePath + "/fp-tuner/propose",
					config.APIBasePath + "/fp-tuner/apply",
					config.APIBasePath + "/logs/read",
					config.APIBasePath + "/logs/stats",
					config.APIBasePath + "/logs/download",
					config.APIBasePath + "/metrics",
				},
			})
		})

		api.GET("/status", handler.StatusHandler)
		api.GET("/metrics", handler.MetricsHandler)
		api.GET("/logs/read", handler.LogsRead)
		api.GET("/logs/stats", handler.LogsStats)
		api.GET("/logs/download", handler.LogsDownload)
		api.GET("/rules", handler.RulesHandler)
		api.POST("/rules:validate", handler.ValidateRules)
		api.PUT("/rules", handler.PutRules)
		api.GET("/crs-rule-sets", handler.GetCRSRuleSets)
		api.POST("/crs-rule-sets:validate", handler.ValidateCRSRuleSets)
		api.PUT("/crs-rule-sets", handler.PutCRSRuleSets)
		api.GET("/bypass-rules", handler.GetBypassRules)
		api.POST("/bypass-rules:validate", handler.ValidateBypassRules)
		api.PUT("/bypass-rules", handler.PutBypassRules)
		api.GET("/cache-rules", handler.GetCacheRules)
		api.POST("/cache-rules:validate", handler.ValidateCacheRules)
		api.PUT("/cache-rules", handler.PutCacheRules)
		api.GET("/cache-store", handler.GetResponseCacheStore)
		api.POST("/cache-store:validate", handler.ValidateResponseCacheStore)
		api.PUT("/cache-store", handler.PutResponseCacheStore)
		api.POST("/cache-store:clear", handler.ClearResponseCacheStore)
		api.GET("/country-block-rules", handler.GetCountryBlockRules)
		api.POST("/country-block-rules:validate", handler.ValidateCountryBlockRules)
		api.PUT("/country-block-rules", handler.PutCountryBlockRules)
		api.GET("/rate-limit-rules", handler.GetRateLimitRules)
		api.POST("/rate-limit-rules:validate", handler.ValidateRateLimitRules)
		api.PUT("/rate-limit-rules", handler.PutRateLimitRules)
		api.GET("/notifications", handler.GetNotificationRules)
		api.GET("/notifications/status", handler.GetNotificationStatusHandler)
		api.POST("/notifications/validate", handler.ValidateNotificationRules)
		api.POST("/notifications/test", handler.TestNotificationRules)
		api.PUT("/notifications", handler.PutNotificationRules)
		api.GET("/ip-reputation", handler.GetIPReputation)
		api.POST("/ip-reputation:validate", handler.ValidateIPReputation)
		api.PUT("/ip-reputation", handler.PutIPReputation)
		api.GET("/bot-defense-rules", handler.GetBotDefenseRules)
		api.POST("/bot-defense-rules:validate", handler.ValidateBotDefenseRules)
		api.PUT("/bot-defense-rules", handler.PutBotDefenseRules)
		api.GET("/semantic-rules", handler.GetSemanticRules)
		api.POST("/semantic-rules:validate", handler.ValidateSemanticRules)
		api.PUT("/semantic-rules", handler.PutSemanticRules)
		api.GET("/proxy-rules", handler.GetProxyRules)
		api.POST("/proxy-rules:action", handler.ProxyRulesAction)
		api.PUT("/proxy-rules", handler.PutProxyRules)
		api.POST("/fp-tuner/propose", handler.ProposeFPTuning)
		api.POST("/fp-tuner/apply", handler.ApplyFPTuning)
	}

	handler.RegisterAdminUIRoutes(r)
	if err := handler.InitResponseCacheRuntime(config.CacheStoreFile); err != nil {
		log.Fatalf("[CACHE][FATAL] failed to initialize response cache runtime: %v", err)
	}
	if err := handler.SyncResponseCacheStoreStorage(); err != nil {
		log.Printf("[CACHE][DB][WARN] sync failed (fallback=file): %v", err)
	}

	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, config.APIBasePath) {
			c.AbortWithStatus(404)
			return
		}
		if proxyConcurrencyGuard != nil && !proxyConcurrencyGuard.Acquire() {
			proxyConcurrencyGuard.Reject(c)
			return
		}
		if proxyConcurrencyGuard != nil {
			defer proxyConcurrencyGuard.Release()
		}

		handler.ProxyHandler(c)
	})

	const cacheConfPath = "conf/cache.conf"
	if err := handler.SyncCacheRulesStorage(); err != nil {
		log.Printf("[CACHE][DB][WARN] sync failed (fallback=file): %v", err)
	}
	if config.DBEnabled && config.DBSyncInterval > 0 {
		handler.StartStorageSyncLoop(config.DBSyncInterval)
		log.Printf("[DB][SYNC] periodic sync loop enabled interval=%s", config.DBSyncInterval)
	}
	stopWatch, err := cacheconf.Watch(cacheConfPath, func(rs *cacheconf.Ruleset) {
		//
	})
	if err != nil {
		log.Printf("[CACHE] watch disabled: %v", err)
	} else {
		defer stopWatch()
	}

	srv := &http.Server{
		Addr:              config.ListenAddr,
		Handler:           r,
		ReadTimeout:       config.ServerReadTimeout,
		ReadHeaderTimeout: config.ServerReadHeaderTimeout,
		WriteTimeout:      config.ServerWriteTimeout,
		IdleTimeout:       config.ServerIdleTimeout,
		MaxHeaderBytes:    config.ServerMaxHeaderBytes,
	}

	if config.ServerTLSEnabled {
		tlsConfig, redirectSrv, err := buildManagedServerTLSConfig()
		if err != nil {
			log.Fatalf("[FATAL] build server tls config: %v", err)
		}
		srv.TLSConfig = tlsConfig
		if redirectSrv != nil {
			go func() {
				log.Printf("[INFO] starting HTTP redirect server on %s", config.ServerTLSHTTPRedirectAddr)
				if err := redirectSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Fatalf("[FATAL] redirect server stopped: %v", err)
				}
			}()
		}
		log.Printf("[INFO] starting HTTPS server on %s", config.ListenAddr)
		log.Printf("[SERVER] tls enabled source=%s cert_file=%s acme_domains=%s min_version=%s redirect_http=%t redirect_addr=%s",
			handler.ServerTLSRuntimeStatusSnapshot().Source,
			config.ServerTLSCertFile,
			strings.Join(config.ServerTLSACMEDomains, ","),
			config.ServerTLSMinVersion,
			config.ServerTLSRedirectHTTP,
			config.ServerTLSHTTPRedirectAddr,
		)
		log.Printf("[SERVER] read_timeout=%s read_header_timeout=%s write_timeout=%s idle_timeout=%s max_header_bytes=%d",
			config.ServerReadTimeout,
			config.ServerReadHeaderTimeout,
			config.ServerWriteTimeout,
			config.ServerIdleTimeout,
			config.ServerMaxHeaderBytes,
		)
		if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[FATAL] server stopped: %v", err)
		}
		return
	}

	log.Printf("[INFO] starting server on %s", config.ListenAddr)
	log.Printf("[SERVER] read_timeout=%s read_header_timeout=%s write_timeout=%s idle_timeout=%s max_header_bytes=%d",
		config.ServerReadTimeout,
		config.ServerReadHeaderTimeout,
		config.ServerWriteTimeout,
		config.ServerIdleTimeout,
		config.ServerMaxHeaderBytes,
	)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("[FATAL] server stopped: %v", err)
	}
}
