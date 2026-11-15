package parser

// offerArtwork 按正面封面优先、否则先到先得的顺序接收候选图；负数类型表示格式不带图片类型。
func offerArtwork(o *AudioObservation, data []byte, mime string, pictureType int) {
	if len(data) == 0 || len(data) > 16<<20 {
		return
	}
	front := pictureType == 3
	if len(o.Artwork) > 0 && (o.artworkFront || !front) {
		return
	}
	o.Artwork = append([]byte(nil), data...)
	o.ArtworkMIME = mime
	o.artworkFront = front
}
