// Copyright 2026 azaaron. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/mvanhorn/printing-press-library/library/productivity/strava/internal/cliutil"
	"github.com/mvanhorn/printing-press-library/library/productivity/strava/internal/store"
)

type zoneWeekRow struct {
	Week       string    `json:"week"`
	Zones      []float64 `json:"zone_minutes"`
	ZoneLabels []string  `json:"zone_labels,omitempty"`
}

func newTrainingZonesCmd(flags *rootFlags) *cobra.Command {
	var weeks int
	var activityType string
	var zoneType string
	var dbPath string

	cmd := &cobra.Command{
		Use:         "zones",
		Short:       "See minutes spent in each HR or power zone per week",
		Annotations: map[string]string{"mcp:read-only": "true"},
		Long: `Shows how many minutes per week you spent in each heart rate or power zone.
Fetches zone thresholds from the Strava API, then analyzes synced stream data.
Streams must be present in the local database.`,
		Example: strings.Trim(`
  strava-pp-cli training zones --weeks 8
  strava-pp-cli training zones --weeks 4 --type Run --zone-type heartrate --agent
  strava-pp-cli training zones --json --select week,zone_minutes`, "\n"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				return nil
			}
			if cliutil.IsVerifyEnv() {
				sample := []zoneWeekRow{
					{
						Week:       "2026-05-12",
						Zones:      []float64{120.0, 90.0, 45.0, 30.0, 15.0},
						ZoneLabels: []string{"Z1", "Z2", "Z3", "Z4", "Z5"},
					},
				}
				return printJSONFiltered(cmd.OutOrStdout(), sample, flags)
			}

			if dbPath == "" {
				dbPath = defaultDBPath("strava-pp-cli")
			}

			// Fetch zone thresholds from API
			c, err := flags.newClient()
			if err != nil {
				return err
			}
			zonesData, err := c.Get(cmd.Context(), "/athlete/zones", nil)
			if err != nil {
				return fmt.Errorf("fetching athlete zones (requires profile:read_all scope): %w", err)
			}
			var zonesResponse map[string]any
			if jsonErr := json.Unmarshal(zonesData, &zonesResponse); jsonErr != nil {
				return fmt.Errorf("parsing zones response: %w", jsonErr)
			}
			thresholds, labels := extractZoneThresholds(zonesResponse, zoneType)
			numZones := len(labels)
			if numZones == 0 {
				return fmt.Errorf("no %s zones found in athlete profile; configure them at strava.com/settings/performance", zoneType)
			}

			db, err := store.OpenReadOnly(dbPath)
			if err != nil {
				return fmt.Errorf("opening database: %w\nRun 'strava-pp-cli sync' first", err)
			}
			defer db.Close()

			cutoff := time.Now().UTC().AddDate(0, 0, -(weeks * 7))
			streamKey := "heartrate"
			if zoneType == "power" {
				streamKey = "watts"
			}

			query := `SELECT r.data, s.data
FROM resources r
LEFT JOIN activities_streams s ON s.activities_id = r.id
WHERE r.resource_type IN ('athlete-activities', 'activities')
AND COALESCE(json_extract(r.data, '$.start_date'), '') >= ?`
			qargs := []any{cutoff.Format("2006-01-02T15:04:05Z")}
			if activityType != "" {
				query += ` AND json_extract(r.data, '$.type') = ?`
				qargs = append(qargs, activityType)
			}
			query += ` ORDER BY json_extract(r.data, '$.start_date') ASC`

			rows, err := db.DB().QueryContext(cmd.Context(), query, qargs...)
			if err != nil {
				return fmt.Errorf("querying activities with streams: %w", err)
			}
			defer rows.Close()

			weekZones := map[string][]float64{}
			var weekOrder []string

			for rows.Next() {
				var actData, streamDataRaw sql.NullString
				if err := rows.Scan(&actData, &streamDataRaw); err != nil || !actData.Valid {
					continue
				}
				var act map[string]any
				if err := json.Unmarshal([]byte(actData.String), &act); err != nil {
					continue
				}
				startRaw, _ := act["start_date"].(string)
				if startRaw == "" {
					continue
				}
				t, err := time.Parse(time.RFC3339, startRaw)
				if err != nil {
					continue
				}

				// Monday-anchored week key
				weekDay := int(t.Weekday())
				if weekDay == 0 {
					weekDay = 7
				}
				wk := t.UTC().AddDate(0, 0, -(weekDay-1)).Format("2006-01-02")
				if _, seen := weekZones[wk]; !seen {
					weekZones[wk] = make([]float64, numZones)
					weekOrder = append(weekOrder, wk)
				}
				if !streamDataRaw.Valid {
					continue
				}
				values := extractStreamValues(streamDataRaw.String, streamKey)
				for _, val := range values {
					z := classifyZone(val, thresholds)
					if z >= 0 && z < numZones {
						weekZones[wk][z] += 1.0 / 60.0 // seconds → minutes
					}
				}
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("reading rows: %w", err)
			}

			var result []zoneWeekRow
			for _, wk := range weekOrder {
				result = append(result, zoneWeekRow{
					Week:       wk,
					Zones:      roundFloats(weekZones[wk]),
					ZoneLabels: labels,
				})
			}
			return printJSONFiltered(cmd.OutOrStdout(), result, flags)
		},
	}

	cmd.Flags().IntVar(&weeks, "weeks", 8, "Number of weeks to analyze")
	cmd.Flags().StringVar(&activityType, "type", "", "Filter by activity type (Run, Ride, Swim, etc.)")
	cmd.Flags().StringVar(&zoneType, "zone-type", "heartrate", "Zone type: heartrate or power")
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	return cmd
}

// extractZoneThresholds parses /athlete/zones response into thresholds and labels.
func extractZoneThresholds(zones map[string]any, zoneType string) ([]float64, []string) {
	key := "heart_rate"
	if zoneType == "power" {
		key = "power"
	}
	zd, ok := zones[key].(map[string]any)
	if !ok {
		return nil, nil
	}
	buckets, ok := zd["zones"].([]any)
	if !ok {
		return nil, nil
	}
	var thresholds []float64
	var labels []string
	for i, b := range buckets {
		bm, _ := b.(map[string]any)
		thresholds = append(thresholds, jsonFloat(bm, "min"))
		labels = append(labels, fmt.Sprintf("Z%d", i+1))
	}
	thresholds = append(thresholds, 9999) // sentinel max
	return thresholds, labels
}

// classifyZone returns the 0-based zone index for val given sorted zone min thresholds.
func classifyZone(val float64, thresholds []float64) int {
	for i := len(thresholds) - 2; i >= 0; i-- {
		if val >= thresholds[i] {
			return i
		}
	}
	return 0
}

// roundFloats rounds each element to 1 decimal place.
func roundFloats(vals []float64) []float64 {
	out := make([]float64, len(vals))
	for i, v := range vals {
		out[i] = float64(int(v*10+0.5)) / 10
	}
	return out
}
