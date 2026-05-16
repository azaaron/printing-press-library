// Copyright 2026 gregce. Licensed under Apache-2.0. See LICENSE.

// PATCH(library): SSE parser for prod-stream.tella.tv/ai-mistakes/analyze-scene.
// The unofficial AI service streams mistakes as Server-Sent Events; each
// `data: ` line is a JSON array of the form `["Mistakes", [{...}, ...]]`.
// The stream terminates when the AI pass finishes (no explicit close event
// observed in the HAR — io.EOF on the body is the signal). Cataloged in
// .printing-press-patches.json#add-cut-panel-parity.

package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// detectedMistake mirrors the per-mistake object Tella's AI service emits.
// Field names are camelCase to match the wire format verbatim, simplifying
// debugging when an envelope is logged.
type detectedMistake struct {
	Trim struct {
		StartTime float64 `json:"startTime"`
		Duration  float64 `json:"duration"`
	} `json:"trim"`
	Reasoning  string  `json:"reasoning"`
	WordsToCut string  `json:"wordsToCut"`
	Confidence float64 `json:"confidence"`
	RawStart   string  `json:"rawStart"`
	RawEnd     string  `json:"rawEnd"`
}

// parseMistakesSSE reads a Server-Sent Events stream from analyze-scene and
// returns every detectedMistake it finds. The body is read to completion
// (or the first non-`data:` line that fails to decode), then the slice is
// returned in stream order.
//
// SSE wire shape captured 2026-05-16:
//
//	data: ["Mistakes",[{"trim":{...}, "reasoning":..., ...}]]
//	(blank line)
//	data: ["Mistakes",[{...}]]
//	(blank line)
//
// Each event payload is a 2-tuple: [event_type, payload]. We only act on
// event_type == "Mistakes" today; future event kinds are surfaced via the
// `unknownEvents` count so a future regen can decide what to do with them.
func parseMistakesSSE(body io.Reader) ([]detectedMistake, int, error) {
	out := []detectedMistake{}
	unknownEvents := 0
	scanner := bufio.NewScanner(body)
	// analyze-scene events can include long `wordsToCut` strings; give the
	// scanner a generous buffer to avoid `bufio.Scanner: token too long`.
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		raw := scanner.Text()
		if !strings.HasPrefix(raw, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(raw, "data:"))
		if payload == "" {
			continue
		}
		var tuple [2]json.RawMessage
		if err := json.Unmarshal([]byte(payload), &tuple); err != nil {
			// Some SSE servers send the event type as a separate line
			// (`event: name\ndata: payload`); fall back to parsing the
			// data line as a bare mistakes array if it isn't a 2-tuple.
			var direct []detectedMistake
			if derr := json.Unmarshal([]byte(payload), &direct); derr == nil {
				out = append(out, direct...)
				continue
			}
			unknownEvents++
			continue
		}
		var eventType string
		if err := json.Unmarshal(tuple[0], &eventType); err != nil {
			unknownEvents++
			continue
		}
		if eventType != "Mistakes" {
			unknownEvents++
			continue
		}
		var batch []detectedMistake
		if err := json.Unmarshal(tuple[1], &batch); err != nil {
			return out, unknownEvents, fmt.Errorf("decoding mistakes batch: %w (line: %s)", err, truncate(payload, 200))
		}
		out = append(out, batch...)
	}
	if err := scanner.Err(); err != nil {
		return out, unknownEvents, fmt.Errorf("reading SSE stream: %w", err)
	}
	return out, unknownEvents, nil
}

// mistakesToCuts maps detected mistakes into the `{startTime, duration}`
// shape the web UI's apply-PATCH expects on www.tella.tv. The unofficial
// PATCH endpoint uses startTime+duration, NOT fromMs+toMs like the public
// /cut endpoint, so this helper keeps the shape mapping in one place.
func mistakesToCuts(mistakes []detectedMistake) []map[string]any {
	out := make([]map[string]any, 0, len(mistakes))
	for _, m := range mistakes {
		if m.Trim.Duration <= 0 {
			continue
		}
		out = append(out, map[string]any{
			"startTime": m.Trim.StartTime,
			"duration":  m.Trim.Duration,
		})
	}
	return out
}
