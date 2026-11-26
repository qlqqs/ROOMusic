package upstreammock

import _ "embed"

//go:embed testdata/artist-portrait.png
var artistPortrait []byte

const artistIntro = "海岸来信是为测试创作的独立音乐组合，作品以潮汐和沿岸生活为主题。"

const artistDirectoryHTML = `<!doctype html><html lang="zh-CN"><head><title>沿岸音乐名录</title></head><body>
<p>纸屿二重奏（Wikidata Q900001）与同名独奏者分别录制潮声作品。</p>
<p>` + artistIntro + `</p><img src="portrait.png" alt="海岸来信"></body></html>`

func artistSearchPage(base string) object {
	return object{"title": "沿岸音乐名录", "url": base + "/searxng/artists/coast", "content": "纸屿二重奏（Wikidata Q900001）与同名独奏者分别录制潮声作品。" + artistIntro, "image": base + "/searxng/artists/portrait.png"}
}

func (s script) artistCalls(base, item string, image, bio bool) []call {
	page := base + "/searxng/artists/coast"
	if s.ArtistScenario == "media" {
		if !image && !bio {
			return []call{{"shelve_pending_item", object{"item_id": item, "reason": "只查到艺人资料，没有找到 MusicBrainz 编号。"}}}
		}
		args := object{"item_id": item, "citations": []object{{"url": page, "title": "沿岸音乐名录"}}}
		if image {
			args["image"] = object{"url": base + "/searxng/artists/portrait.png", "page_url": page}
		}
		if bio {
			args["bio"] = object{"text": artistIntro, "lang": "zh"}
		}
		return []call{{"submit_artist_media", args}}
	}
	identity := func(source, id, note string) call {
		return call{"resolve_artist_identity", object{"item_id": item, "source": source, "external_id": id, "basis": object{"note": note, "citations": []string{page}}}}
	}
	if s.ArtistScenario == "candidates" {
		return []call{identity("musicbrainz", "10000000-0000-4000-8000-000000000001", "署名与二重奏相同，还需核对。"), identity("musicbrainz", "10000000-0000-4000-8000-000000000003", "署名与独奏者相同，还需核对。")}
	}
	return []call{identity("musicbrainz", "10000000-0000-4000-8000-000000000001", "合成名录将专辑和曲目列在纸屿二重奏名下。")}
}
