package main

import (
	"encoding/json"
	"sort"
)

type serviceUnitTotal struct {
	TokenID  int     `json:"token_id"`
	Pack     string  `json:"pack"`
	Unit     string  `json:"unit"`
	Source   string  `json:"source"`
	Mode     string  `json:"mode"`
	Quantity float64 `json:"quantity"`
}

type serviceTraceSummary struct {
	Requests         int                `json:"requests"`
	Attempts         int                `json:"attempts"`
	UnknownOutcomes  int                `json:"unknown_outcomes"`
	UnknownCosts     int                `json:"unknown_costs"`
	CostByStatus     map[string]float64 `json:"cost_usd_by_status"`
	Units            []serviceUnitTotal `json:"units"`
	UnkeyedSnapshots int                `json:"unkeyed_snapshots"`
}

// Aggregate the complete filtered history, independently of display pagination.
// Only request spans carry usage. Repeated observations of one resource keep
// the newest snapshot; request parameters remain separate from provider values.
func (store *localStore) serviceTraceSummary(owner int, traceID, taskID string) (serviceTraceSummary, error) {
	result := serviceTraceSummary{CostByStatus: map[string]float64{}, Units: []serviceUnitTotal{}}
	rows, err := store.db.Query(`SELECT payload FROM service_traces WHERE (?<=0 OR token_id=?) AND (?='' OR trace_id=?) AND (?='' OR task_id=?) ORDER BY started_at DESC, span_id DESC`, owner, owner, traceID, traceID, taskID, taskID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	totals := map[string]serviceUnitTotal{}
	seen := map[string]bool{}
	for rows.Next() {
		var raw string
		var item serviceTrace
		if err := rows.Scan(&raw); err != nil {
			return result, err
		}
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return result, err
		}
		if item.Kind == "attempt" {
			result.Attempts++
		}
		if item.Kind != "request" {
			continue
		}
		result.Requests++
		if item.Outcome == "outcome_unknown" {
			result.UnknownOutcomes++
		}
		if item.UpstreamCalled {
			if item.Cost == nil {
				result.UnknownCosts++
			} else {
				result.CostByStatus[item.Cost.Status] += item.Cost.AmountUSD
			}
		}
		for _, unit := range item.Units {
			key := serviceDigest([]any{item.TokenID, item.Pack, unit.Unit, unit.Source, unit.Mode})
			if unit.Mode == "snapshot" {
				if item.ResourceRef == "" {
					result.UnkeyedSnapshots++
					continue
				}
				snapshot := key + ":" + item.ResourceRef
				if seen[snapshot] {
					continue
				}
				seen[snapshot] = true
			}
			total := totals[key]
			total.TokenID, total.Pack, total.Unit, total.Source, total.Mode = item.TokenID, item.Pack, unit.Unit, unit.Source, unit.Mode
			total.Quantity += unit.Quantity
			totals[key] = total
		}
	}
	keys := make([]string, 0, len(totals))
	for key := range totals {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result.Units = append(result.Units, totals[key])
	}
	return result, rows.Err()
}
