package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/MeKo-Christian/accessdump/extract"
	"github.com/spf13/cobra"
)

var (
	queriesJSON    bool
	queriesSummary bool
)

var queriesCmd = &cobra.Command{
	Use:   "queries [file]",
	Short: "Print the SQL of all saved queries in an Access file",
	Long: "queries rebuilds the SQL of every saved query, including the embedded " +
		"~sq_ record and row sources of forms, reports and controls. Queries that " +
		"cannot be rebuilt are listed with the reason. Passwords in connect strings " +
		"are redacted.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		queries, err := extract.Queries(args[0], nil)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()

		switch {
		case queriesJSON:
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")

			return enc.Encode(queries)
		case queriesSummary:
			printQuerySummary(out, args[0], queries)
		default:
			printQueries(out, queries)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(queriesCmd)
	queriesCmd.Flags().BoolVar(&queriesJSON, "json", false, "Output as JSON")
	queriesCmd.Flags().BoolVar(&queriesSummary, "summary", false,
		"Only count queries by type and status and list those not reconstructed")
}

func printQueries(out io.Writer, queries []extract.Query) {
	for _, q := range queries {
		fmt.Fprintf(out, "-- %s (%s", q.Name, q.Type)

		if q.Owner != nil {
			fmt.Fprintf(out, ", %s %s", q.Owner.Kind, q.Owner.Object)

			if q.Owner.Control != "" {
				fmt.Fprintf(out, ".%s", q.Owner.Control)
			}
		}

		fmt.Fprintln(out, ")")

		if q.Connect != "" {
			fmt.Fprintf(out, "-- Connect: %s\n", q.Connect)
		}

		if q.SQLStatus != extract.SQLStatusFound {
			fmt.Fprintf(out, "-- SQL not available: %s", q.SQLStatus)

			if q.Reason != "" {
				fmt.Fprintf(out, " (%s)", q.Reason)
			}

			fmt.Fprint(out, "\n\n")

			continue
		}

		fmt.Fprintf(out, "%s\n\n", q.SQL)
	}
}

type queryCount struct {
	total, found int
}

func printQuerySummary(out io.Writer, path string, queries []extract.Query) {
	byType := map[extract.QueryType]*queryCount{}
	all := queryCount{}

	var missing []extract.Query

	for _, q := range queries {
		c, ok := byType[q.Type]
		if !ok {
			c = &queryCount{}
			byType[q.Type] = c
		}

		c.total++
		all.total++

		if q.SQLStatus == extract.SQLStatusFound {
			c.found++
			all.found++
		} else {
			missing = append(missing, q)
		}
	}

	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, string(t))
	}

	sort.Strings(types)

	fmt.Fprintf(out, "file: %s queries: %d reconstructed: %d missing: %d\n",
		path, all.total, all.found, all.total-all.found)

	fmt.Fprintf(out, "%-16s %6s %6s %6s\n", "TYPE", "TOTAL", "FOUND", "MISS")

	for _, t := range types {
		c := byType[extract.QueryType(t)]
		fmt.Fprintf(out, "%-16s %6d %6d %6d\n", t, c.total, c.found, c.total-c.found)
	}

	if len(missing) == 0 {
		return
	}

	fmt.Fprintln(out, "\nnot reconstructed:")

	for _, q := range missing {
		reason := string(q.SQLStatus)
		if q.Reason != "" {
			reason += ": " + q.Reason
		}

		fmt.Fprintf(out, "  %s [%s] %s\n", q.Name, q.Type, strings.TrimSpace(reason))
	}
}
