package main

import (
	"fmt"
	"log"
	"os"
	"vendel/commands"
	"vendel/cronjobs"
	"vendel/handlers"
	"vendel/hooks"
	"vendel/middleware"
	"vendel/services"
	"vendel/ui"

	_ "vendel/migrations"

	"github.com/joho/godotenv"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	// Load .env file (from cwd or parent dir)
	_ = godotenv.Load()
	_ = godotenv.Load("../.env")

	app := pocketbase.New()

	// Enable auto-migration in dev mode
	isGoRun := os.Getenv("ENVIRONMENT") != "production"
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		Automigrate: isGoRun,
	})

	// ── Bootstrap services on serve ──────────────────────────────────
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		services.InitFCM(se.App)
		seedSuperuser(se.App)
		configureOAuth(se.App)
		configureSMTP(se.App)
		configureEmailTemplates(se.App)
		configureAuthSecurity(se.App)
		configureRateLimits(se.App)

		if os.Getenv("WEBHOOK_ENCRYPTION_KEY") == "" {
			return fmt.Errorf("WEBHOOK_ENCRYPTION_KEY environment variable is required")
		}

		// Custom API routes
		handlers.RegisterSMSRoutes(se)
		handlers.RegisterSMPPRoutes(se)
		handlers.RegisterPlanRoutes(se)
		handlers.RegisterUserWebhookRoutes(se)
		handlers.RegisterWebhookRoutes(se)
		handlers.RegisterApiKeyRoutes(se)
		handlers.RegisterContactRoutes(se)
		handlers.RegisterUtilRoutes(se)
		handlers.RegisterPromoRoutes(se)
		handlers.RegisterDeviceListRoutes(se)

		// Global middleware
		se.Router.BindFunc(middleware.SecurityHeadersMiddleware)
		se.Router.BindFunc(middleware.MaintenanceMiddleware(se.App))

		// Serve embedded frontend (SPA catch-all, must be last)
		se.Router.GET("/{path...}", apis.Static(ui.DistDirFS, true))

		return se.Next()
	})

	// ── Hooks ────────────────────────────────────────────────────────
	hooks.RegisterAuthHooks(app)
	hooks.RegisterUserHooks(app)
	hooks.RegisterDeviceHooks(app)
	hooks.RegisterSMPPHooks(app)
	hooks.RegisterWebhookHooks(app)
	hooks.RegisterApiKeyHooks(app)
	hooks.RegisterScheduledSMSHooks(app)
	hooks.RegisterEncryptionHooks(app)
	hooks.RegisterRealtimeHooks(app)

	// ── Cron jobs ────────────────────────────────────────────────────
	cronjobs.RegisterCronJobs(app)

	// ── CLI commands ─────────────────────────────────────────────────
	app.RootCmd.AddCommand(commands.NewUpdateCommand(version))

	// ── Start ────────────────────────────────────────────────────────
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
