// Command inventoryd seeds a demo inventory and prints its summary.
package main

import (
	"fmt"
	"os"

	"inventoryd/internal/api"
	"inventoryd/internal/report"
	"inventoryd/internal/store"
)

func main() {
	svc := api.NewService(store.New(), nil)
	seed := []store.Item{
		{ID: "A-1", Name: "油壶", PriceCents: 4900, Stock: 12},
		{ID: "B-2", Name: "turkey baster", PriceCents: 1299, Stock: 3},
		{ID: "C-3", Name: "赠品袋", PriceCents: 0, Stock: 120},
	}
	for _, it := range seed {
		if _, err := svc.CreateItem(it); err != nil {
			fmt.Fprintln(os.Stderr, "seed:", err)
			os.Exit(1)
		}
	}
	items, err := svc.QueryItems("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "query:", err)
		os.Exit(1)
	}
	sum := report.Build("季度库存报告——华东区", items)
	fmt.Println(sum.Title)
	for _, line := range sum.Display {
		fmt.Println(line)
	}
	fmt.Printf("total: %d cents\n", sum.TotalCents)
}
