package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/Subhashis-1/traceforge/internal/models"
	"github.com/Subhashis-1/traceforge/internal/storage"
)

const (
	defaultSearchLimit = 50
	maxSearchLimit     = 1000
)

type searchResponse struct {
	Hits  []*models.Trace `json:"hits"`
	Total int             `json:"total"`
}

func RegisterSearchRoutes(g *echo.Group) {
	g.GET("/search", SearchHandler)
}

func SearchHandler(c echo.Context) error {
	rawQuery := strings.TrimSpace(c.QueryParam("q"))
	if rawQuery == "" {
		return c.JSON(http.StatusBadRequest, Error{
			Code:    http.StatusBadRequest,
			Message: "query parameter 'q' is required",
		})
	}

	parsedQuery, err := ParseDSL(rawQuery)
	if err != nil {
		return c.JSON(http.StatusBadRequest, Error{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
	}

	limit := defaultSearchLimit
	if limitParam := c.QueryParam("limit"); limitParam != "" {
		parsedLimit, err := strconv.Atoi(limitParam)
		if err != nil {
			return c.JSON(http.StatusBadRequest, Error{
				Code:    http.StatusBadRequest,
				Message: "invalid 'limit' parameter: must be an integer",
			})
		}
		switch {
		case parsedLimit < 1:
			limit = 1
		case parsedLimit > maxSearchLimit:
			limit = maxSearchLimit
		default:
			limit = parsedLimit
		}
	}

	cql, args := buildSearchCQL(parsedQuery, limit)
	_ = cql
	_ = args

	repo, ok := c.Get("repo").(storage.Repository)
	if !ok || repo == nil {
		return c.JSON(http.StatusInternalServerError, Error{
			Code:    http.StatusInternalServerError,
			Message: "search repository not configured",
		})
	}

	hits, err := repo.SearchTraces(c.Request().Context(), parsedQuery, limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, Error{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("search traces: %v", err),
		})
	}

	return c.JSON(http.StatusOK, searchResponse{
		Hits:  hits,
		Total: len(hits),
	})
}

func buildSearchCQL(q *Query, limit int) (string, []interface{}) {
	clauses := make([]string, 0, len(q.Tags)+3)
	args := make([]interface{}, 0, len(q.Tags)+4)

	if q.Service != "" {
		clauses = append(clauses, "service_name = ?")
		args = append(args, q.Service)
	}

	if q.StatusOp != "" {
		clauses = append(clauses, fmt.Sprintf("status %s ?", q.StatusOp))
		args = append(args, q.StatusVal)
	}

	if q.DurationOp != "" {
		clauses = append(clauses, fmt.Sprintf("duration %s ?", q.DurationOp))
		args = append(args, q.DurationMs)
	}

	if len(q.Tags) > 0 {
		keys := make([]string, 0, len(q.Tags))
		for key := range q.Tags {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		for _, key := range keys {
			clauses = append(clauses, fmt.Sprintf("tags['%s'] = ?", key))
			args = append(args, q.Tags[key])
		}
	}

	var builder strings.Builder
	builder.WriteString("SELECT service_name, date_bucket, start_time, trace_id, root_span_id,\n")
	builder.WriteString("       duration, status, tags\n")
	builder.WriteString("FROM traceforge.traces_by_service")
	if len(clauses) > 0 {
		builder.WriteString("\nWHERE ")
		builder.WriteString(strings.Join(clauses, " AND "))
	}
	builder.WriteString("\nLIMIT ?")

	args = append(args, limit)
	return builder.String(), args
}
