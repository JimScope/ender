package handlers

import (
	"bufio"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"vendel/middleware"
)

var phoneCleanRegex = regexp.MustCompile(`[^\d+]`)

// RegisterContactRoutes registers contact-related API routes.
func RegisterContactRoutes(se *core.ServeEvent) {
	// POST /api/contacts/import — import contacts from vCard (.vcf) file
	se.Router.POST("/api/contacts/import", func(e *core.RequestEvent) error {
		userId := e.Auth.Id

		file, _, err := e.Request.FormFile("file")
		if err != nil {
			return apis.NewBadRequestError("File is required", nil)
		}
		defer file.Close()

		groupId := e.Request.FormValue("group_id")
		if groupId != "" {
			// The group must belong to the importing user — otherwise contacts
			// could be attached to another user's group.
			group, err := e.App.FindRecordById("contact_groups", groupId)
			if err != nil || group.GetString("user") != userId {
				return apis.NewBadRequestError("Invalid group", nil)
			}
		}

		contacts := parseVCard(file)
		if len(contacts) == 0 {
			return apis.NewBadRequestError("No valid contacts found in file", nil)
		}

		collection, err := e.App.FindCollectionByNameOrId("contacts")
		if err != nil {
			return apis.NewApiError(http.StatusInternalServerError, "contacts collection not found", nil)
		}

		imported, skipped := 0, 0
		for _, c := range contacts {
			// Skip if phone already exists for this user
			existing, _ := e.App.FindFirstRecordByFilter(
				"contacts",
				"user = {:userId} && phone_number = {:phone}",
				dbx.Params{"userId": userId, "phone": c.phone},
			)
			if existing != nil {
				skipped++
				continue
			}

			record := core.NewRecord(collection)
			record.Set("user", userId)
			record.Set("name", c.name)
			record.Set("phone_number", c.phone)
			if groupId != "" {
				record.Set("groups", []string{groupId})
			}

			if err := e.App.Save(record); err != nil {
				skipped++
				continue
			}
			imported++
		}

		return e.JSON(http.StatusOK, map[string]any{
			"imported": imported,
			"skipped":  skipped,
			"total":    len(contacts),
		})
	}).Bind(apis.RequireAuth("users"))

	// GET /api/contacts — list contacts (JWT + API key auth)
	se.Router.GET("/api/contacts", func(e *core.RequestEvent) error {
		userId, err := middleware.ResolveAuthOrAPIKey(e)
		if err != nil {
			return apis.NewUnauthorizedError("Authentication required", nil)
		}

		page, perPage := parsePagination(e)
		offset := (page - 1) * perPage

		filter := "user = {:userId}"
		params := dbx.Params{"userId": userId}
		// The COUNT query takes raw SQL, not PocketBase filter syntax — build
		// both in parallel so total_items matches the filtered listing.
		countExprs := []dbx.Expression{dbx.HashExp{"user": userId}}

		if search := strings.TrimSpace(e.Request.URL.Query().Get("search")); search != "" {
			filter += " && (name ~ {:search} || phone_number ~ {:search})"
			params["search"] = search
			// dbx.Like escapes %/_/\ and adds an ESCAPE clause, mirroring the
			// listing query's `~` operator — so total_items can't diverge from
			// the returned rows when a name contains a LIKE wildcard.
			countExprs = append(countExprs, dbx.Or(
				dbx.Like("name", search),
				dbx.Like("phone_number", search),
			))
		}

		if groupId := strings.TrimSpace(e.Request.URL.Query().Get("group_id")); groupId != "" {
			filter += " && groups.id ?= {:groupId}"
			params["groupId"] = groupId
			// Multi-relation values are stored as a JSON array; mirror the
			// `groups.id ?=` operator with a json_each EXISTS.
			countExprs = append(countExprs, dbx.NewExp(
				"EXISTS (SELECT 1 FROM json_each(CASE WHEN json_valid([[groups]]) THEN [[groups]] ELSE json_array([[groups]]) END) WHERE json_each.value = {:groupId})",
				dbx.Params{"groupId": groupId},
			))
		}

		records, err := e.App.FindRecordsByFilter(
			"contacts",
			filter,
			"-created",
			perPage,
			offset,
			params,
		)
		if err != nil {
			records = []*core.Record{}
		}

		totalItems, err := e.App.CountRecords("contacts", countExprs...)
		if err != nil {
			return apis.NewApiError(http.StatusInternalServerError, "Failed to count contacts", nil)
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
				"phone_number": r.GetString("phone_number"),
				"groups":       r.GetStringSlice("groups"),
				"notes":        r.GetString("notes"),
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

	// GET /api/contacts/groups — list contact groups (JWT + API key auth)
	se.Router.GET("/api/contacts/groups", func(e *core.RequestEvent) error {
		userId, err := middleware.ResolveAuthOrAPIKey(e)
		if err != nil {
			return apis.NewUnauthorizedError("Authentication required", nil)
		}

		page, perPage := parsePagination(e)
		offset := (page - 1) * perPage

		filter := "user = {:userId}"
		params := dbx.Params{"userId": userId}

		records, err := e.App.FindRecordsByFilter(
			"contact_groups",
			filter,
			"name",
			perPage,
			offset,
			params,
		)
		if err != nil {
			records = []*core.Record{}
		}

		totalItems, err := e.App.CountRecords("contact_groups", dbx.HashExp{"user": userId})
		if err != nil {
			return apis.NewApiError(http.StatusInternalServerError, "Failed to count groups", nil)
		}
		totalPages := int(totalItems) / perPage
		if int(totalItems)%perPage != 0 {
			totalPages++
		}

		items := make([]map[string]any, 0, len(records))
		for _, r := range records {
			items = append(items, map[string]any{
				"id":      r.Id,
				"name":    r.GetString("name"),
				"created": r.GetDateTime("created"),
				"updated": r.GetDateTime("updated"),
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

type vcardEntry struct {
	name  string
	phone string
}

// parseVCard extracts name + phone from a vCard file.
// Supports multiple contacts in a single .vcf file.
func parseVCard(r io.Reader) []vcardEntry {
	var entries []vcardEntry
	var current vcardEntry
	inCard := false

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		switch {
		case line == "BEGIN:VCARD":
			inCard = true
			current = vcardEntry{}

		case line == "END:VCARD":
			if inCard && current.name != "" && current.phone != "" {
				entries = append(entries, current)
			}
			inCard = false

		case inCard && strings.HasPrefix(line, "FN:"):
			current.name = strings.TrimPrefix(line, "FN:")

		case inCard && strings.Contains(line, "TEL"):
			// TEL:+123... or TEL;TYPE=CELL:+123... or TEL;TYPE=CELL;VALUE=uri:tel:+123...
			phone := line
			if idx := strings.LastIndex(phone, ":"); idx >= 0 {
				phone = phone[idx+1:]
			}
			// Clean non-digit chars except +
			phone = phoneCleanRegex.ReplaceAllString(phone, "")
			if e164Regex.MatchString(phone) && current.phone == "" {
				current.phone = phone
			}
		}
	}

	return entries
}

// parsePagination extracts page and per_page query params with defaults and bounds.
func parsePagination(e *core.RequestEvent) (page int, perPage int) {
	page = 1
	perPage = 50

	if v := e.Request.URL.Query().Get("page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			page = p
		}
	}

	if v := e.Request.URL.Query().Get("per_page"); v != "" {
		if pp, err := strconv.Atoi(v); err == nil && pp > 0 {
			perPage = pp
		}
	}

	if perPage > 200 {
		perPage = 200
	}

	return page, perPage
}
