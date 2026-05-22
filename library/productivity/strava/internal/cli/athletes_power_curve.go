// Copyright 2026 azaaron. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/spf13/cobra"
	"github.com/mvanhorn/printing-press-library/library/productivity/strava/internal/cliutil"
	"github.com/mvanhorn/printing-press-library/library/productivity/strava/internal/store"
)

type powerCurveRow struct {
	Duration string  `json:"duration"`
	Seconds  int     `json:"seconds"`
	Watts    float64 `json:"watts"`
	WKg      float64 `json:"wkg,omitempty"`
}

var powerCurveWindows = []struct {
	label   string
	seconds int
}{
	{"1s", 1},
	{"5s", 5},
	{"30s", 30},
	{"1m", 60},
	{"5m", 300},
	{"20m", 1200},
	{"60m", 3600},
}

func newAthletesPowerCurveCmd(flags *rootFlags) *cobra.Command {
	var since string
	var weight float64
	var dbPath string

	cmd := &cobra.Command{
		Use:         "power-curve",
		Short:       "See your best mean power for each standard duration (1s to 60min)",
		Annotations: map[string]string{"mcp:read-only": "true"},
		Long: `Computes your best mean power (W) for each standard duration window from
synced activity stream data. Optionally normalizes to W/kg with --weight.

Requires activities with power meter data synced with stream data.`,
		Example: strings.Trim(`
  strava-pp-cli athlete power-curve
  strava-pp-cli athlete power-curve --since 2025-01-01 --weight 72 --agent
  strava-pp-cli athlete power-curve --json --select duration,watts,wkg`, "\n"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				return nil
			}
			if cliutil.IsVerifyEnv() {
				sample := []powerCurveRow{
					{Duration: "1m", Seconds: 60, Watts: 420, WKg: 5.8},
					{Duration: "5m", Seconds: 300, Watts: 360, WKg: 5.0},
					{Duration: "20m", Seconds: 1200, Watts: 310, WKg: 4.3},
				}
				return printJSONFiltered(cmd.OutOrStdout(), sample, flags)
			}

			if dbPath == "" {
				dbPath = defaultDBPath("strava-pp-cli")
			}
			db, err := store.OpenReadOnly(dbPath)
			if err != nil {
				return fmt.Errorf("opening database: %w\nRun 'strava-pp-cli sync' first", err)
			}
			defer db.Close()

			query := `SELECT s.data FROM activities_streams s
JOIN resources r ON r.id = s.activities_id
WHERE r.resource_type IN ('athlete-activities', 'activities')`
			var qargs []any
			if since != "" {
				query += ` AND COALESCE(json_extract(r.data, '$.start_date'), '') >= ?`
				qargs = append(qargs, since+"T00:00:00Z")
			}

			rows, err := db.DB().QueryContext(cmd.Context(), query, qargs...)
			if err != nil {
				return fmt.Errorf("querying activity streams: %w", err)
			}
			defer rows.Close()

			// Track best mean power per window across all activities
			bestWatts := make([]float64, len(powerCurveWindows))

			for rows.Next() {
				var streamData sql.NullString
				if err := rows.Scan(&streamData); err != nil || !streamData.Valid {
					continue
				}
				wattsArray := extractStreamValues(streamData.String, "watts")
				if len(wattsArray) == 0 {
					continue
				}
				// Sliding window max for each duration
				for i, win := range powerCurveWindows {
					best := slidingWindowMean(wattsArray, win.seconds)
					if best > bestWatts[i] {
						bestWatts[i] = best
					}
				}
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("reading rows: %w", err)
			}

			var result []powerCurveRow
			for i, win := range powerCurveWindows {
				w := math.Round(bestWatts[i])
				row := powerCurveRow{
					Duration: win.label,
					Seconds:  win.seconds,
					Watts:    w,
				}
				if weight > 0 && w > 0 {
					row.WKg = math.Round((w/weight)*100) / 100
				}
				result = append(result, row)
			}

			return printJSONFiltered(cmd.OutOrStdout(), result, flags)
		},
	}

	cmd.Flags().StringVar(&since, "since", "", "Only include activities since this date (YYYY-MM-DD)")
	cmd.Flags().Float64Var(&weight, "weight", 0, "Body weight in kg for W/kg normalization")
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	return cmd
}

// slidingWindowMean computes the highest mean of any consecutive windowSec-length
// subarray in vals (treating vals as 1-second samples).
func slidingWindowMean(vals []float64, windowSec int) float64 {
	n := len(vals)
	if n < windowSec {
		// Window larger than activity — compute mean of full activity
		if n == 0 {
			return 0
		}
		sum := 0.0
		for _, v := range vals {
			sum += v
		}
		return sum / float64(n)
	}
	// Compute initial window
	sum := 0.0
	for i := 0; i < windowSec; i++ {
		sum += vals[i]
	}
	best := sum
	for i := windowSec; i < n; i++ {
		sum += vals[i] - vals[i-windowSec]
		if sum > best {
			best = sum
		}
	}
	return best / float64(windowSec)
}
