package collector

import "time"

type distribution map[string]int
type FeatureSummary struct {
	AutoCuration  int          `json:"auto_curation"`
	StewardModel  int          `json:"steward_model"`
	MCPToken      int          `json:"mcp_token"`
	LANDevices    int          `json:"lan_devices"`
	DSP           int          `json:"dsp"`
	ScheduledScan int          `json:"scheduled_scan"`
	Plugins       distribution `json:"plugins"`
}

type Summary struct {
	GeneratedAt time.Time               `json:"generated_at"`
	Active7d    int                     `json:"active_7d"`
	Active30d   int                     `json:"active_30d"`
	Versions    distribution            `json:"versions"`
	Arch        distribution            `json:"arch"`
	Container   distribution            `json:"container"`
	Library     map[string]distribution `json:"library"`
	Members     distribution            `json:"members"`
	Features    FeatureSummary          `json:"features"`
	Devices30d  map[string]distribution `json:"devices_30d"`
}

func (s *Store) Summary(now time.Time) Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := Summary{GeneratedAt: now.UTC(), Versions: distribution{}, Arch: distribution{}, Container: distribution{"true": 0, "false": 0},
		Library: map[string]distribution{"tracks": {}, "releases": {}}, Members: distribution{}, Features: FeatureSummary{Plugins: distribution{}},
		Devices30d: map[string]distribution{"browser": {}, "upnp": {}}}
	week, month := now.Add(-7*24*time.Hour), now.Add(-30*24*time.Hour)
	for _, entry := range s.instances {
		if entry.LastSeen.Before(month) {
			continue
		}
		result.Active30d++
		if !entry.LastSeen.Before(week) {
			result.Active7d++
		}
		p := entry.Payload
		result.Versions[p.Version]++
		result.Arch[p.Arch]++
		if p.Container {
			result.Container["true"]++
		} else {
			result.Container["false"]++
		}
		result.Library["tracks"][p.Library.Tracks]++
		result.Library["releases"][p.Library.Releases]++
		result.Members[p.Members]++
		if p.Features.AutoCuration {
			result.Features.AutoCuration++
		}
		if p.Features.StewardModel {
			result.Features.StewardModel++
		}
		if p.Features.MCPToken {
			result.Features.MCPToken++
		}
		if p.Features.LANDevices {
			result.Features.LANDevices++
		}
		if p.Features.DSP {
			result.Features.DSP++
		}
		if p.Features.ScheduledScan {
			result.Features.ScheduledScan++
		}
		result.Features.Plugins[p.Features.Plugins]++
		result.Devices30d["browser"][p.Devices30d.Browser]++
		result.Devices30d["upnp"][p.Devices30d.UPnP]++
	}
	return result
}
