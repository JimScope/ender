package handlers

import (
	"net/http"
	"strings"
	"vendel/middleware"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// RegisterDeviceListRoutes registers read-only device listing routes for
// external clients (vk_ user keys or JWT). It does NOT replace the
// PocketBase REST collection endpoints used by the dashboard.
func RegisterDeviceListRoutes(se *core.ServeEvent) {
	// GET /api/devices — list user's devices (JWT or API key auth)
	se.Router.GET("/api/devices", func(e *core.RequestEvent) error {
		userId, err := middleware.ResolveAuthOrAPIKey(e)
		if err != nil {
			return apis.NewUnauthorizedError("Authentication required", nil)
		}

		page, perPage := parsePagination(e)
		offset := (page - 1) * perPage

		filter := "user = {:userId}"
		params := dbx.Params{"userId": userId}
		// CountRecords takes raw SQL expressions, not PocketBase filter
		// syntax — build both in parallel so total_items stays accurate.
		countExprs := []dbx.Expression{dbx.HashExp{"user": userId}}

		// device_type acts as a coarse filter; the collection has no
		// dedicated `status` field, but we accept the param for forward
		// compatibility and ignore it when empty.
		if deviceType := strings.TrimSpace(e.Request.URL.Query().Get("device_type")); deviceType != "" {
			filter += " && device_type = {:deviceType}"
			params["deviceType"] = deviceType
			countExprs = append(countExprs, dbx.HashExp{"device_type": deviceType})
		}

		records, err := e.App.FindRecordsByFilter(
			"sms_devices",
			filter,
			"-created",
			perPage,
			offset,
			params,
		)
		if err != nil {
			records = []*core.Record{}
		}

		totalItems, err := e.App.CountRecords("sms_devices", countExprs...)
		if err != nil {
			return apis.NewApiError(http.StatusInternalServerError, "Failed to count devices", nil)
		}
		totalPages := int(totalItems) / perPage
		if int(totalItems)%perPage != 0 {
			totalPages++
		}

		items := make([]map[string]any, 0, len(records))
		for _, r := range records {
			items = append(items, map[string]any{
				"id":           r.Id,
				"name":         r.GetString("name"),
				"device_type":  r.GetString("device_type"),
				"phone_number": r.GetString("phone_number"),
				"created":      r.GetDateTime("created"),
				"updated":      r.GetDateTime("updated"),
			})
		}

		return e.JSON(http.StatusOK, map[string]any{
			"items":       items,
			"page":        page,
			"per_page":    perPage,
			"total_items": totalItems,
			"total_pages": totalPages,
		})
	})
}
