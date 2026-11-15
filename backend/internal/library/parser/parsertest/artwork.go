package parsertest

import "encoding/binary"

// FLACPicture 返回带中文描述的合成 PICTURE 块体，不含 FLAC 元数据块头。
func FLACPicture(pictureType uint32, mime string, width, height uint32, data []byte) []byte {
	result := binary.BigEndian.AppendUint32(nil, pictureType)
	result = binary.BigEndian.AppendUint32(result, uint32(len(mime)))
	result = append(result, mime...)
	description := "合成封面"
	result = binary.BigEndian.AppendUint32(result, uint32(len(description)))
	result = append(result, description...)
	for _, value := range []uint32{width, height, 32, 0, uint32(len(data))} {
		result = binary.BigEndian.AppendUint32(result, value)
	}
	return append(result, data...)
}

// FLACWithPictures 返回 STREAMINFO 后按传入顺序排列 PICTURE 块的最小 FLAC。
func FLACWithPictures(pictures ...[]byte) []byte {
	streamInfo := flacStreamInfo([16]byte{})
	blocks := append([][]byte{streamInfo}, pictures...)
	result := []byte("fLaC")
	for index, block := range blocks {
		kind := byte(6)
		if index == 0 {
			kind = 0
		}
		if index == len(blocks)-1 {
			kind |= 0x80
		}
		result = append(result, kind, byte(len(block)>>16), byte(len(block)>>8), byte(len(block)))
		result = append(result, block...)
	}
	return result
}
