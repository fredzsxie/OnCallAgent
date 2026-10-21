package middleware

import (
	"bytes"
	"context"
	"strings"

	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/olekukonko/tablewriter"
)

func PrintRoutes(ctx context.Context, server *ghttp.Server) {
	var buffer bytes.Buffer
	table := tablewriter.NewWriter(&buffer)
	table.SetHeader([]string{"ADDRESS", "METHOD", "ROUTE", "HANDLER", "MIDDLEWARE"})
	table.SetRowLine(true)
	table.SetBorder(false)
	table.SetCenterSeparator("|")
	count := 0
	for _, route := range server.GetRoutes() {
		if !route.IsServiceHandler || route.Handler == nil {
			continue
		}
		middlewares := strings.Split(route.Middleware, ",")
		for i, name := range middlewares {
			middlewares[i] = strings.TrimSuffix(strings.TrimSpace(name), "-fm")
		}
		table.Append([]string{route.Address, route.Method, route.Route, strings.TrimSuffix(route.Handler.Name, "-fm"), strings.Join(middlewares, "\n")})
		count++
	}
	if count > 0 {
		table.Render()
		logger.RouterTable(ctx, buffer.String(), count)
	}
}
