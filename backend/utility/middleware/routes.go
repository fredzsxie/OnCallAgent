package middleware

import (
	"bytes"
	"context"
	"sort"
	"strings"

	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/gin-gonic/gin"
	"github.com/olekukonko/tablewriter"
)

func PrintRoutes(ctx context.Context, engine *gin.Engine) {
	var buffer bytes.Buffer
	table := tablewriter.NewWriter(&buffer)
	table.SetHeader([]string{"METHOD", "ROUTE", "HANDLER"})
	table.SetRowLine(true)
	table.SetBorder(false)
	table.SetCenterSeparator("|")
	routes := engine.Routes()
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	for _, route := range routes {
		table.Append([]string{route.Method, route.Path, strings.TrimSuffix(route.Handler, "-fm")})
	}
	if len(routes) > 0 {
		table.Render()
		logger.RouterTable(ctx, buffer.String(), len(routes))
	}
}
