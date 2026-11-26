package musicbrainz

import "github.com/qlqq/roomusic/backend/internal/sources"

func credits(cs []ArtistCredit) []sources.Credit {
	out := make([]sources.Credit, 0, len(cs))
	for _, c := range cs {
		out = append(out, sources.Credit{ArtistID: c.Artist.ID, Name: c.Name, JoinPhrase: c.JoinPhrase})
	}
	return out
}

func release(e Entity, complete bool) sources.Release {
	r := sources.Release{ID: e.ID, Title: e.Title, Disambiguation: e.Disambiguation, Artists: credits(e.ArtistCredit), Date: e.Date, Country: e.Country, Barcode: e.Barcode, Status: e.Status, Complete: complete}
	if g := e.ReleaseGroup; g != nil {
		r.Group = sources.Group{ID: g.ID, Title: g.Title, PrimaryType: g.PrimaryType, SecondaryTypes: g.SecondaryTypes, FirstReleaseDate: g.FirstReleaseDate}
	}
	for _, l := range e.LabelInfo {
		r.Labels = append(r.Labels, sources.Label{Name: l.Label.Name, CatalogNumber: l.CatalogNumber})
	}
	for _, m := range e.Media {
		medium := sources.Medium{Position: m.Position, Format: m.Format, Title: m.Title, TrackCount: m.TrackCount}
		for _, d := range m.Discs {
			medium.DiscIDs = append(medium.DiscIDs, d.ID)
		}
		if complete {
			for _, t := range m.Tracks {
				track := sources.Track{ID: t.ID, Position: t.Position, Number: t.Number, Title: t.Title, LengthMS: t.Length, Artists: credits(t.ArtistCredit)}
				if rec := t.Recording; rec != nil {
					track.RecordingID = rec.ID
					track.RecordingTitle = rec.Title
					track.ISRCs = rec.ISRCs
				}
				medium.Tracks = append(medium.Tracks, track)
			}
		}
		r.Media = append(r.Media, medium)
	}
	return r
}

func releases(items []Entity) []sources.Release {
	out := make([]sources.Release, 0, len(items))
	for _, e := range items {
		out = append(out, release(e, false))
	}
	return out
}
func recording(e Entity) sources.Recording {
	return sources.Recording{ID: e.ID, Title: e.Title, LengthMS: e.Length, Artists: credits(e.ArtistCredit), ISRCs: e.ISRCs, Releases: releases(e.Releases[:min(25, len(e.Releases))])}
}
func recordings(items []Entity) []sources.Recording {
	out := make([]sources.Recording, 0, len(items))
	for _, e := range items {
		out = append(out, recording(e))
	}
	return out
}
func artist(e Entity) sources.Artist {
	a := sources.Artist{ID: e.ID, Name: e.Name, SortName: e.SortName, Disambiguation: e.Disambiguation, Type: e.Type, Country: e.Country}
	for _, alias := range e.Aliases {
		a.Aliases = append(a.Aliases, sources.Alias{Name: alias.Name, Locale: alias.Locale, Primary: alias.Primary})
	}
	return a
}
